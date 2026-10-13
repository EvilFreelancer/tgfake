# Docker and Docker Compose

The image runs the static tgfake binary as UID/GID 65532, includes CA certificates for outgoing HTTPS requests, and listens on `0.0.0.0:18790`. It needs no persistent volume: chats, files and updates live in memory. Stopping the container sends SIGTERM to tgfake and releases held requests.

## Local build and working example

From the repository root:

```bash
docker build --build-arg VERSION=dev -t tgfake:local .
TGFAKE_IMAGE=tgfake:local docker compose -f examples/docker/compose.yml up -d --wait
```

Open `http://127.0.0.1:18790/` and send a message. The included Node.js echo bot registers its webhook and replies. Stop both services with:

```bash
TGFAKE_IMAGE=tgfake:local docker compose -f examples/docker/compose.yml down
```

For a published release, set `TGFAKE_IMAGE=ghcr.io/evilfreelancer/tgfake:<version>` instead. Replace `<version>` with a release that includes Docker support; the image is published for Linux amd64 and arm64. A fork publishes under its own owner name. Published tags omit the leading `v`; pin a full version for repeatable tests.

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
command: ["--addr", "0.0.0.0:18790", "--allow-http-webhook", "--token", "123456:TEST"]
ports:
  - "127.0.0.1:18790:18790"
```

`command` replaces the image's default arguments, so retain `--addr` when adding flags such as `--llm`. Containers reach tgfake by its service name; only the browser uses the host port. The bot does not need a published port. The example healthcheck waits for `/sim/state` before starting the bot. Use distinct Compose project names and host ports when running multiple stands.

`--allow-http-webhook` is an explicit exception for test networks. It accepts non-loopback HTTP webhook URLs on arbitrary ports. It does not detect Docker networks or limit destinations to private addresses. By default, only localhost/loopback HTTP URLs are accepted. The option leaves HTTPS port restrictions, malformed URL checks, webhook secrets, retries and polling conflicts unchanged. In Go tests, set `server.Options.AllowHTTPWebhook` to `true` for the same behavior.

The image contains no Docker CLI and does not configure the optional `--serverless` mode, which launches additional Docker containers. No Docker socket is needed for ordinary bot testing.

## Verify the image

```bash
TGFAKE_IMAGE=tgfake:local sh scripts/test-docker.sh
```

This starts the example on host port 18792, verifies webhook registration, secret validation and a Bot API reply across the Compose network, then removes the containers. Override `TGFAKE_PORT` and `COMPOSE_PROJECT_NAME` if needed. The same test runs in GitHub Actions before merging.
