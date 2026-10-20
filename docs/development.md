# Development

This page is for contributors: how the repository is laid out, the `make` targets, how the tests are organised, what CI runs on a pull request and how a release is cut. Using the stand is covered by the other pages, starting with [command.md](command.md).

## Requirements

- Go 1.22 or newer. `go.mod` says `go 1.22.0`, and CI tests that toolchain next to the current stable one, so a bot pinned to Go 1.22 can import the packages.
- [golangci-lint](https://golangci-lint.run) v2 for `make lint`; CI runs v2.12.2.
- [GoReleaser](https://goreleaser.com) v2 for `make snapshot` and `make release-check`.
- `python3` and `curl` for `make install-test`, which serves the snapshot archives with `python3 -m http.server`.
- A local Linux Docker engine and `docker pull node:24-bookworm-slim` for serverless integration tests. `TGFAKE_TEST_DOCKER=1` requires these tests instead of skipping them when Docker or the image is unavailable. Linux CI enables it.
- Docker with the Compose plugin for the container image and [`scripts/test-docker.sh`](../scripts/test-docker.sh), which also pulls `node:24-alpine` for the example bot.

The packages and the command import nothing outside the standard library. The one module requirement, `github.com/cucumber/godog`, is used by tests only.

## Layout

| Path | What it holds |
|------|---------------|
| [`cmd/tgfake`](../cmd/tgfake) | The command: flags, the banner, `run(ctx, args, stdout, stderr)`, which tests drive in-process on port 0, and the version. |
| [`pkg/server`](../pkg/server) | The server: the Bot API (`methods.go`), the update queue and long polling (`updates.go`), chats and their views (`chat.go`), faults (`faults.go`), menu buttons and Mini App launches (`webapp.go`), the simulation API (`sim.go`), the response envelope (`response.go`) and the `Server` itself (`server.go`). |
| [`pkg/botapi`](../pkg/botapi) | The wire types of the Bot API objects. |
| [`pkg/webapp`](../pkg/webapp) | Mini App launch data without state: signing, validation, theme parameters, the address rule. |
| [`pkg/llmstub`](../pkg/llmstub) | The scripted OpenAI-compatible model. |
| [`pkg/serverless`](../pkg/serverless) | Local tgcloud module execution in Docker, the embedded JavaScript SDK, SQLite, update polling and authenticated endpoints. |
| [`internal/chatpage`](../internal/chatpage) | The chat page: one self-contained HTML file embedded into the binary, which reads everything from the simulation API. |
| [`features/`](../features) | Gherkin specs of the happy paths, run by godog harnesses in the packages. |
| [`examples/echobot`](../examples/echobot) | A standard-library bot and its in-process tests. |
| [`examples/serverless`](../examples/serverless) | A tgcloud counter project with a handler, schema and Mini App endpoint. |
| [`examples/shell/bot-e2e.sh`](../examples/shell/bot-e2e.sh) | An integration test for a bot in any language: the binary, a bot process and curl. |
| [`examples/docker`](../examples/docker) | A Docker Compose example: the image and a Node.js webhook echo bot in separate containers ([docker.md](docker.md)). |
| [`Dockerfile`](../Dockerfile) | The container image: a static build of the command on Alpine with CA certificates, run as UID 65532 with `--addr 0.0.0.0:18790` in its entrypoint. `.dockerignore` keeps the agent folders, build output and env files out of the build context. |
| [`scripts/install.sh`](../scripts/install.sh) | The release installer ([ci.md](ci.md#the-install-script)). |
| [`scripts/test-install.sh`](../scripts/test-install.sh) | Checks the installer against a snapshot build served from a local mirror, including a tampered checksum. |
| [`scripts/test-docker.sh`](../scripts/test-docker.sh) | Runs the Compose example against an image and checks a webhook and the bot's replies across the Compose network. |
| [`action.yml`](../action.yml) | The composite GitHub Action ([ci.md](ci.md#github-actions)). |
| [`.goreleaser.yaml`](../.goreleaser.yaml) | The release build: archives per platform and `checksums.txt`. |
| [`.github/workflows/ci.yaml`](../.github/workflows/ci.yaml) | Tests, lint and the release path on every pull request and push to `main`. |
| [`.github/workflows/release.yaml`](../.github/workflows/release.yaml) | Publishes a release when a `vX.Y.Z` tag is pushed. |
| [`docs/`](.) | This reference. [`README.md`](../README.md) is the landing page and links every page. |

Inside `pkg/server`, `media.go` owns media sends/edits and authenticated downloads, `incoming_media.go` incoming files, `relay.go` copies/forwards and static contact/location sends, `inline.go` inline and Mini App messages, `information.go` configurable chat/member/profile information, and `webhook.go` the delivery worker. Network requests use `Options.HTTPClient` and are cancelled on `Close`.

The release asset names are spelled the same way in `.goreleaser.yaml`, `scripts/install.sh`, `action.yml` and [ci.md](ci.md): renaming them in one place means renaming them in all of them.

## Make targets

| Target | What it does |
|--------|--------------|
| `make build` | Builds `build/tgfake` for the host the way a release links it (`-trimpath`, `-s -w`), with `VERSION` linked in as `main.version`. `VERSION` defaults to `git describe --tags --always --dirty`. |
| `make run` | Runs the stand with its scripted model on `127.0.0.1:18790` (`go run ./cmd/tgfake --llm`). |
| `make test` | `go test ./...`. |
| `make test-race` | `go test -race -count=1 ./...`. |
| `make cover` | Writes `coverage.out` and prints the total coverage. |
| `make lint` | `golangci-lint run ./...`. |
| `make check` | `lint` and `test-race`: what CI's test and lint jobs run. |
| `make snapshot` | `goreleaser release --snapshot --clean`: every release archive and `checksums.txt` into `dist/`, nothing published. |
| `make release-check` | `goreleaser check` on `.goreleaser.yaml`. |
| `make install-test` | `scripts/test-install.sh dist`: installs the host's archive from `dist/` through a local mirror and checks the version it reports, that a tampered checksum stops the install and that `latest` is refused with a mirror. Run `make snapshot` first. |
| `make clean` | Removes `build/`, `dist/` and `coverage.out`. |

`GORELEASER` names the GoReleaser binary, and `VERSION` can be set on the command line, for example `make build VERSION=v1.0.0`.

## Tests

The happy path of a feature is a Gherkin scenario in `features/`, and a godog harness in the package that owns the behaviour runs it as an ordinary Go test, in strict mode, so a step without a definition fails:

| Spec | Harness |
|------|---------|
| [`features/command.feature`](../features/command.feature) | [`cmd/tgfake/bdd_command_test.go`](../cmd/tgfake/bdd_command_test.go), against `run` on a free port. |
| [`features/update_lifecycle.feature`](../features/update_lifecycle.feature) | [`pkg/server/bdd_lifecycle_test.go`](../pkg/server/bdd_lifecycle_test.go), with the server's clock under the test's control. |
| [`features/mini_app.feature`](../features/mini_app.feature) | [`pkg/server/bdd_mini_app_test.go`](../pkg/server/bdd_mini_app_test.go). |
| [`features/bot_capabilities.feature`](../features/bot_capabilities.feature) | [`pkg/server/bdd_capabilities_test.go`](../pkg/server/bdd_capabilities_test.go), covering file downloads, inline selection, prepared sharing, webhooks and membership information. |
| [`features/serverless.feature`](../features/serverless.feature) | [`pkg/serverless/bdd_serverless_test.go`](../pkg/serverless/bdd_serverless_test.go), against the Docker runtime and counter example. |

Edge cases and refusals are ordinary unit tests next to the code: `pkg/server/*_test.go` for the Bot API, the update queue, media, drafts and the simulation API, `pkg/llmstub/llmstub_test.go`, `pkg/webapp/webapp_test.go` (including a known signing vector), `internal/chatpage/page_test.go`, which holds the page to having no outside asset, and `cmd/tgfake/main_test.go`. `go test ./...` also runs the tests of `examples/echobot`, so the example stays a working bot.

A change starts with a failing scenario for a new behaviour, or a failing unit test for an edge case or a bug, and carries its documentation: a new Bot API method is a row of [bot-api.md](bot-api.md), a new route a section of [sim-api.md](sim-api.md), a new flag a row of [command.md](command.md).

To run the any-language integration test against a build of the working tree:

```bash
examples/shell/bot-e2e.sh
```

Without `TGFAKE_BIN` and with no `tgfake` on the `PATH` it builds the command from the checkout, and without `BOT_CMD` it builds and runs the example bot.

To run the container test against an image of the working tree:

```bash
docker build -t tgfake:local .
TGFAKE_IMAGE=tgfake:local sh scripts/test-docker.sh
```

## CI

[`ci.yaml`](../.github/workflows/ci.yaml) runs on every pull request, on every push to `main` and on demand. Its jobs run side by side:

| Job | What it checks |
|-----|----------------|
| Test | `go vet` and the tests on Ubuntu, macOS and Windows with the stable Go, and on Ubuntu with Go 1.22. On Linux the tests run under the race detector. |
| Lint | golangci-lint v2.12.2, on the Go 1.26 toolchain it is built with, and `go mod tidy -diff`, which fails when `go.mod` or `go.sum` is not tidy. |
| Release snapshot | The release without publishing it: GoReleaser builds every archive as a snapshot and keeps them as a workflow artifact. |
| Install | On Ubuntu, macOS and Windows: `scripts/test-install.sh` installs the host's archive from a local mirror of the snapshot (and, outside Windows, again over a running binary), then the action in `action.yml` installs the snapshot from that mirror and starts it with `--llm`, a bot's first calls (`getMe`, a message through `/sim/message`, `getUpdates`, `/v1/models`) are made against it and the installed version is compared with the snapshot's; a second start on port 0 must report its own address. On Linux, `examples/shell/bot-e2e.sh` runs the example bot against the installed binary. |
| Container webhook test | Builds the image from the `Dockerfile` without publishing it and runs `scripts/test-docker.sh`: the image and the example bot of `examples/docker` in separate Compose containers, a webhook over the Compose network with `--allow-http-webhook`, and the bot's replies through the Bot API. |
| CI | The gate. It runs after the others whatever their result and passes only when every one of them succeeded; it is the check a branch protection rule requires. |

## Cutting a release

A release is a tag `vX.Y.Z` on `main`. Make sure `main` is green, then tag the commit and push the tag:

```bash
git switch main
git pull --ff-only
git tag v1.0.1
git push origin v1.0.1
```

Pushing the tag starts [`release.yaml`](../.github/workflows/release.yaml):

1. **Publish the release.** The job refuses a tag whose commit is not on `main`, runs the tests under the race detector and runs `goreleaser release --clean`. GoReleaser checks that `go mod tidy` would change nothing, builds the binaries for Linux, macOS and Windows on amd64 and arm64 with the tag linked in, packs the archives and `checksums.txt`, and publishes them on a GitHub release with notes built from the commit messages (`docs:`, `test:` and `chore:` commits and merge commits are left out) and the install commands. A tag with a pre-release suffix, such as `v1.1.0-rc.1`, is published as a pre-release.
2. **Install the release.** On Ubuntu, macOS and Windows runners, the action in `action.yml` installs the release just published, starts it with `--llm`, and the job checks that the binary reports the tag and that `getMe` and `/v1/models` answer. This is what a bot's own workflow does next.
3. **Publish the container image.** Next to step 2, the image is built from the `Dockerfile` for Linux amd64 and arm64 with the tag linked in as the version, and pushed to `ghcr.io/evilfreelancer/tgfake` under the version without `v`, its major/minor and major version and `latest`; a pre-release gets its full version only. GitHub makes a new package of a personal account private, so after the first release that publishes the image, set the package's visibility to public in its settings once, or `docker pull` asks for a login.

To check a release before tagging it, run `make release-check`, `make snapshot` and `make install-test` locally.

### Tags are never moved

A tag is also the Go module version: `go install github.com/EvilFreelancer/tgfake/cmd/tgfake@vX.Y.Z` and a `require` line in another module's `go.mod` resolve it, and the Go module proxy and checksum database keep what they first saw under it. A tag that is moved or deleted and pushed again no longer matches that record, and builds that depend on it fail checksum verification. Release tags are therefore never moved or deleted: a mistake in a release is fixed by the next patch version.
