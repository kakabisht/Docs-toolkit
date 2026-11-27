# Contributing to docsbot

Thanks for helping. Issues and pull requests are both welcome.

## Before you start

- For a bug, open an issue with the command you ran, what you expected, and what
  happened. A small docs folder that reproduces it helps most.
- For a new feature or a change in behaviour, open an issue first, so we can
  agree on the approach before you write code. Design decisions and their
  reasons are recorded in [docs/decisions.md](docs/decisions.md).

## Working on the code

You need Go (the version in `go.mod`). Node 22 is only needed to run
`agent-ready check` for real.

```bash
make build   # ./docsbot
make test    # all tests; the network is faked
make lint    # gofmt and go vet
```

- Add or update a test for every change. Fixtures live in `testdata/` and use
  the placeholder product "lumen"; never add real customer or company content.
- Keep the principles in the README: no silent edits to anyone's docs, nothing
  published without a person, nothing sent off the machine unless configured,
  and every finding with a file and line.
- Write user-facing text (help, reports, docs) in plain British English.

## Pull requests

- One change per pull request, with a short description of what and why.
- CI runs `gofmt`, `go vet`, the tests with the race detector, and a docsbot
  check of its own fixture site. Please make sure `make test` and `make lint`
  pass locally first.

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
