# Decisions

Each entry records a choice, why it was made, and what would change it.
Entries marked **open** need a decision from the project owner.

## D1. Language: Go

Single static binary, fast enough to read thousands of pages in under a second,
easy to ship in a GitHub Action. Decided by the project owner.

## D2. Name and licence

Decided by the project owner: the repository is `Docs-toolkit` and the command
is `docsbot` (the requirements' `docsctl` is dropped). The licence is Apache
2.0, permissive with an explicit patent grant, as is usual for developer tools.

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

## D14. Convert: native Go, not Pandoc

The earlier converter (Convert-ascii-md) ran a preprocess → Pandoc →
postprocess pipeline and needed Python and Pandoc installed. Pandoc reads
AsciiDoc only through DocBook or not at all, depending on the version, so the
old pipeline had to rewrite macros, conditionals and attributes before Pandoc
saw them and repair headings and links afterwards. Each repair was a place
where content could change without a record.

`docsbot convert` is native Go: preprocess (header, attributes, conditionals,
includes), a scan pass that records every section and block id with the
Markdown anchor it will get, then one renderer. Because one program sees every
step, every construct it can't carry over exactly is written to the report as
**approximated**, **dropped** or **review**, with file and line. It ships in the
same binary, with nothing else to install.

Carried over from the old tool: the content check. `compare_docs.py` checked
that the Markdown kept the source's content; here the same idea runs after
every conversion and lists source lines whose words are nowhere in the output,
unless the loss is already reported.

Defaults, all overridable:

- Output goes to `--out`, which must not be the source folder. A file is only
  rewritten when its content changes, so a second run changes nothing; files
  that replaced different content are counted in the report.
- The document title goes into front matter (`title:`); `--h1` also writes it
  as a heading. Description, keywords (as tags) and author go into front matter
  too.
- `--target gfm` (GitHub alerts for admonitions, bold-term lists for
  description lists). `mkdocs`, `docusaurus`, `hugo` and `commonmark` change
  admonition and definition-list syntax, and MkDocs gets four-space list
  indents.
- Includes are inlined, and files that other files include are not converted on
  their own. Conditionals that can't be decided are kept (`--unknown drop` to
  leave them out); either way they are reported.
- Antora: attributes from the governing `antora.yml` apply, the document may
  override them (soft and hard sets are treated alike), and resource ids
  resolve within the component. References to other components are reported,
  because their pages are not in the source.
- Tables with merged cells or block content become HTML; others become pipe
  tables. Callouts become `(n)` in a code comment for the block's language.
- `--check` runs the offline link and image checks on the output.

Tested against the fixture in `testdata/fixtures/convert` (golden output for all
five targets) and dogfooded on the public Asciidoctor documentation (95 pages);
cases found there are regression tests.

## D15. Drafts: cite everything, check facts without a model

`draft write` builds a best-practice page from the repository by default
(decided by the project owner): every page under the content root is a source,
except pages that are themselves drafts. `--kb` adds other local folders and
`--transcript` adds interview transcripts (WebVTT, SRT, plain text with
optional `[hh:mm:ss]` stamps and `Name:` speakers); both are optional. URLs
are refused, so nothing is fetched. Sources are cut into passages (paragraphs,
list items, code blocks; transcript cues by the same speaker up to about 80
words) and each template section gets the best passages by BM25.

- **Without a model (the default)** the draft is extractive: up to three
  sentences per passage, as written, each with its citation; code blocks with
  a `Source:` line. Transcript fillers ("um", "you know") and doubled words are
  removed and the first letter is capitalised; that is the only change to the
  text, and the draft says it must be rewritten.
- **With a model** one interface covers Ollama, any OpenAI-compatible server
  (llama.cpp, LM Studio, vLLM, hosted services) and Anthropic (official Go SDK,
  default model `claude-opus-5-5`, server-side refusal fallback on). The prompt
  is versioned (`internal/draft/prompts/draft-v1.md`); its version and hash are
  in the front matter and the ledger. Temperature 0 where the API allows it.
- **Nothing leaves the machine unless configured.** Endpoints that are not
  loopback are refused unless `draft.allow_remote: true` or `--allow-remote`;
  Anthropic always needs it.
- **Citations are checked, not trusted.** After writing, every sentence and
  code block of `draft.md` becomes a claim. A claim citing nothing, or only ids
  that are not sources, is uncited and marked **[uncited]** in the draft. The
  ledger extracts numbers, versions, flags, commands, parameters, fields and
  defaults with fixed patterns and records whether each appears word for word
  in a cited passage; those that don't are listed first in `review.md`. This
  catches invented values; it does not prove a statement true, which is why a
  person verifies every claim.
- **Facts are checked against the repository.** Code, configuration, schemas
  and the table rows of reference pages are read line by line. Flags are looked
  for in flag definitions and tables, fields and parameters as keys, versions
  as text, and a default or number next to a setting is compared with the
  value or `default:` the repository gives for that key. A different value is
  reported as a conflict at the top of the claim in `review.md`. The page a
  claim cites never counts as its own confirmation. These are text matches: a
  confirmation means "found", not "proved".
- **The knowledge graph links claims to pages.** Commands, flags and settings a
  claim names are listed with the pages that define or document them.
- **Review survives edits.** Claims are matched to `draft.md` by a hash of
  their text. `draft verify` and `check` match again first: edited and new
  statements become unverified claims, deleted ones are marked removed.
- **No publishing.** Output goes to `drafts/<topic>` (or `--out`), never inside
  a knowledge-base folder, with `status: draft` and `review: required`. An
  existing draft is only replaced with `--force`. `check` (rule
  `draft-unverified-claims`) fails a page whose status is ready, approved,
  final, published or done while its `claims:` ledger has claims that are not
  verified, or rejected or needing change.

Tests use synthetic transcripts about the placeholder product and a fake model;
the HTTP providers are tested against local servers.

## D16. The GitHub Action runs one command

`action.yml` installs docsbot with Go and runs the command it is given, so
every feature is available in CI without a separate interface to maintain.
Node is only set up when `node: true`, for `agent-ready check`. For pull
requests, `check --changed-only -f github` reports on changed files as
annotations; `-f sarif` suits code scanning. The repository is github.com/kakabisht/Docs-toolkit.

## D17. `agent-ready check` on a local repository

Without `--site`, docsbot serves the repository on a local port and runs
AFDocs against that preview, so the whole check works before anything is
deployed (decided by the project owner). The preview serves only the
`llms.txt` and `llms-full.txt` the repository ships; it does not generate
them, so a missing file is reported as missing. Checks whose answer depends on
the host, the theme or the server (HTML size, content negotiation, status
codes, redirects, caching, rendering, authentication, Markdown URLs) are
marked skipped, left out of the score, can't cap it, and are listed by name.
The checks that count are the llms.txt checks and those on the pages'
Markdown. When the generator configuration shows an llms.txt plugin, the
report says the file may be made at build time. Checks AFDocs adds later count
as host checks until they are reviewed.

## D18. The documentation agent: fixed steps, human approval

`docsbot agent` is an agent with a fixed plan, not one where a model picks its next
tool. The five steps (gaps, MCP context, draft, validate, evaluate) always run in
that order for every draft, so no draft can skip validation, and approval is a
separate command a person runs. A model, when configured, writes the prose and
can judge it; it cannot publish or pass its own work. Without a model the agent
still runs end to end, with extractive drafts and the deterministic checks.

- **Gaps are text matches.** Flags, environment variables, endpoints and config
  keys are read from code with fixed patterns per language, each with file and
  line, and looked for on the pages. "Documented" means the name was found, not
  that the page is right. Config keys match by full path or by leaf name on lines
  that show keys (code spans, YAML, tables), so common leaf names can hide a gap.
  Test files, dependencies and `.env` (which may hold secrets) are never read.
- **Changes come from `git diff`.** Items are extracted from added and removed
  lines; a name on both sides with a different default is *changed*, a removed
  name no longer anywhere in the code is *removed*. YAML keys on a diff line are
  read without their parents, so nested keys are matched by their leaf.
- **Context is MCP, cited like any source.** Each server is queried twice per
  group (the items; their examples). Passages that don't name an item are
  dropped unless nothing else came back. Remote servers need `allow_remote`,
  because the queries name the product's flags and settings. `docsbot mcp serve`
  is the reference server, so the whole agent runs offline.
- **Out-of-date docs are not sources for their own fix.** For changed or removed
  items the repository's pages are left out of the draft's sources.
- **The gate.** Score weights (citations 30, grounded facts 30, gap items 25,
  sections 15) and the threshold of 80 are starting values. Contradicted facts,
  invalid citations, flags or endpoints the code does not define, examples that
  fail validation and broken links block whatever the score. The judge prompt is
  versioned (`judge-v1`); its scores are recorded and can only block.
- **Approval is tied to a version.** `eval.json` and `approval.json` hold the
  SHA-256 of `draft.md`; an edit after evaluation needs a new evaluation, and an
  edit after approval a new approval. Publishing writes new pages only and never
  overwrites one; changes to existing pages are proposed, not applied.
