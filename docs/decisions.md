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
