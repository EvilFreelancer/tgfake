# Docker and Docker Compose

The image runs the static tgfake binary as UID/GID 65532, includes CA certificates for outgoing HTTPS requests, and listens on `0.0.0.0:18790`. It needs no persistent volume: chats, files and updates live in memory. Stopping the container sends SIGTERM to tgfake and releases held requests.

The entrypoint is `tgfake --addr 0.0.0.0:18790`, and arguments after the image name are added to it, so any flag of [command.md](command.md) can follow. An `--addr` among them takes precedence, because the last one given wins; publish the port it names instead of 18790:

```bash
docker run --rm -p 127.0.0.1:18790:18790 tgfake:local --llm
```

## Local build and working example

From the repository root:

```bash
docker build --build-arg VERSION=dev -t tgfake:local .
TGFAKE_IMAGE=tgfake:local docker compose -f examples/docker/compose.yml up -d --wait
```

Open `http://127.0.0.1:18790/` and send a message. The included Node.js echo bot registers its webhook, answers text messages and confirms every other update with a 200, so an update it does not handle is not retried in front of the next one. Stop both services with:

```bash
TGFAKE_IMAGE=tgfake:local docker compose -f examples/docker/compose.yml down
```

For a published release, set `TGFAKE_IMAGE=ghcr.io/evilfreelancer/tgfake:1.3.1` instead, or the version of a later release; images are published for Linux amd64 and arm64 from v1.2.0 on. Published tags omit the leading `v`; pin a full version for repeatable tests.

## Connect your bot

The [example Compose file](../examples/docker/compose.yml) gives each service its own network namespace. Replace the `bot` service with your bot image and configure these values through the settings its library supports:

| Setting | Value |
|---------|-------|
| Bot API origin | `http://tgfake:18790` |
| Webhook URL registered with tgfake | `http://bot:8080/webhook` |
| Bot webhook listener | `0.0.0.0:8080` |
| Token | The same test token passed to tgfake's `--token` |
| Webhook secret | The same value registered with `setWebhook` and checked by the bot |

The environment variable names in the example belong to the example bot, not tgfake. For python-telegram-bot, set `.base_url("http://tgfake:18790/bot")` and `.base_file_url("http://tgfake:18790/file/bot")` on the application builder. Set the webhook listener and registered URL through `run_webhook`.

The tgfake service uses:

```yaml
command: ["--allow-http-webhook", "--token", "123456:TEST"]
ports:
  - "127.0.0.1:18790:18790"
```

`command` adds these flags to the `--addr 0.0.0.0:18790` of the image's entrypoint; add `--llm` the same way. Containers reach tgfake by its service name; only the browser uses the host port. The bot does not need a published port. The example healthcheck waits for `/sim/state` before starting the bot. Use distinct Compose project names and host ports when running multiple stands.

`--allow-http-webhook` is an explicit exception for test networks. It accepts non-loopback HTTP webhook URLs on arbitrary ports. It does not detect Docker networks or limit destinations to private addresses. By default, only localhost/loopback HTTP URLs are accepted. The option leaves HTTPS port restrictions, malformed URL checks, webhook secrets, retries and polling conflicts unchanged. In Go tests, set `server.Options.AllowHTTPWebhook` to `true` for the same behavior.

The image contains no Docker CLI and does not configure the optional `--serverless` mode, which launches additional Docker containers. No Docker socket is needed for ordinary bot testing.

## Verify the image

```bash
TGFAKE_IMAGE=tgfake:local sh scripts/test-docker.sh
```

This starts the example on host port 18792 and checks, across the Compose network, that the bot registers its webhook, that a message reaches it with the secret it expects and its answer comes back through the Bot API, and that a callback query, an update without a message, does not hold back the message after it. Then it removes the containers. Override `TGFAKE_PORT` and `COMPOSE_PROJECT_NAME` if needed. The `Container webhook test` job of CI runs the same test on every pull request.
