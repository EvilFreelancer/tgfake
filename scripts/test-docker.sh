#!/bin/sh
# Build the image before running this test. Compose starts both containers.
set -eu
: "${TGFAKE_IMAGE:?Set TGFAKE_IMAGE to the image under test}"
export TGFAKE_IMAGE
TGFAKE_PORT=${TGFAKE_PORT:-18792}
export TGFAKE_PORT
COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-tgfake-docker-test}
export COMPOSE_PROJECT_NAME
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose() { docker compose -f "$root/examples/docker/compose.yml" "$@"; }
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then compose logs >&2; fi
  compose down --volumes > /dev/null
}
trap cleanup EXIT
compose up -d --wait
origin="http://127.0.0.1:$TGFAKE_PORT"
ready=false
for _ in $(seq 1 100); do
  if curl -sf "$origin/bot123456:TEST/getWebhookInfo" | grep -q 'http://bot:8080/webhook'; then ready=true; break; fi
  sleep 0.1
done
[ "$ready" = true ] || { echo "webhook was not registered" >&2; exit 1; }
curl -sf -X POST "$origin/sim/message" -H 'Content-Type: application/json' -d '{"text":"hello from Compose"}' > /dev/null
for _ in $(seq 1 100); do
  if curl -sf "$origin/sim/chat/4242?format=text" | grep -q 'bot: You said: hello from Compose'; then
    echo "Container webhook delivery and Bot API reply passed"
    exit 0
  fi
  sleep 0.1
done
echo "bot did not answer the webhook" >&2
exit 1
