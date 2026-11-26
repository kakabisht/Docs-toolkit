# Docs audit: testdata/fixtures/graph

**Score: 60/100** (structure 55, findability 69) across 8 pages.

Scores come from deterministic rules with the weights listed at the end. They are indicators for deciding what to fix first, not measurements of reader success.

## Fix first

1. `docs/configure.md:1` Missing required metadata: description _(metadata)_
2. Task "Uninstall lumen": none of the expected pages exist: uninstall.md _(search)_
3. `docs/configure.md:1` how-to page lacks numbered steps _(templates)_
4. `docs/install.md:1` Missing required metadata: description _(metadata)_
5. `docs/reference/cli.md:1` Missing required metadata: description _(metadata)_
6. `docs/glossary.md:1` Missing required metadata: description _(metadata)_
7. `docs/old-install.md` Can't be reached by clicking from the entry page _(click-paths)_
8. `docs/old-install.md:1` Missing required metadata: description _(metadata)_
9. `docs/reference/config.md:1` Missing required metadata: description _(metadata)_
10. `docs/troubleshoot.md:1` Missing required metadata: description _(metadata)_

## Structure

| Dimension | Score | Weight | Summary |
| --- | ---: | ---: | --- |
| Content types | 12 | 1 | 1 of 8 (12%) pages declare a content type |
| Template conformance | 38 | 1.5 | 3 of 8 (38%) pages with a template conform |
| Titles and headings | 100 | 1 | 8 of 8 (100%) pages have no title or heading problem |
| Page shape | 12 | 1 | 1 of 8 (12%) pages have no shape problem |
| Metadata completeness | 47 | 1.5 | title 8 of 8 (100%); description 1 of 8 (12%); tags 2 of 8 (25%); owner 2 of 8 (25%) |
| Duplication | 75 | 1 | 2 pages overlap with another page or share its description |
| Terminology | 62 | 1 | 5 of 8 (62%) pages use preferred terms and vocabulary tags |
| Navigation depth and breadth | 96 | 1 | navigation from mkdocs.yml: 9 entries, 2 levels deep at most |
| Ownership and freshness | 25 | 0.5 | 2 of 8 (25%) pages have an owner; no git history, so freshness is unknown |
| URL quality | 100 | 0.5 | 8 of 8 (100%) URLs are clean |

### Content types

_How it is scored:_ Share of pages whose content type is declared (front matter or config). Inferred types are a guess from path, title and shape.

- distribution: how-to 4, reference 3, landing 1

7 items:

- `docs/configure.md:1` Content type not declared; it looks like how-to
- `docs/glossary.md:1` Content type not declared; it looks like reference
- `docs/index.md:1` Content type not declared; it looks like landing
- `docs/install.md:1` Content type not declared; it looks like how-to
- `docs/old-install.md:1` Content type not declared; it looks like how-to
- `docs/reference/cli.md:1` Content type not declared; it looks like reference
- `docs/troubleshoot.md:1` Content type not declared; it looks like how-to

### Template conformance

_How it is scored:_ Share of pages that meet the template for their content type (steps for how-to and tutorials, a table, definition list or code for reference, links for landing pages, required headings).

- templates: how-to, landing, reference, tutorial

5 items:

- `docs/configure.md:1` how-to page lacks numbered steps
- `docs/glossary.md:1` reference page lacks a table, definition list or code example
- `docs/install.md:1` how-to page lacks numbered steps
- `docs/old-install.md:1` how-to page lacks numbered steps
- `docs/troubleshoot.md:1` how-to page lacks numbered steps

### Titles and headings

_How it is scored:_ Share of pages with no title or heading problem: missing or long titles (over 70 characters), duplicate titles, skipped heading levels, generic headings, and headings in the minority case style.

- heading style: 1 sentence case, 0 title case
- duplicate titles: 0

### Page shape

_How it is scored:_ Share of pages without a shape problem: under 100 words (except landing pages), over 3000 words, a section over 600 words without a subheading, or over 500 words with no headings.

- words per page: median 27, 90th percentile 51, longest 51

7 items:

- `docs/configure.md:1` Only 22 words
- `docs/glossary.md:1` Only 16 words
- `docs/install.md:1` Only 51 words
- `docs/old-install.md:1` Only 30 words
- `docs/reference/cli.md:1` Only 27 words
- `docs/reference/config.md:1` Only 28 words
- `docs/troubleshoot.md:1` Only 14 words

### Metadata completeness

_How it is scored:_ Mean per page of 70% x share of required fields (title, description) plus 30% x share of recommended fields (tags, owner).

- title: 8 of 8 (100%)
- description: 1 of 8 (12%)
- tags: 2 of 8 (25%)
- owner: 2 of 8 (25%)

8 items:

- `docs/configure.md:1` Missing required metadata: description
- `docs/glossary.md:1` Missing required metadata: description
- `docs/install.md:1` Missing required metadata: description
- `docs/old-install.md:1` Missing required metadata: description
- `docs/reference/cli.md:1` Missing required metadata: description
- `docs/reference/config.md:1` Missing required metadata: description
- `docs/troubleshoot.md:1` Missing required metadata: description
- `docs/index.md:1` Missing recommended metadata: tags, owner

### Duplication

_How it is scored:_ Share of pages not involved in an overlap (half or more of the shorter page's five-word sequences repeated on another page) or a duplicated description.

1 item:

- `docs/install.md` Overlaps with docs/old-install.md: 22 of 22 five-word sequences shared (100%)

### Terminology

_How it is scored:_ Share of pages that use preferred terms only (no vocabulary aliases in prose) and tag only with vocabulary terms. Not scored without a vocabulary file.

- vocabulary terms: 2
- suspected duplicate entities: 0

5 items:

- `docs/install.md:11` Uses "config file"; the preferred term is "configuration file"
- `docs/old-install.md:7` Uses "config file"; the preferred term is "configuration file"
- `docs/configure.md:1` Tag "setup" is not in the vocabulary
- `docs/install.md:1` Tag "setup" is not in the vocabulary
- `docs/install.md:1` Tag "cli" is not in the vocabulary

### Navigation depth and breadth

_How it is scored:_ Mean of: share of pages in the navigation (explicit navigation only), share of navigation entries at most 3 levels deep, and share of menus with at most 15 entries.

- pages in navigation: 7 of 8 (88%)
- entries within depth: 9 of 9 (100%)
- menus within breadth: 3 of 3 (100%)

1 item:

- `docs/old-install.md` Not in the navigation (mkdocs.yml)

### Ownership and freshness

_How it is scored:_ Mean of: share of pages with an owner (front matter or CODEOWNERS) and share changed in the last 365 days (git history). A part without data is left out.

- pages with an owner: 2 of 8 (25%)

### URL quality

_How it is scored:_ Share of page URLs that are lower case, use hyphens (not underscores, spaces or encoded characters), have no file extension, are at most 80 characters and at most 5 segments deep.

## Findability

| Dimension | Score | Weight | Summary |
| --- | ---: | ---: | --- |
| Click paths | 88 | 1.5 | 7 of 8 (88%) pages within 3 clicks; 1 unreachable |
| Search tasks | 83 | 2 | 5 of 6 (83%) queries find an answer in the top 3; 3 of 4 (75%) tasks pass every query |
| Query log coverage | 88 | 1 | 4 distinct searches, 68 of 77 (88%) of all searches matched; 1 gaps |
| Metadata for discovery | 46 | 1 | titles, descriptions and tags checked on 8 pages |
| Cross-linking | 14 | 1 | 1 of 7 (14%) pages have two or more inbound content links; 1 similar pairs don't link |

### Click paths

_How it is scored:_ Share of pages reachable within 3 clicks of the entry page, following content links and the navigation menu.

- pages by clicks: 0: 1, 1: 6
- entry page: /

1 item:

- `docs/old-install.md` Can't be reached by clicking from the entry page

### Search tasks

_How it is scored:_ Share of task queries for which a local BM25 search ranks an expected page in the top 3. A local index stands in for the site's search.

1 item:

- Task "Uninstall lumen": none of the expected pages exist: uninstall.md

### Query log coverage

_How it is scored:_ Share of logged searches (weighted by count) for which a local BM25 search matches every query term on at least one page. Unmatched searches are likely content or vocabulary gaps.

1 item:

- Search "uninstall lumen" (9 times) has no page that matches all its words; nearest: /reference/cli, /configure, /troubleshoot

### Metadata for discovery

_How it is scored:_ Mean per page of three checks: a description of 50 to 160 characters, a specific title (not generic, not shared), and tags when the site uses tags.

7 items:

- `docs/configure.md:1` No description: search engines and llms.txt fall back to the first lines
- `docs/glossary.md:1` No description: search engines and llms.txt fall back to the first lines
- `docs/install.md:1` No description: search engines and llms.txt fall back to the first lines
- `docs/old-install.md:1` No description: search engines and llms.txt fall back to the first lines
- `docs/reference/cli.md:1` No description: search engines and llms.txt fall back to the first lines
- `docs/reference/config.md:1` No description: search engines and llms.txt fall back to the first lines
- `docs/troubleshoot.md:1` No description: search engines and llms.txt fall back to the first lines

### Cross-linking

_How it is scored:_ Share of pages with at least two content links from other pages (the entry page and landing pages excepted). Similar pages that don't link to each other are listed as suggestions; similarity is inferred.

7 items:

- `docs/glossary.md` Only 0 other pages link here in the content
- `docs/install.md` Only 1 other page links here in the content
- `docs/old-install.md` Only 0 other pages link here in the content
- `docs/reference/cli.md` Only 1 other page links here in the content
- `docs/reference/config.md` Only 0 other pages link here in the content
- `docs/troubleshoot.md` Only 0 other pages link here in the content
- `docs/install.md` Suggestion: link to /old-install (similar content, cosine 0.61 over title, headings and text; shared terms: install, lumen, next, workspace, binary)

## Tasks

| Task | Search | Clicks | Queries |
| --- | --- | ---: | --- |
| Install lumen | found | 1 | "install lumen" rank 2; "download binary" rank 2 |
| Change the server port | found | 1 | "server port" rank 2; "change port" rank 1 |
| Fix a refused connection | found | 1 | "connection refused" rank 1 |
| Uninstall lumen | **not found** | unreachable | none of the expected pages exist: uninstall.md |

## Agent summary

- llms.txt: not found
- pages with a description (used for llms.txt entries): 1 of 8 (12%)
- pages over 50,000 characters: 0
- code blocks with a language: 6 of 6 (100%)

## Weights

Each part's score is the weighted mean of its scored dimensions; the overall score is the weighted mean of all of them. Change weights with `audit.weights` in the config.

click-paths 1.5, content-types 1, cross-linking 1, discovery-metadata 1, duplication 1, metadata 1.5, navigation 1, ownership-freshness 0.5, page-shape 1, query-log 1, search 2, templates 1.5, terminology 1, titles-headings 1, urls 0.5

## Notes

- No git history was available, so freshness is not scored.
- The agent summary is a pointer, not a score: run `docsbot agent-ready check` for the full agent-readiness checks.
