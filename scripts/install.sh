#!/bin/sh
# Install the tgfake command from a GitHub release.
#
#   curl -sSfL https://raw.githubusercontent.com/EvilFreelancer/tgfake/main/scripts/install.sh | sh -s -- [-b DIR] [VERSION]
#
# VERSION is a release tag such as v0.1.0, or "latest" (the default). The
# binary lands in DIR (default ./bin), its archive checked against the
# release's checksums.txt first, and the last line printed is its path.
#
# Environment:
#   TGFAKE_REPO           owner/name of the repository (default EvilFreelancer/tgfake)
#   TGFAKE_DOWNLOAD_BASE  where <tag>/<asset> is fetched from (default the
#                         repository's release downloads); a local mirror or a
#                         snapshot build is served this way, and needs VERSION
#   TGFAKE_OS, TGFAKE_ARCH  override the detected platform (linux, darwin,
#                         windows; amd64, arm64)
set -eu

REPO="${TGFAKE_REPO:-EvilFreelancer/tgfake}"
BIN_DIR="./bin"
VERSION="latest"

usage() {
  cat >&2 <<'EOF'
usage: install.sh [-b DIR] [VERSION]
  VERSION  a release tag such as v0.1.0, or latest (the default)
  -b DIR   where the binary goes (default ./bin)
EOF
  exit "${1:-2}"
}

log() { echo "tgfake install: $*" >&2; }
fail() { log "$*"; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    -b) [ $# -ge 2 ] || usage; BIN_DIR="$2"; shift 2 ;;
    -h|--help) usage 0 ;;
    -*) log "unknown option $1"; usage ;;
    *) VERSION="$1"; shift ;;
  esac
done

detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    MINGW*|MSYS*|CYGWIN*|Windows_NT) echo windows ;;
    *) fail "unsupported system $(uname -s); set TGFAKE_OS" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) fail "unsupported architecture $(uname -m); set TGFAKE_ARCH" ;;
  esac
}

# fetch URL FILE downloads URL into FILE with curl or wget.
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -sSfL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    fail "neither curl nor wget is installed"
  fi
}

# latest_tag asks GitHub where /releases/latest redirects, which costs no API
# rate limit.
latest_tag() {
  url="https://github.com/${REPO}/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    final="$(curl -sSfL -o /dev/null -w '%{url_effective}' "$url")" || fail "cannot reach $url"
  else
    final="$(wget -q -S -O /dev/null "$url" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r')"
  fi
  tag="${final##*/}"
  case "$tag" in
    v[0-9]*) echo "$tag" ;;
    *) fail "no release found at $url" ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  else
    fail "neither sha256sum nor shasum is installed"
  fi
}

OS="${TGFAKE_OS:-$(detect_os)}"
ARCH="${TGFAKE_ARCH:-$(detect_arch)}"

if [ "$VERSION" = "latest" ]; then
  [ -z "${TGFAKE_DOWNLOAD_BASE:-}" ] || fail "TGFAKE_DOWNLOAD_BASE needs an explicit VERSION"
  VERSION="$(latest_tag)"
fi
case "$VERSION" in
  v*) ;;
  *) VERSION="v$VERSION" ;;
esac

BASE="${TGFAKE_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download}"
EXT="tar.gz"
EXE=""
if [ "$OS" = "windows" ]; then
  EXT="zip"
  EXE=".exe"
fi
ASSET="tgfake_${VERSION#v}_${OS}_${ARCH}.${EXT}"

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t tgfake)"
trap 'rm -rf "$TMP"' EXIT INT TERM

log "downloading ${ASSET} (${VERSION})"
fetch "${BASE}/${VERSION}/${ASSET}" "$TMP/$ASSET" || fail "cannot download ${BASE}/${VERSION}/${ASSET}"
fetch "${BASE}/${VERSION}/checksums.txt" "$TMP/checksums.txt" || fail "cannot download the checksums of ${VERSION}"

want="$(awk -v f="$ASSET" '$2 == f || $2 == "*" f { print $1 }' "$TMP/checksums.txt")"
[ -n "$want" ] || fail "${ASSET} is not listed in checksums.txt"
got="$(sha256_of "$TMP/$ASSET")"
[ "$want" = "$got" ] || fail "checksum mismatch for ${ASSET}: want ${want}, got ${got}"

mkdir -p "$TMP/x"
if [ "$EXT" = "zip" ]; then
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "$TMP/$ASSET" -d "$TMP/x"
  else
    powershell.exe -NoProfile -Command "Expand-Archive -Path '$TMP/$ASSET' -DestinationPath '$TMP/x'" >/dev/null
  fi
else
  tar -xzf "$TMP/$ASSET" -C "$TMP/x"
fi
[ -f "$TMP/x/tgfake$EXE" ] || fail "the archive holds no tgfake$EXE"

mkdir -p "$BIN_DIR"
cp "$TMP/x/tgfake$EXE" "$BIN_DIR/tgfake$EXE"
chmod +x "$BIN_DIR/tgfake$EXE"
log "installed ${VERSION} into ${BIN_DIR}"
echo "${BIN_DIR}/tgfake${EXE}"
