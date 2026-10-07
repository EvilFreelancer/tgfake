# Running tgfake in CI

This page covers running the stand in continuous integration: the GitHub Action in [`action.yml`](../action.yml), the install script [`scripts/install.sh`](../scripts/install.sh) for GitLab CI and every other system, `go install` as an alternative, and running the binary in a container. The checks a job runs against the stand are the ones of [sim-api.md](sim-api.md); [`examples/shell/bot-e2e.sh`](../examples/shell/bot-e2e.sh) is a complete script of that kind.

## Release assets

Every release `vX.Y.Z` on [GitHub](https://github.com/EvilFreelancer/tgfake/releases) carries one archive per platform, built by [GoReleaser](../.goreleaser.yaml), and a checksums file:

| Asset | Content |
|-------|---------|
| `tgfake_<X.Y.Z>_<os>_<arch>.tar.gz` | The static binary `tgfake`, `README.md` and `LICENSE`, for `os` `linux` or `darwin` and `arch` `amd64` or `arm64`. |
| `tgfake_<X.Y.Z>_windows_<arch>.zip` | The same with `tgfake.exe`, for `amd64` or `arm64`. |
| `checksums.txt` | The SHA-256 of every archive. |

The version in an asset name has no leading `v`: release `v0.1.0` carries `tgfake_0.1.0_linux_amd64.tar.gz`. The binaries are built without cgo, so they need no system library, and they report the tag with `--version` (`tgfake v0.1.0`).

## GitHub Actions

The repository is a composite action, `EvilFreelancer/tgfake`. It installs a release on Linux, macOS and Windows runners, puts it on the `PATH` and can start it in the background.

### Inputs

| Input | Default | Meaning |
|-------|---------|---------|
| `version` | `""` | The release to install: a tag such as `v0.1.0`, or `latest`. Empty means the tag the action itself was used at, so `uses: EvilFreelancer/tgfake@v0.1.0` installs v0.1.0; when that ref is not a release tag, `latest`. |
| `install-dir` | `""` | Where the binary goes. Empty means `$RUNNER_TEMP/tgfake`. The directory is added to `PATH` for the following steps. |
| `start` | `"false"` | `"true"` starts the server in the background and waits until it answers. |
| `addr` | `127.0.0.1:18790` | The address the started server listens on. Give a fixed port: the `url` output is built from this value. |
| `args` | `""` | More arguments for the started server, split on whitespace, for example `--llm --llm-script rules.json`. |
| `download-base` | `""` | Fetch `<tag>/<asset>` from this base instead of the GitHub release downloads: a mirror, or a snapshot build under test. It does not work with `latest`, so the version has to be a tag. |

### Outputs

| Output | Meaning |
|--------|---------|
| `path` | The path of the installed binary. |
| `version` | The version the installed binary reports, such as `v0.1.0`. |
| `url` | The origin of the started server, such as `http://127.0.0.1:18790`; empty unless `start` is `"true"`. |
| `log` | The file the started server writes its stdout and stderr to, `$RUNNER_TEMP/tgfake.log`; empty unless `start` is `"true"`. |

With `start: "true"` the action runs `tgfake --addr <addr> <args>` in the background and polls `GET /sim/state` every 0.2 s. If the server does not answer within 20 seconds, the step prints the log and fails. Once started, the server runs until the job ends.

### Pinning a version

Pin the action to a release tag and leave `version` out:

```yaml
- uses: EvilFreelancer/tgfake@v0.1.0
```

The action then installs the binary of that same release, and its install script is the one of that release too, so one tag in the workflow decides everything. The action takes the ref from `github.action_ref`, and when a runner leaves that empty, from the last element of the path the action was checked out to. A ref counts as a release when it is `v` followed by a digit and contains a dot, as `v0.1.0` and `v0.2.0-rc.1` do.

`version` is for overriding that choice. It matters when the action is pinned by something that does not name a release, such as a commit SHA, a branch or a tag like `v0`: the action would install `latest`, which changes under the workflow. Name the release explicitly then:

```yaml
- uses: EvilFreelancer/tgfake@<commit sha of v0.1.0>
  with:
    version: v0.1.0
```

### A complete job

The job below installs v0.1.0, starts it with the scripted model, starts the bot under test pointed at it, and runs the checks of `examples/shell/bot-e2e.sh`: a message is echoed, a tap on a button edits the message, and a flood-control fault is what the bot sees. The checks are written for the echo bot of [`examples/echobot`](../examples/echobot); replace `bot.py` and the checks with your own. `--poll-max 1s` matters: the first check waits for the bot's first `getUpdates` in the outbox, and a poll is recorded only when it is answered, which with the default would be 30 seconds later.

```yaml
name: Bot end-to-end

on:
  push:
  pull_request:

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Start tgfake
        id: tgfake
        uses: EvilFreelancer/tgfake@v0.1.0
        with:
          start: "true"
          args: --llm --llm-delay 0s --poll-max 1s

      - name: Start the bot
        env:
          TELEGRAM_API: ${{ steps.tgfake.outputs.url }}
          BOT_TOKEN: "123456:fake"
          OPENAI_BASE_URL: ${{ steps.tgfake.outputs.url }}/v1
          OPENAI_API_KEY: any
        run: nohup python3 bot.py > bot.log 2>&1 &

      - name: Talk to the bot
        env:
          O: ${{ steps.tgfake.outputs.url }}
        run: |
          set -eu
          wait_for() {
            for _ in $(seq 1 100); do "$@" > /dev/null 2>&1 && return 0; sleep 0.1; done
            return 1
          }
          chat_has() { curl -sf "$O/sim/chat/4242?format=text" | grep -qF "$1"; }
          sent_at_least() { [ "$(curl -sf "$O/sim/outbox/count?method=$1" | tr -dc '0-9')" -ge "$2" ]; }
          say() { curl -sf -X POST "$O/sim/message" -d "{\"text\": \"$1\"}" > /dev/null; }
          tap() { curl -sf -X POST "$O/sim/callback" -d "{\"label\": \"$1\"}" > /dev/null; }

          wait_for sent_at_least getUpdates 1 || { echo "the bot never polled" >&2; exit 1; }

          say "hello"
          wait_for chat_has "bot: You said: hello" || { echo "no echo of hello" >&2; exit 1; }

          say "/start"
          wait_for chat_has "[Ping]" || { echo "no keyboard after /start" >&2; exit 1; }
          tap "Ping"
          wait_for chat_has "bot: Pong." || { echo "the tap did not edit the message" >&2; exit 1; }
          sent_at_least answerCallbackQuery 1 || { echo "the tap was not answered" >&2; exit 1; }

          curl -sf -X POST "$O/sim/fault" \
            -d '{"method": "sendMessage", "code": 429, "retry_after": 1, "times": 1}' > /dev/null
          say "under flood control"
          sleep 1
          if chat_has "You said: under flood control"; then
            echo "a refused sendMessage still reached the chat" >&2
            exit 1
          fi

      - name: Show the chat and the logs
        if: failure()
        run: |
          curl -s "${{ steps.tgfake.outputs.url }}/sim/chat/4242?format=text"
          curl -s "${{ steps.tgfake.outputs.url }}/sim/outbox"
          cat "${{ steps.tgfake.outputs.log }}"
          cat bot.log
```

To run `bot-e2e.sh` itself, or a copy of it in your repository, install without starting and hand it the binary: the script starts the stand on its own port (`PORT`, 18791 by default) and starts the bot from `BOT_CMD`, passing it `TELEGRAM_API` and `BOT_TOKEN`.

```yaml
      - name: Install tgfake
        id: tgfake
        uses: EvilFreelancer/tgfake@v0.1.0

      - name: End-to-end test
        env:
          TGFAKE_BIN: ${{ steps.tgfake.outputs.path }}
          BOT_CMD: python3 bot.py
        run: sh tests/bot-e2e.sh
```

## The install script

`scripts/install.sh` installs a release on any machine with a POSIX shell, which makes it the way in for GitLab CI, Jenkins, Buildkite, a developer's laptop and anything else:

```bash
curl -sSfL https://raw.githubusercontent.com/EvilFreelancer/tgfake/v0.1.0/scripts/install.sh | sh -s -- -b ./bin v0.1.0
```

```text
usage: install.sh [-b DIR] [VERSION]
```

| Argument | Default | Meaning |
|----------|---------|---------|
| `VERSION` | `latest` | A release tag such as `v0.1.0`; a version without its `v` gets one. `latest` asks GitHub where `/releases/latest` redirects, which costs no API rate limit. |
| `-b DIR` | `./bin` | Where the binary goes; the directory is created. |
| `-h`, `--help` | | Print the usage and exit 0. An unknown option prints it and exits 2. |

| Environment variable | Default | Meaning |
|----------------------|---------|---------|
| `TGFAKE_REPO` | `EvilFreelancer/tgfake` | The repository whose releases are installed, as `owner/name`. |
| `TGFAKE_DOWNLOAD_BASE` | `https://github.com/<repo>/releases/download` | Where `<tag>/<asset>` and `<tag>/checksums.txt` are fetched from: a mirror, or a snapshot build served locally. It needs an explicit `VERSION`; `latest` is refused with it. |
| `TGFAKE_OS` | detected with `uname -s` | `linux`, `darwin` or `windows`. MINGW, MSYS and Cygwin shells count as `windows`. |
| `TGFAKE_ARCH` | detected with `uname -m` | `amd64` or `arm64`. |

The script downloads the archive of the platform and `checksums.txt`, and checks the archive's SHA-256 against it before unpacking anything: an archive missing from the list, or one whose checksum differs, stops the install with exit status 1 and nothing is installed. On Windows it unpacks the `.zip` with `unzip`, or with PowerShell's `Expand-Archive` when there is no `unzip`, and installs `tgfake.exe`. It needs `curl` or `wget`, `sha256sum` or `shasum`, and `tar` for the other platforms.

Progress goes to stderr, prefixed `tgfake install:`. The last line on stdout is the path of the installed binary, so a script can capture it:

```bash
bin="$(curl -sSfL https://raw.githubusercontent.com/EvilFreelancer/tgfake/v0.1.0/scripts/install.sh | sh -s -- -b "$HOME/.local/bin" v0.1.0 | tail -n 1)"
"$bin" --version
```

Fetching the script from the tag, as above, rather than from `main` keeps the installer as fixed as the version it installs.

### GitLab CI

A job that installs a pinned release, then runs a copy of `examples/shell/bot-e2e.sh` kept in the repository as `tests/bot-e2e.sh`, against the bot started from `BOT_CMD`:

```yaml
bot-e2e:
  image: python:3.12
  variables:
    TGFAKE_VERSION: v0.1.0
  before_script:
    - curl -sSfL -o install.sh "https://raw.githubusercontent.com/EvilFreelancer/tgfake/${TGFAKE_VERSION}/scripts/install.sh"
    - sh install.sh -b "$CI_PROJECT_DIR/bin" "$TGFAKE_VERSION"
    - export PATH="$CI_PROJECT_DIR/bin:$PATH"
    - tgfake --version
  script:
    - BOT_CMD="python3 bot.py" sh tests/bot-e2e.sh
```

The image needs the tools the install script uses; Debian-based images such as `python:3.12` have them. To start the stand yourself instead, run it in the background and wait for `GET /sim/state`:

```bash
tgfake --llm --llm-delay 0s --poll-max 1s > tgfake.log 2>&1 &
for _ in $(seq 1 100); do curl -sf -o /dev/null http://127.0.0.1:18790/sim/state && break; sleep 0.2; done
```

## go install

Where Go 1.22 or newer is at hand, the command can be built from the module instead of downloaded:

```bash
go install github.com/EvilFreelancer/tgfake/cmd/tgfake@v0.1.0
```

The binary lands in `$(go env GOPATH)/bin` (or `GOBIN`) and reports the module version it was built at. A Go bot does not need the binary at all: its tests can run the stand in-process ([go.md](go.md)).

## Containers

No container image is published yet. The release binaries are static, so one runs in any Linux container. A Dockerfile that installs a pinned release with the install script and serves it on every interface of the container:

```dockerfile
FROM alpine:3.20
ARG TGFAKE_VERSION=v0.1.0
RUN apk add --no-cache curl \
 && curl -sSfL "https://raw.githubusercontent.com/EvilFreelancer/tgfake/${TGFAKE_VERSION}/scripts/install.sh" \
    | sh -s -- -b /usr/local/bin "${TGFAKE_VERSION}" \
 && apk del curl
EXPOSE 18790
ENTRYPOINT ["tgfake", "--addr", "0.0.0.0:18790"]
```

Inside a container the stand has to listen on `0.0.0.0` (or `:18790`), because the default `127.0.0.1` is reachable from that container only. Other containers then reach it by the container's name, for example `http://tgfake:18790`, and extra flags such as `--llm` follow the image name in `docker run`. An image you build this way can also serve as a GitLab CI `services:` entry.
