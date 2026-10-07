---
description: The exported API of pkg/, the command flags and the sim API are a versioned contract with known consumers
paths:
  - "pkg/**"
  - "cmd/**"
  - "go.mod"
---

# Public API

`pkg/` is imported by other modules and the command is started by other
projects' scripts and CI. Both are a contract versioned by the module's tags.

## What is public

- Every exported identifier of `pkg/server`, `pkg/botapi`, `pkg/webapp` and
  `pkg/llmstub`, including struct fields and their JSON names (the simulation API
  serialises `ChatView`, `MessageView`, `Call`, `Fault`, `IncomingMessage` and the
  rest as they are).
- The command's flags, their defaults, the banner's lines (scripts read the
  origin from it) and its exit codes.
- The simulation API routes, bodies and status codes.
- Release asset names (see the release rule).

`internal/` is for what nobody else may import (the chat page).

## Changing it

- Releases follow semantic versioning from v1.0.0 on. A patch release (vX.Y.Z+1)
  fixes behaviour, a minor release (vX.Y+1.0) only adds, and nothing in v1 breaks
  a caller: a breaking change is v2, which for a Go module means the module path
  `github.com/EvilFreelancer/tgfake/v2`, so it is a decision for the owner, not a
  side effect of a change. Mark one in its commit message (`feat!:` or a
  `BREAKING CHANGE:` footer) so the release notes carry it.
- Prefer adding over changing: a new option field with a zero value that keeps
  the old behaviour, a new flag, a new route.
- Removing or renaming an exported identifier, a flag, a JSON field or a route is
  breaking. So is changing a default the documentation names (`@tgfake_bot`,
  `tgfake`, `tgfake-demo`, port 18790, user 4242 `alice`).
- Keep the packages free of non-test dependencies and buildable with the Go
  version in `go.mod`; CI runs the oldest one.

## Known consumers

Coddy (`github.com/coddy-project/coddy-agent`) pins this module in its `go.mod`:
its Telegram gateway tests run `pkg/server` in-process, several tests use
`pkg/llmstub`, and its end-to-end scripts and CI start the release binary of the
same version (the action of this repository). A release that changes what Coddy
uses is followed by a version bump there.
