# Development

This page is for contributors: how the repository is laid out, the `make` targets, how the tests are organised, what CI runs on a pull request and how a release is cut. Using the stand is covered by the other pages, starting with [command.md](command.md).

## Requirements

- Go 1.22 or newer. `go.mod` says `go 1.22.0`, and CI tests that toolchain next to the current stable one, so a bot pinned to Go 1.22 can import the packages.
- [golangci-lint](https://golangci-lint.run) v2 for `make lint`; CI runs v2.12.2.
- [GoReleaser](https://goreleaser.com) v2 for `make snapshot` and `make release-check`.
- `python3` and `curl` for `make install-test`, which serves the snapshot archives with `python3 -m http.server`.

The packages and the command import nothing outside the standard library. The one module requirement, `github.com/cucumber/godog`, is used by tests only.

## Layout

| Path | What it holds |
|------|---------------|
| [`cmd/tgfake`](../cmd/tgfake) | The command: flags, the banner, `run(ctx, args, stdout, stderr)`, which tests drive in-process on port 0, and the version. |
| [`pkg/server`](../pkg/server) | The server: the Bot API (`methods.go`), the update queue and long polling (`updates.go`), chats and their views (`chat.go`), faults (`faults.go`), menu buttons and Mini App launches (`webapp.go`), the simulation API (`sim.go`), the response envelope (`response.go`) and the `Server` itself (`server.go`). |
| [`pkg/botapi`](../pkg/botapi) | The wire types of the Bot API objects. |
| [`pkg/webapp`](../pkg/webapp) | Mini App launch data without state: signing, validation, theme parameters, the address rule. |
| [`pkg/llmstub`](../pkg/llmstub) | The scripted OpenAI-compatible model. |
| [`internal/chatpage`](../internal/chatpage) | The chat page: one self-contained HTML file embedded into the binary, which reads everything from the simulation API. |
| [`features/`](../features) | Gherkin specs of the happy paths, run by godog harnesses in the packages. |
| [`examples/echobot`](../examples/echobot) | A standard-library bot and its in-process tests. |
| [`examples/shell/bot-e2e.sh`](../examples/shell/bot-e2e.sh) | An integration test for a bot in any language: the binary, a bot process and curl. |
| [`scripts/install.sh`](../scripts/install.sh) | The release installer ([ci.md](ci.md#the-install-script)). |
| [`scripts/test-install.sh`](../scripts/test-install.sh) | Checks the installer against a snapshot build served from a local mirror, including a tampered checksum. |
| [`action.yml`](../action.yml) | The composite GitHub Action ([ci.md](ci.md#github-actions)). |
| [`.goreleaser.yaml`](../.goreleaser.yaml) | The release build: archives per platform and `checksums.txt`. |
| [`.github/workflows/ci.yaml`](../.github/workflows/ci.yaml) | Tests, lint and the release path on every pull request and push to `main`. |
| [`.github/workflows/release.yaml`](../.github/workflows/release.yaml) | Publishes a release when a `vX.Y.Z` tag is pushed. |
| [`docs/`](.) | This reference. [`README.md`](../README.md) is the landing page and links every page. |

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

Edge cases and refusals are ordinary unit tests next to the code: `pkg/server/*_test.go` for the Bot API, the update queue, media, drafts and the simulation API, `pkg/llmstub/llmstub_test.go`, `pkg/webapp/webapp_test.go` (including a known signing vector), `internal/chatpage/page_test.go`, which holds the page to having no outside asset, and `cmd/tgfake/main_test.go`. `go test ./...` also runs the tests of `examples/echobot`, so the example stays a working bot.

A change starts with a failing scenario for a new behaviour, or a failing unit test for an edge case or a bug, and carries its documentation: a new Bot API method is a row of [bot-api.md](bot-api.md), a new route a section of [sim-api.md](sim-api.md), a new flag a row of [command.md](command.md).

To run the any-language integration test against a build of the working tree:

```bash
examples/shell/bot-e2e.sh
```

Without `TGFAKE_BIN` and with no `tgfake` on the `PATH` it builds the command from the checkout, and without `BOT_CMD` it builds and runs the example bot.

## CI

[`ci.yaml`](../.github/workflows/ci.yaml) runs on every pull request, on every push to `main` and on demand. Its jobs run side by side:

| Job | What it checks |
|-----|----------------|
| Test | `go vet` and the tests on Ubuntu, macOS and Windows with the stable Go, and on Ubuntu with Go 1.22. On Linux the tests run under the race detector. |
| Lint | golangci-lint v2.12.2, on the Go 1.26 toolchain it is built with, and `go mod tidy -diff`, which fails when `go.mod` or `go.sum` is not tidy. |
| Release snapshot | The release without publishing it: GoReleaser builds every archive as a snapshot and keeps them as a workflow artifact. |
| Install | On Ubuntu, macOS and Windows: `scripts/test-install.sh` installs the host's archive from a local mirror of the snapshot (and, outside Windows, again over a running binary), then the action in `action.yml` installs the snapshot from that mirror and starts it with `--llm`, a bot's first calls (`getMe`, a message through `/sim/message`, `getUpdates`, `/v1/models`) are made against it and the installed version is compared with the snapshot's; a second start on port 0 must report its own address. On Linux, `examples/shell/bot-e2e.sh` runs the example bot against the installed binary. |
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

To check a release before tagging it, run `make release-check`, `make snapshot` and `make install-test` locally.

### Tags are never moved

A tag is also the Go module version: `go install github.com/EvilFreelancer/tgfake/cmd/tgfake@vX.Y.Z` and a `require` line in another module's `go.mod` resolve it, and the Go module proxy and checksum database keep what they first saw under it. A tag that is moved or deleted and pushed again no longer matches that record, and builds that depend on it fail checksum verification. Release tags are therefore never moved or deleted: a mistake in a release is fixed by the next patch version.
