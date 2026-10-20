#!/bin/sh
# The Compose example end to end: tgfake and the echo bot in separate
# containers, a webhook across the Compose network and the bot's replies
# through the Bot API. Build the image first and name it in TGFAKE_IMAGE.
set -eu
: "${TGFAKE_IMAGE:?Set TGFAKE_IMAGE to the image under test}"
export TGFAKE_IMAGE
TGFAKE_PORT=${TGFAKE_PORT:-18792}
export TGFAKE_PORT
COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-tgfake-docker-test}
export COMPOSE_PROJECT_NAME
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
compose() { docker compose -f "$root/examples/docker/compose.yml" "$@"; }
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then compose logs >&2; fi
  compose down --volumes > /dev/null
}
trap cleanup EXIT
compose up -d --wait
origin="http://127.0.0.1:$TGFAKE_PORT"

# wait_for URL TEXT polls URL for up to 10 s until its body contains TEXT.
wait_for() {
  for _ in $(seq 1 100); do
    if curl -sf "$1" | grep -qF "$2"; then return 0; fi
    sleep 0.1
  done
  return 1
}
send() { curl -sf -X POST "$origin/sim/$1" -H 'Content-Type: application/json' -d "$2" > /dev/null; }

wait_for "$origin/bot123456:TEST/getWebhookInfo" 'http://bot:8080/webhook' || { echo "webhook was not registered" >&2; exit 1; }
send message '{"text":"hello from Compose"}'
wait_for "$origin/sim/chat/4242?format=text" 'bot: You said: hello from Compose' || { echo "bot did not answer the webhook" >&2; exit 1; }
# A tap on the bot's reply is an update without a message. Delivery is one
# update at a time, so the bot has to confirm it for the next message to come.
send callback '{"chat_id":4242,"message_id":2,"data":"tap"}'
send message '{"text":"after a tap"}'
wait_for "$origin/sim/chat/4242?format=text" 'bot: You said: after a tap' || { echo "an update without a message held back the next one" >&2; exit 1; }
echo "Container webhook delivery and Bot API reply passed"
