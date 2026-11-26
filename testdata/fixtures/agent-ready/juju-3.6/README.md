# Juju 3.6 regression fixture (placeholder)

The requirements name the Juju 3.6 agent-readiness findings as a regression
fixture for `agent-ready check`. Those findings are not in this repository yet.

To complete the fixture, add here:

- `afdocs.json`: the afdocs JSON report from that run (`--format json --score`);
- `discovered.txt`: the page URLs that were discovered (one per line), if kept;
- `expected.yaml`: what the report must say, for example the label
  (indicative or audit), the blocking issue if the score was capped, and the
  checks that failed.

`internal/readiness` runs every folder in `testdata/fixtures/agent-ready/`
that has an `afdocs.json` and an `expected.yaml`. Until then, the synthetic
`capped.json` fixture covers the same rules: deterministic sampling, the
indicative label on small samples, and never headlining a capped score.
