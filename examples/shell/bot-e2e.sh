#!/bin/sh
# An integration test of a bot written in any language: the tgfake binary is
# the Telegram, the bot is a separate process pointed at it, and the test
# talks to the stand's simulation API with curl - nothing here knows how the
# bot is built. Swap BOT_CMD for your own bot and the checks for your own.
#
#   examples/shell/bot-e2e.sh
#   TGFAKE_BIN=./bin/tgfake BOT_CMD="python3 bot.py" examples/shell/bot-e2e.sh
#
# Environment:
#   TGFAKE_BIN  the tgfake binary (default: tgfake from PATH, else go run ./cmd/tgfake)
#   BOT_CMD     how to start the bot (default: the Go example in examples/echobot);
#               it is given TELEGRAM_API (the stand's origin) and BOT_TOKEN
#   PORT        the stand's port (default 18791)
#   KEEP=1      leave the stand and the bot running at the end
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PORT="${PORT:-18791}"
ORIGIN="http://127.0.0.1:$PORT"
TMP="$(mktemp -d)"
TGFAKE_PID=""
BOT_PID=""

cleanup() {
  if [ "${KEEP:-}" = 1 ]; then
    echo "kept: chat page $ORIGIN/ (tgfake pid $TGFAKE_PID, bot pid $BOT_PID, logs in $TMP)"
    return
  fi
  [ -z "$BOT_PID" ] || kill "$BOT_PID" 2>/dev/null || true
  [ -z "$TGFAKE_PID" ] || kill "$TGFAKE_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  echo "--- chat" >&2
  curl -s "$ORIGIN/sim/chat/4242?format=text" >&2 || true
  echo "--- bot log" >&2
  cat "$TMP/bot.log" >&2 || true
  exit 1
}

# wait_for CMD... retries a check for up to 10 s.
wait_for() {
  i=0
  until "$@" >/dev/null 2>&1; do
    i=$((i + 1))
    [ "$i" -lt 100 ] || return 1
    sleep 0.1
  done
}

chat_has() { curl -sf "$ORIGIN/sim/chat/4242?format=text" | grep -qF "$1"; }
sent_at_least() { [ "$(curl -sf "$ORIGIN/sim/outbox/count?method=$1" | tr -dc '0-9')" -ge "$2" ]; }
say() { curl -sf -X POST "$ORIGIN/sim/message" -H 'Content-Type: application/json' -d "{\"text\": \"$1\"}" >/dev/null; }
tap() { curl -sf -X POST "$ORIGIN/sim/callback" -H 'Content-Type: application/json' -d "{\"label\": \"$1\"}" >/dev/null; }

# 1. The stand.
if [ -n "${TGFAKE_BIN:-}" ]; then
  set -- "$TGFAKE_BIN"
elif command -v tgfake >/dev/null 2>&1; then
  set -- tgfake
else
  (cd "$ROOT" && go build -o "$TMP/tgfake" ./cmd/tgfake)
  set -- "$TMP/tgfake"
fi
"$@" --addr "127.0.0.1:$PORT" --poll-max 1s >"$TMP/tgfake.log" 2>&1 &
TGFAKE_PID=$!
wait_for curl -sf "$ORIGIN/sim/state" || fail "tgfake did not come up on $ORIGIN"

# 2. The bot, pointed at the stand.
if [ -z "${BOT_CMD:-}" ]; then
  (cd "$ROOT" && go build -o "$TMP/echobot" ./examples/echobot)
  BOT_CMD="$TMP/echobot"
fi
TELEGRAM_API="$ORIGIN" BOT_TOKEN="123456:fake" sh -c "exec $BOT_CMD" >"$TMP/bot.log" 2>&1 &
BOT_PID=$!
wait_for sent_at_least getUpdates 1 || fail "the bot never polled"

# 3. The checks: what a person does, what the chat shows.
say "hello"
wait_for chat_has "bot: You said: hello" || fail "no echo of hello"
echo "ok   the bot echoes a message"

say "/start"
wait_for chat_has "[Ping]" || fail "no keyboard after /start"
tap "Ping"
wait_for chat_has "bot: Pong." || fail "the tap did not edit the message"
sent_at_least answerCallbackQuery 1 || fail "the tap was not answered"
echo "ok   a tap on Ping edits the greeting"

curl -sf -X POST "$ORIGIN/sim/fault" -H 'Content-Type: application/json' \
  -d '{"method": "sendMessage", "code": 429, "retry_after": 1, "times": 1}' >/dev/null
say "under flood control"
sleep 1
chat_has "You said: under flood control" && fail "a refused sendMessage still reached the chat"
echo "ok   a flood-control fault is what the bot sees"

echo "PASS"
