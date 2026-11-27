# docsbot

> Open source under the [Apache License 2.0](LICENSE). The repository is
> `Docs-toolkit`; the command it installs is `docsbot`.

A command-line toolkit for documentation teams. One binary, written in Go, that reads a
local docs repository (Markdown, MDX, HTML, AsciiDoc for conversion) or a live docs site,
and helps with the jobs that otherwise take a script per project:

| Command | What it does |
| --- | --- |
| `check` | Links, anchors, images, navigation, headings, front matter, leftover TODOs and draft review, as findings with file, line and severity |
| `graph` | A knowledge graph of pages, sections and entities (commands, flags, config keys, terms), each edge with its file, line and how it was found |
| `audit` | Structure and findability scores with the method behind each, and a fix-first list |
| `agent-ready` | How well AI agents can use the docs (wrapping AFDocs), and the files that help: llms.txt, Markdown copies of pages |
| `convert` | AsciiDoc to Markdown, with a report of every construct that was approximated, dropped or needs review |
| `draft` | A best-practice draft from the docs and interview transcripts, with a citation for every statement and a claim-by-claim review |
| `agent` | A documentation agent: finds what the code defines that the docs miss, fetches product context over MCP, drafts the pages, validates and scores them, and waits for a person to approve |
| `mcp serve` | Docs and knowledge-base passages over the Model Context Protocol, for the agent or any MCP client |

It is a working tool, not a finished one. Scores are indicators from fixed rules, not
measurements of reader success, and the defaults are starting values
([D9](docs/decisions.md#d9-audit-thresholds-and-weights-starting-values)).

## Principles

- **No silent edits.** docsbot never changes your content. `convert`, `agent-ready
  generate` and `draft write` write to a separate folder and refuse to write into the
  source; whatever they could not carry over exactly is reported.
- **No publishing.** Nothing is pushed, deployed or posted. Generated files are yours to
  review and copy.
- **Nothing leaves your machine unless you configure it.** Only `draft` can use a language
  model, only when one is configured, and only a local one unless you also set
  `allow_remote`.
- **Evidence over opinion.** Findings, graph edges and draft claims carry the file and line
  they come from.

## Install

```bash
go install github.com/kakabisht/Docs-toolkit/cmd/docsbot@latest   # installs the docsbot command

# or from a clone
make build        # ./docsbot
make test
```

`agent-ready check` also needs Node 22 or later for `npx afdocs`. Nothing else is needed.

## Quick start

```bash
docsbot check docs                      # everything, as findings
docsbot check docs --only links --offline
docsbot graph build docs && docsbot graph stats
docsbot audit docs -o audit.md
docsbot agent-ready check docs --site https://docs.example.com
docsbot convert asciidoc/ --out markdown/ --check
docsbot draft write "Upgrading safely" --transcript calls/upgrade.vtt
```

`SOURCE` is a directory or a URL. Every command takes `-f json`; `check` also writes
`github` annotations, `sarif` and `junit`. Exit codes: `0` success, `1` findings over the
threshold, `2` bad input or a failure.

Configuration lives in `docsbot.yml`; [`docsbot.example.yml`](docsbot.example.yml) lists
every setting with its default.

## How docs are read

**Local repositories.** docsbot finds the content root (`content/`, `docs/`,
`src/content/docs` … or `--content-dir`), reads front matter, and works out each file's
published route. Links resolve the way readers follow them: relative to the file, to the
site root, pretty URLs, and full URLs to `base_url`. Navigation comes from `mkdocs.yml`,
`SUMMARY.md`, Docusaurus sidebars or the folder structure. Hugo, Docusaurus, MkDocs, Astro,
Mintlify and Jekyll are detected, along with their include syntax.

**Live sites.** docsbot reads `llms.txt`, the sitemaps listed in `robots.txt`, then follows
internal links up to `--max-pages`. The HTTP client is polite: 100 ms between requests to
a host, eight at a time, retries on `429` with `Retry-After`, and a six-hour disk cache.

## check

```text
$ docsbot check testdata/fixtures/graph --offline
testdata/fixtures/graph/docs/install.md
  21:49   error    Links to a page or file that does not exist: missing.md  [link-missing-file]
                   hint: Fix the path, or remove the link if the page was deleted.

testdata/fixtures/graph/docs/old-install.md
  1       warning  No page or navigation entry links to this page  [page-orphan]
                   hint: Link to it from a related page or add it to the navigation.
```

`docsbot rules` lists the 31 rules with their groups (links, images, structure, metadata,
drafts) and default severities. Change a severity with `--rule id=warning` or in the
config. `--changed-only --base origin/main` reports only on files changed in a pull
request. Front matter can be validated against a JSON Schema. `links` is a shortcut for
`check --only links,images`.

## graph

```bash
docsbot graph build docs                 # writes docsbot-graph.db (SQLite)
docsbot graph stats                      # orphans, dead ends, hubs, undefined terms, naming
docsbot graph explain "server.port"      # every page that defines, documents or mentions it
docsbot graph view -f html -o graph.html
docsbot graph export -f graphml -o graph.graphml   # also json, csv, jsonld, cypher
```

Nodes are pages, sections, includes, tags, owners, versions and the entities the docs talk
about: commands, flags, config keys, API endpoints, error messages and glossary terms. Every
edge records whether it is **explicit** (a link, an include, front matter), **extracted**
(found in the text by a rule) or **inferred** (similar pages, suspected duplicates), with
file, line and confidence. Entity extraction uses rules, not a model
([D6](docs/decisions.md#d6-entity-extraction-without-a-model)). Filters such as `--type`,
`--section`, `--root` and `--depth` keep views readable on large sites.

## audit

```text
$ docsbot audit testdata/fixtures/graph
**Score: 62/100** (structure 57, findability 71) across 8 pages.

## Fix first
1. `docs/configure.md:1` Missing required metadata: description _(metadata)_
2. `docs/configure.md:1` how-to page lacks numbered steps _(templates)_
...
```

Structure covers content types, template conformance, metadata, freshness, depth,
navigation size, page length, section length, headings and duplication. Findability covers
click paths from the entry page, search (page titles as a proxy, or your own tasks with
`--tasks`), a search query log (`--query-log`), discovery metadata and cross-links. Each
dimension states its method and weight in the report; the fix-first list ranks items by
impact, weight and page importance. `audit architecture` is deferred until there is a
rubric to apply ([D10](docs/decisions.md#d10-audit-architecture-is-deferred)).

## agent-ready

```bash
docsbot agent-ready check .                                   # the repository, through a local preview
docsbot agent-ready check . --site https://docs.example.com    # the published site
docsbot agent-ready generate docs --out build/agent-ready
```

`check` runs [AFDocs](https://afdocs.dev) on a deterministic sample of pages spread across
sections, adds docsbot's whole-site checks, and labels the result **audit** (every page) or
**indicative** (a sample). When AFDocs caps its score, the report leads with the blocking
issue and the score before the cap
([D12](docs/decisions.md#d12-agent-readiness-wrap-afdocs-sample-deterministically)).
Without `--site`, docsbot serves the repository on a local port and AFDocs checks that
preview. The preview serves only the `llms.txt` the repository ships, and checks that depend
on the host (HTML size, content negotiation, status codes, caching, authentication) are left
out of the score and listed ([D17](docs/decisions.md#d17-agent-ready-check-on-a-local-repository)).
`generate` writes `llms.txt` (split by section when large), `llms-full.txt`, a Markdown copy
of every page, a copy-as-Markdown script and discovery snippets, into a folder outside the
docs.

## convert

```bash
docsbot convert docs/modules --out markdown --target mkdocs --attr product=Lumen --check
docsbot convert page.adoc --out /tmp/md --dry-run
```

A native converter, with no Pandoc to install
([D14](docs/decisions.md#d14-convert-native-go-not-pandoc)). It handles attributes,
conditionals, includes (with tags, lines and level offsets), Antora resource ids and
component attributes, admonitions, tables (pipe tables, or HTML for merged cells), callouts,
description lists, cross references and images. `--target` picks the admonition and list
syntax for GitHub, MkDocs, Docusaurus, Hugo or plain CommonMark.

Every run writes `convert-report.md` and `convert-report.json`: each construct approximated,
dropped or needing review, with file and line. A content check lists source lines whose
words are missing from the output. Output is only rewritten when it changes, so converting
twice gives the same files. `--check` runs the link and image checks on the result.

## draft

```bash
docsbot draft write "Upgrading lumen safely"                 # from the repository in .
docsbot draft write "Upgrading lumen safely" --transcript call.vtt
docsbot draft verify drafts/deploying-lumen --claim C1,C2 --status verified --by "A Reviewer"
docsbot draft verify drafts/deploying-lumen --claim C5 --status needs-change --note "default is info"
docsbot draft status drafts/deploying-lumen
```

`draft write` fills a template (built in, or your own Markdown with `## ` sections) from the
repository: the pages under its content root are the sources, the knowledge graph links each
claim to the pages that define what it names, and the code, configuration, schemas and
reference tables are used to check its facts. Other folders (`--kb`) and WebVTT, SRT or text
transcripts (`--transcript`) can be added. It writes:

- `draft.md`, with `status: draft` and `review: required`, and a footnote citation on every
  statement. Statements without a valid citation are marked **[uncited]**.
- `claims.json`, the verification ledger: every statement, its citations, and the numbers,
  versions, flags, commands, parameters, fields and defaults in it, each marked as found or
  not found in the cited passage, and as confirmed by or **conflicting with** the repository
  (for example, a default in the docs that differs from the config schema).
- `review.md`, the reviewer's checklist, with facts missing from their sources listed first.

Without a model the draft is extractive: sentences come from the sources as written, for a
writer to rework. With a model (Ollama, an OpenAI-compatible server such as llama.cpp or
LM Studio, or Anthropic) the text is written from the same passages with a versioned prompt,
and the ledger checks the result the same way
([D15](docs/decisions.md#d15-drafts-cite-everything-check-facts-without-a-model)).

`draft verify` records decisions. Edits to `draft.md` are picked up: edited statements
become unverified again. `check` fails a page marked ready while its ledger has open claims
(rule `draft-unverified-claims`).

## agent

```bash
docsbot agent gaps                                   # 1. what the docs miss (exit 1 when there are gaps)
docsbot agent gaps --since origin/main               #    ... and what a branch changed or removed
docsbot agent run                                    # 1-5 for the top 3 groups, into drafts/agent
docsbot agent run --group G2 --provider anthropic --allow-remote --judge
docsbot draft verify drafts/agent/g2-... --claim C1,C2 --status verified --by "A Reviewer"
docsbot agent eval drafts/agent/g2-...               # 4-5 again after editing draft.md
docsbot agent approve drafts/agent/g2-... --by "A Reviewer" --publish
```

The agent runs five fixed steps on a local repository
([D18](docs/decisions.md#d18-the-documentation-agent-fixed-steps-human-approval)):

1. **Gaps.** It reads the code for command-line flags (Go `flag`/pflag/cobra, argparse,
   click, commander), environment variables, HTTP endpoints (net/http, gin, chi, FastAPI,
   Flask, Express) and configuration keys (YAML config files, `yaml:`/`toml:` struct tags,
   `.env.example`; never `.env`), and looks for each on the docs pages. With `--since REF`
   it also reads `git diff REF`: items whose default changed while a page still gives the old
   one are **outdated**, items removed from the code that a page still names are **stale**.
   Gaps are grouped into pages: a new reference page, or an update to the page that already
   documents the neighbouring items.
2. **Context.** For each group it asks the MCP servers in `agent.mcp` (stdio or Streamable
   HTTP) for passages about the items, and keeps those that name them. Each passage is cited
   with the server, tool and source the server gave.
3. **Draft.** `draft write` runs with three kinds of source: what the code defines (file
   and line), the context passages, and repository pages that name the item. For changed or
   removed items the existing docs are left out, because their text is what is out of date.
   Update groups also get `proposed-changes.md`: each line to fix, with a diff.
4. **Validate.** Example blocks: JSON, YAML and TOML must parse, and every `--flag` in a
   shell block must be defined in the code. Flags and endpoints named in the prose must exist
   in the code too, unless the draft reports them as removed. Relative links are resolved from
   the page's target location, external ones with `--external-links`. Citations and facts
   come from the ledger: invalid citations, and facts the repository contradicts.
5. **Evaluate.** A score out of 100 (citations 30, grounded facts 30, gap items covered 25,
   sections with sources 15) and a gate (default 80, `--min-score`). Contradicted facts,
   invalid citations, unknown flags or endpoints, invalid examples and broken links block it
   whatever the score. `--judge` also asks the model to score faithfulness, completeness,
   clarity and examples from 1 to 5 with a versioned prompt; any score under 3 blocks.
   The result is `eval.json` and `eval.md`.

Nothing reaches the docs before a person approves. `agent approve` is refused until the
evaluation is of the current `draft.md`, the gate has passed and every claim is verified with
`draft verify`. `--publish` writes a new page at its target without citations or review
comments; it never overwrites a page, so updates are applied by hand from
`proposed-changes.md`.

Product context comes from MCP servers in `docsbot.yml`:

```yaml
agent:
  mcp:
    - name: product-kb                       # stdio: docsbot serving a knowledge-base folder
      command: [docsbot, mcp, serve, --no-repo, --kb, ../product-kb]
    - name: api-catalog                      # Streamable HTTP
      url: https://mcp.example.com/mcp
      allow_remote: true                     # the queries name your flags and settings
      header_env: {Authorization: CATALOG_TOKEN}
      tool: search                           # default: the first tool with "search" in its name
```

`docsbot mcp serve` is a ready-made context server: `search_docs` (the same BM25 ranking
drafts use) and `get_passage`, over stdio or `--http 127.0.0.1:PORT`. It also works as
documentation search for Claude Code: `claude mcp add docs -- docsbot mcp serve ~/docs-repo`.

Try it on the fixture, a synthetic product with real gaps:

```bash
make build
./docsbot agent gaps testdata/fixtures/agent/repo
./docsbot agent run testdata/fixtures/agent/repo -c testdata/fixtures/agent/docsbot.yml --out /tmp/agent-demo
```

## GitHub Action

```yaml
# .github/workflows/docs.yml
on: pull_request
jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: kakabisht/Docs-toolkit@main
        with:
          command: check docs --changed-only --base origin/${{ github.base_ref }} -f github
```

The action installs docsbot and runs one command. With `-f github`, findings appear as
annotations on the pull request. See [`action.yml`](action.yml) for the inputs.

## Development

```bash
make test           # all tests; the network is faked
make lint
make serve-example  # serve a fixture as a live site on :8484
```

Fixtures under `testdata/` use a placeholder product, "lumen", and synthetic transcripts.
The convert fixture is checked against golden output for all five targets, and the
converter has been run on the public Asciidoctor documentation. Decisions and their reasons
are in [`docs/decisions.md`](docs/decisions.md), and an introductory post is drafted in
[`docs/blog/introducing-docsbot.md`](docs/blog/introducing-docsbot.md).

## Contributing and licence

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). docsbot is
released under the [Apache License 2.0](LICENSE).
