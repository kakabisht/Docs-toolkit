# docsbot

A single-binary CLI that audits documentation for AI agents. It reads a **live docs site**
(llms.txt, sitemap, crawl) or a **local docs repo** (Markdown, MDX, HTML) and gives you:

| Command | What it does |
|---|---|
| `docsbot audit` | Agent readiness score: [afdocs](https://afdocs.dev) checks plus whole-site graph checks, with prioritized fixes |
| `docsbot graph` | A knowledge graph of pages, sections and links in one SQLite file, with PageRank, stats and exports |
| `docsbot links` | Broken internal links, missing `#anchors`, redirects, missing images and dead external URLs, ranked by impact |

```bash
go install github.com/docsbot/docsbot@latest

docsbot links ./docs
docsbot audit https://docs.example.com
docsbot graph build ./docs && docsbot graph export --as html -o graph.html
```

`SOURCE` is always a URL (`https://…`) or a directory. Every command takes either.

## How it reads docs

**Live sites.** docsbot reads `llms.txt` (under the base path, then at the origin), the
sitemaps listed in `robots.txt` (or `/sitemap.xml`, including sitemap indexes), and then
follows internal links until `--max-pages`. Pages and their `.md` twins collapse into one
node. Redirects are followed and recorded, so a page reached through a redirect is keyed by
its final URL. Links inside `<main>`/`<article>` are content links. Links in headers,
sidebars and footers are kept as navigation links.

**Local repos.** docsbot finds the content root (`content/en`, `content`, `docs`, `src/content/docs`, …
or `--content-dir`), reads front matter (`title`, `description`, `aliases`, `slug`, `url`,
`draft`), and works out each file's published route (`guide/_index.md` → `/guide`).
Links resolve the way readers hit them: file-relative (`../api.md#auth`), site-absolute
(`/api/`), pretty-URL relative (`../api/`) and full URLs to `--base-url`. Section index
pages count as linking to the pages in their folder, standing in for the sidebar a
generator builds. Hugo, Docusaurus, MkDocs, Astro, Mintlify and Jekyll are detected.
Hugo's `include` shortcodes and `autoDefinitionTermID` anchors are understood.

The HTTP client is polite by default: a 100 ms gap between requests to one host, 8 concurrent
requests overall, one retry on `429` that honors `Retry-After`, and an on-disk cache
(6 hours) so re-runs don't hit external sites again. Use `--no-cache` to skip it.

## `docsbot links`

```
$ docsbot links testdata/site --no-external
Checked 8 link targets across 7 pages (internal 6, asset 2)

2 broken, 2 broken anchors, 0 unreachable, 0 blocked, 1 redirects

BROKEN
  asset /img/gone.png  impact 1.00
      file not found
      from guide/install.md:22
  internal /nope  impact 0.07
      no page at this path
      from index.md:9
...
```

| Problem | Meaning | Fails by default |
|---|---|---|
| `broken` | Page, file or URL does not exist (4xx/5xx) | yes |
| `broken-anchor` | Page exists but the `#section` doesn't | yes |
| `unreachable` | DNS failure, timeout or connection error | no |
| `blocked` | External server refused an automated client (401/403/999). Check it in a browser | no |
| `redirect` | Works, but through a redirect or alias. Link to the final URL | no |

Issues are grouped by target and sorted by **impact**: the summed PageRank of the pages
carrying the bad link, so a break on a heavily linked page sorts above one on a leaf page.
External URLs are checked with `HEAD` and fall back to `GET` when a server mishandles
`HEAD`. Trivial redirects (http→https, trailing slash, www) are ignored.

Flags: `--no-external`, `--redirects` (also report external redirects),
`--fail-on broken,broken-anchor,unreachable,blocked,redirect` (empty for never), `--limit`,
`--db docsbot.db` (reuse a built graph). Exit code `1` when a `--fail-on` problem is found.

## `docsbot graph`

```bash
docsbot graph build ./docs --db docsbot.db   # crawl and write SQLite
docsbot graph stats --db docsbot.db          # size, orphans, depth, top pages, external hosts
docsbot graph export --as html -o graph.html # interactive viewer, no external scripts
docsbot graph export --as mermaid --max-nodes 40
docsbot graph export --as dot | dot -Tsvg > graph.svg
docsbot graph export --as json --sections    # node-link JSON (d3, networkx)
docsbot graph export --as llms-txt -o llms.txt  # generate an llms.txt from the graph
```

Nodes are pages and sections. Edges are `contains` (page → section), `links_to` (content
links, down to the section the link sits in and the anchor it targets) and `nav`. Each page
gets PageRank (content links only, top page = 1), click depth from the entry page
(navigation included), and in/out degree.

The SQLite file has tables `pages`, `sections`, `anchors`, `links`, `code_blocks` and `meta`,
plus `nodes` and `edges` views, so you can query it directly:

```sql
-- pages that many others link to but that have no description
SELECT key, title, in_links FROM pages WHERE description = '' ORDER BY rank DESC LIMIT 10;
-- which sections are linked to most
SELECT dst, count(*) FROM edges WHERE type = 'links_to' AND dst LIKE '%#%' GROUP BY dst ORDER BY 2 DESC;
```

## `docsbot audit`

```
$ docsbot audit https://docs.example.com
  Overall score  84 / 100 (B)
  afdocs         79 / 100 (C)
  Graph checks   92 / 100
```

The score combines two parts (60/40 by default, `audit.afdocs_weight`):

- **afdocs.** The [Agent-Friendly Documentation Spec](https://agentdocsspec.com/spec/web/)
  checks: llms.txt size and directives, markdown availability, content negotiation, page
  size, content start position, cache headers and more. docsbot runs
  `npx -y afdocs@0.23 check URL --format json --score` and reads the JSON, so it needs
  Node 22+. `--afdocs auto` (default) runs it when `npx` is on `PATH`, `on` requires it,
  `off` skips it. Pass extra flags with `--afdocs-arg=--sampling=deterministic`.
- **Graph checks.** Things that need the whole site rather than a sample of pages:
  llms.txt exists and lists every page, orphan pages, click depth, broken internal links and
  anchors, titles, descriptions, heading structure, page size, code block language tags,
  and vague link text ("click here").

For a local repo, afdocs runs when you pass `--site https://docs.example.com` (or set
`base_url`). Otherwise the score uses graph checks only and says so. `--fail-under 70`
exits `1` below the threshold.

## Configuration

Flags override `./docsbot.yml` (or `--config path`). See
[`docsbot.example.yml`](docsbot.example.yml) for every key. The ones you are most likely to
want:

```yaml
base_url: https://docs.example.com
exclude: ["drafts/**"]
links:
  exclude_urls: ['^https://(www\.)?linkedin\.com/']
  ignore_hrefs: ['^g$']     # render-hook shorthands that aren't real URLs
audit:
  fail_under: 70
```

Global flags: `-f json`, `-o file`, `-q`, `--no-cache`, `--concurrency`, `--max-pages`,
`--delay`, `--timeout`, `--include`/`--exclude`, `--no-follow`, `--base-url`, `--content-dir`.

Exit codes: `0` OK, `1` findings over the threshold, `2` error.

## CI

```yaml
# .github/workflows/docs.yml
on: pull_request
jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docsbot/docsbot@main
        with:
          command: links . --no-external
```

## Development

```bash
make test             # unit and end-to-end tests (no network needed)
make serve-example    # serve testdata/site as a live site on :8484
```

`docsbot serve DIR` (hidden) serves a local repo as a small website with HTML pages, `.md`
twins, `llms.txt`, a sitemap and alias redirects. The end-to-end tests use it to exercise
live mode, and you can point afdocs at it.

Layout: `internal/fetch` (HTTP client), `internal/parse` (goldmark + x/net/html),
`internal/crawl` (discovery and link resolution), `internal/graph` (graph, PageRank, SQLite,
exports), `internal/links`, `internal/readiness`, `cmd` (Cobra).

Pure Go throughout (modernc SQLite, no cgo), so `CGO_ENABLED=0` cross-compiles everywhere.
Releases are built with GoReleaser on `v*` tags.
