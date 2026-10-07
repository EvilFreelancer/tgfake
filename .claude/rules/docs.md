---
description: README and docs/ carry every user-visible change, checked against the code
paths:
  - "README.md"
  - "docs/**"
  - "AGENTS.md"
---

# Documentation

`README.md` is the landing page; `docs/` is the reference, one page per
surface: `command.md`, `bot-api.md`, `sim-api.md`, `mini-apps.md`,
`scripted-model.md`, `go.md`, `ci.md`, `development.md`.

- A change that a user can see carries its documentation in the same commit: a
  method, a refusal, a route, a flag, an option field, an action input.
- Every statement is checked against the code; an example is run before it is
  written down.
- Images live in `docs/assets/` and every file there is referenced by a page.
- English prose, a spaced hyphen " - " instead of an em dash, straight quotes,
  no emoji, no horizontal rules. Code blocks name their language.
- Links between pages are relative; links to sources are relative to `docs/`.
