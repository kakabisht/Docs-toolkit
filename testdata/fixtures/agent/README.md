# Agent fixture

A synthetic product, `lumen`, for `docsbot agent`. Nothing here describes a
real product.

- `repo/` is the product's repository: code in `cmd/`, configuration in
  `config/`, docs in `docs/`. The docs miss the `--keep-days` flag, the
  `LUMEN_LOG_LEVEL` variable, two API endpoints and the `server.port`
  setting. With `--since`, the tests also remove `--force-upgrade` from the
  code and change the default of `retries`, which the docs still describe.
- `product-kb/` is the product knowledge base the agent reads over MCP
  (`docsbot mcp serve --no-repo --kb product-kb`): release notes and API notes
  written by the product team.

Try it from the repository root:

    ./docsbot agent gaps testdata/fixtures/agent/repo
    ./docsbot agent run testdata/fixtures/agent/repo -c testdata/fixtures/agent/docsbot.yml --out /tmp/agent-demo

The tests copy `repo/` into a git repository to exercise `--since`.
