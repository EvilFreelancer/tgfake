#!/bin/sh
# Check scripts/install.sh against a snapshot build, with no GitHub involved:
# the archives of dist/ (goreleaser release --snapshot, `make snapshot`) are
# served from a local HTTP server laid out the way release downloads are
# (<base>/<tag>/<asset>), the installer fetches the host's archive from it,
# and the binary it installs has to report the snapshot's version. A tampered
# checksum has to stop the install.
#
#   scripts/test-install.sh [DIST]        # DIST defaults to ./dist
set -eu

DIST="${1:-dist}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ -f "$DIST/metadata.json" ] || { echo "no $DIST/metadata.json: run make snapshot first" >&2; exit 1; }
VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$DIST/metadata.json" | head -n 1)"
[ -n "$VERSION" ] || { echo "no version in $DIST/metadata.json" >&2; exit 1; }
TAG="v$VERSION"

PY="$(command -v python3 || command -v python)" || { echo "python3 is needed for the mirror" >&2; exit 1; }
EXE=""
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) EXE=".exe" ;; esac
# A path the Windows python can open: Git Bash hands out /tmp/...
hostpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else printf '%s' "$1"; fi; }

TMP="$(mktemp -d)"
SERVER_PID=""
cleanup() {
  [ -z "$SERVER_PID" ] || kill "$SERVER_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

mkdir -p "$TMP/srv/$TAG"
cp "$DIST"/tgfake_*.tar.gz "$DIST"/tgfake_*.zip "$DIST/checksums.txt" "$TMP/srv/$TAG/"

PORT="$("$PY" -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
"$PY" -m http.server "$PORT" --bind 127.0.0.1 --directory "$(hostpath "$TMP/srv")" >"$TMP/http.log" 2>&1 &
SERVER_PID=$!
i=0
until curl -sf -o /dev/null "http://127.0.0.1:$PORT/$TAG/checksums.txt"; do
  i=$((i + 1))
  [ "$i" -lt 50 ] || { echo "the mirror did not come up" >&2; cat "$TMP/http.log" >&2; exit 1; }
  sleep 0.1
done
BASE="http://127.0.0.1:$PORT"

path="$(TGFAKE_DOWNLOAD_BASE="$BASE" sh "$ROOT/scripts/install.sh" -b "$TMP/bin" "$TAG" | tail -n 1)"
got="$("$path" --version | tr -d '\r')"
[ "$got" = "tgfake $TAG" ] || { echo "installed binary reports '$got', want 'tgfake $TAG'" >&2; exit 1; }
echo "ok   install.sh installed $got into $path"

# Installing again over a binary that is running replaces it rather than
# writing into it ("Text file busy"). Windows locks a running .exe against
# any replacement, so the case is for the other systems.
if [ -z "$EXE" ]; then
  "$path" --addr 127.0.0.1:0 >/dev/null 2>&1 &
  RUNNING=$!
  sleep 0.3
  if ! TGFAKE_DOWNLOAD_BASE="$BASE" sh "$ROOT/scripts/install.sh" -b "$TMP/bin" "$TAG" >/dev/null 2>"$TMP/again.log"; then
    kill "$RUNNING" 2>/dev/null || true
    echo "install.sh could not replace a running binary:" >&2
    cat "$TMP/again.log" >&2
    exit 1
  fi
  kill "$RUNNING" 2>/dev/null || true
  [ "$("$path" --version)" = "tgfake $TAG" ] || { echo "the replaced binary does not run" >&2; exit 1; }
  echo "ok   install.sh replaces a running binary"
fi

# A checksum that does not match stops the install before anything lands.
awk '{ c = substr($1, 1, 1); print (c == "0" ? "1" : "0") substr($1, 2) "  " $2 }' "$TMP/srv/$TAG/checksums.txt" >"$TMP/srv/$TAG/checksums.tmp"
mv "$TMP/srv/$TAG/checksums.tmp" "$TMP/srv/$TAG/checksums.txt"
if TGFAKE_DOWNLOAD_BASE="$BASE" sh "$ROOT/scripts/install.sh" -b "$TMP/bad" "$TAG" >/dev/null 2>"$TMP/bad.log"; then
  echo "install.sh accepted a tampered checksum" >&2
  exit 1
fi
grep -q 'checksum mismatch' "$TMP/bad.log" || { echo "install.sh failed for another reason:" >&2; cat "$TMP/bad.log" >&2; exit 1; }
[ ! -e "$TMP/bad/tgfake$EXE" ] || { echo "install.sh left a binary behind after a checksum mismatch" >&2; exit 1; }
echo "ok   install.sh refuses a tampered checksum"

# latest needs GitHub, which a mirror is not.
if TGFAKE_DOWNLOAD_BASE="$BASE" sh "$ROOT/scripts/install.sh" -b "$TMP/latest" >/dev/null 2>&1; then
  echo "install.sh resolved latest against a mirror" >&2
  exit 1
fi
echo "ok   install.sh asks for a version when given a mirror"
