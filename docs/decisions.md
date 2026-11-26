# Decisions

Each entry records a choice, why it was made, and what would change it.
Entries marked **open** need a decision from the project owner.

## D1. Language: Go

Single static binary, fast enough to read thousands of pages in under a second,
easy to ship in a GitHub Action. Decided by the project owner.

## D2. Name and licence (open)

`docsbot` is a working name in code; the requirements call it `docsctl`, also a
placeholder. No licence file yet. Both need the project owner.

## D3. Includes and partials

Links inside a partial are reported at the partial's own file and line, so the
fix lands in one place. Inlined includes (Hugo include shortcodes, MkDocs
snippets, AsciiDoc `include::`) resolve relative links from the including page,
as the generators do. MDX imports resolve from the partial's folder.

## D4. Navigation sources

Navigation comes from, in order: `nav_file` in the config, `mkdocs.yml` nav,
`SUMMARY.md`, `sidebars.json` or a YAML/JSON list, and finally the folder
structure. A page in the folder-derived navigation is not an orphan, because
generators such as Hugo list section children automatically.

## D5. Graph storage: SQLite plus exports

The graph is one SQLite file (pure Go driver, no cgo) with generic `nodes`,
`edges` and `reviews` tables. Exports cover JSON (the golden format), GraphML,
CSV, JSON-LD, Cypher, Mermaid and DOT. A graph database server was not needed
for any query the CLI makes.

## D6. Entity extraction without a model

Deterministic rules only, each with a file, line and span:

- commands and flags from shell code blocks; a command counts as the product's
  when a page title or heading names it, or it appears on at least 30% as many
  pages as the most used one (override with `graph.commands`);
- configuration keys from YAML, TOML and JSON blocks, and from reference tables;
- API endpoints (`GET /path`) and error messages;
- glossary terms from glossary pages, and vocabulary terms and aliases in prose;
- definition-list terms only when they look like flags or keys, or name a known entity.

A page *defines* an entity when its title, a glossary entry or a reference row
names it, and *documents* it when its title or a heading names it. Names that
differ only in case or separators are listed for review, never merged. An
optional model extractor can add inferred edges, but each must cite a span from
the page and none can replace an explicit or extracted edge.

## D7. Similarity

`related-to` uses TF-IDF cosine over title and headings (weighted three times),
description and prose, top three per page at 0.35 or more. `overlaps-with` uses
shared five-word sequences relative to the shorter page, at 50% or more. Both
are labelled inferred with the score as confidence. Thresholds are configurable;
they are starting values, not measured ones.

## D8. Search: local BM25, site search later

Findability uses a local BM25 index (title x3, headings and definition terms x2,
description x2, tags x2, prose x1, code x0.5, light English stemming). It runs
offline and gives the same answer every time, so it can sit in CI. It is a
stand-in for the site's own search, and the report says so. A plug-in for a
real site search (Algolia, Typesense, Pagefind) can come later behind the same
`Search(query, n)` call if results need to match production.

## D9. Audit thresholds and weights (starting values)

Defaults: 3 clicks, navigation 3 levels and 15 entries per menu, pages of 100
to 3,000 words, sections up to 600 words, descriptions of 50 to 160
characters, titles up to 70 characters, 365 days for freshness, search success
in the top 3. Dimension weights favour search tasks (2), templates, metadata
and click paths (1.5). None of these is measured; all are configurable under
`audit`, and the report prints how each score is computed. Revisit once real
tasks and query logs are available.

## D10. `audit architecture` is deferred

An information architecture review needs a rubric of what good looks like for
the docs set in question. Until one is supplied, the command explains that and
exits 2 rather than inventing criteria.

## D11. Old commands moved

The phase-one readiness audit (afdocs plus graph checks) moved from `audit` to
`agent-ready check`, so `audit` can mean structure and findability as the
requirements describe. `links` stays as a shortcut for `check --only links,images`.

## D12. Agent readiness: wrap AFDocs, sample deterministically

`agent-ready check` runs AFDocs for delivery checks on a live URL (content
negotiation, Markdown URLs, rendering, caching) and adds docsbot's whole-site
checks, which also run on a local folder. AFDocs picks pages at random by
default; docsbot discovers pages itself (llms.txt, sitemap, crawl), takes a
sample spread across top-level sections with the home page always included,
and passes it with `--urls`, so the same site gives the same result.

Labels: **audit** when every discovered page was checked and discovery was not
cut off by `--max-pages`; **indicative** otherwise, or when the sample is under
30 pages, with a warning. When AFDocs caps its score (no llms.txt, mostly
client-side rendering, no viable content path), the report leads with the
blocking issue and the score before the cap; the capped number is never the
headline, because it says "blocked", not "how good".

The Juju 3.6 findings named in the requirements as a regression fixture are not
in the repository yet; `testdata/fixtures/agent-ready/juju-3.6/README.md` says
what to add. A synthetic capped report covers the same rules meanwhile.

## D13. Generated agent files go to a separate folder

`agent-ready generate` writes to `build/agent-ready` (or `--out`) and refuses a
folder inside the content root, so it can't create docs pages or change the
source. Page copies sit at the page URL plus `.md` (home page `index.md`),
which is the first place agents and AFDocs look. llms.txt follows llmstxt.org,
with navigation sections as headings; above 50,000 characters it becomes an
index of per-section files under `llms/`, as AFDocs recommends. Shortcodes and
JSX are kept as written and listed rather than guessed at.
