# Agent notes for tgfake

**tgfake server** is a fake Telegram Bot API for testing bots offline: the Bot
API methods a bot calls, the person's side of the chat as a simulation API and
a chat page, Mini App launches, injected faults, and a scripted
OpenAI-compatible model for LLM bots. It ships as a static binary
(`cmd/tgfake`, released by GoReleaser) and as Go packages a test runs
in-process. `README.md` is the landing page; `docs/` is the reference.

## Repository map

| Path | Responsibility |
|------|----------------|
| `cmd/tgfake` | The command: flags, the banner, `run(ctx, args, stdout, stderr)` (testable in-process, port 0 picks a free port), `--version` from `-X main.version` or the module version. `bdd_command_test.go` runs `features/command.feature` against `run`. |
| `pkg/server` | The server: `Server` state under one mutex, the Bot API (`methods.go`: the method switch, parameter parsing, refusals), the update queue and long polling (`updates.go`), faults (`faults.go`), chats and their views (`chat.go`), menu buttons and Mini App launches (`webapp.go`), the simulation API under `/sim/` (`sim.go`), the response envelope (`response.go`). `Handler()` mounts the Bot API, `/sim/` and the chat page. |
| `pkg/botapi` | Wire types of the Bot API objects (`Update`, `Message`, `User`, `InlineKeyboardMarkup`, `MenuButton`, ...). Data only, no behaviour. |
| `pkg/webapp` | Mini App launch data with no state: `Sign`, `Validate`, `ThemeParams`, `URLProblem`, `AppendLaunchParams`, the claimed version and platform. |
| `pkg/llmstub` | The scripted model: `/v1/models` and `/v1/chat/completions`, rules, answers in turn, echo, tool-call rules, `StripTags`, streaming pace. |
| `internal/chatpage` | The chat page, one self-contained HTML file embedded into the binary; it reads everything from the simulation API. |
| `features/` | Gherkin specs of the happy paths, run by godog harnesses in the owning package (`pkg/server/bdd_*_test.go`, `cmd/tgfake/bdd_command_test.go`). |
| `examples/echobot`, `examples/shell` | A standard-library bot with its in-process tests, and an any-language integration test driving the binary and a bot process with curl. |
| `scripts/install.sh`, `action.yml` | The release installer (checksum-verified) and the composite GitHub Action built on it. `scripts/test-install.sh` checks the installer against a snapshot. |
| `.goreleaser.yaml`, `.github/workflows/` | Release archives and checksums; `ci.yaml` (test matrix, lint, release path, gate job `CI`) and `release.yaml` (publish on a `vX.Y.Z` tag, then install it on three systems). |

## Commands

```bash
make test           # go test ./...
make check          # golangci-lint and the race detector, what CI runs
make build          # build/tgfake with the version linked in
make run            # the stand with its scripted model on 127.0.0.1:18790
make snapshot       # every release archive into dist/, nothing published (needs goreleaser)
make install-test   # scripts/install.sh against the snapshot in dist/
examples/shell/bot-e2e.sh   # the binary and the example bot, driven through /sim/
```

Go 1.22 is the oldest toolchain the module admits (`go.mod`); CI tests it next
to the stable one.

## Invariants

- **Behave like api.telegram.org.** A method answers what Telegram answers and
  refuses what Telegram refuses, with Telegram's own error text and status. Being
  more lenient hides a bug the bot will hit in production; the one place the
  stand is stricter (a button with more than one action) is documented. Details:
  `.claude/rules/telegram-fidelity.md`.
- **No dependencies outside tests.** The packages and the command use the
  standard library only, so importing `pkg/server` costs a bot nothing.
  `godog` is a test dependency.
- **The exported API of `pkg/` and the command's flags are a public contract.**
  Other modules pin a version and call them (Coddy's gateway tests use
  `pkg/server` and `pkg/llmstub`, its scripts start the binary). See
  `.claude/rules/public-api.md`.
- **No knowledge of a particular bot.** Defaults name the stand itself
  (`@tgfake_bot`, `tgfake`, `tgfake-demo`); a behaviour one client needs is an
  option that client turns on (`llmstub.Server.StripTags` for Coddy's
  `<turn_context>`).
- **Everything the server does is safe for concurrent use** and `Close`
  releases every held `getUpdates` before a listener shuts down.
- **Release names are coupled.** `.goreleaser.yaml`, `scripts/install.sh`,
  `action.yml` and `docs/ci.md` all spell `tgfake_<version>_<os>_<arch>`; a tag
  `vX.Y.Z` is a Go module version and is never moved or deleted. See
  `.claude/rules/release.md`.

## Workflow

1. Happy path first: extend or add a `features/*.feature` scenario and its
   step definitions, or a failing unit test for an edge case; run it and see it
   fail for the right reason.
2. Implement the smallest change that makes it pass.
3. `make check` (lint and the race detector over everything).
4. Carry the change into `README.md` and `docs/` in the same commit: a new
   method is a row in `docs/bot-api.md`, a new route a section of
   `docs/sim-api.md`, a new flag a row of `docs/command.md`.
5. Commit messages are Conventional Commits (`feat:`, `fix:`, `docs:`, `build:`,
   `refactor:`, `test:`, `ci:`); the release notes are built from them.

All code comments and documentation are in English. Prose uses a spaced hyphen
" - " rather than an em dash, straight quotes and no emoji.

## Agent host integrations

`AGENTS.md` is the baseline and `CLAUDE.md` is its symlink. Claude Code reads
the scoped rules under `.claude/rules/`, Cursor the same rules under
`.cursor/rules/`; the two trees carry the same bodies and must be changed
together.
