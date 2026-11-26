---
title: "One command line for the jobs docs teams script by hand"
status: draft
author: "[placeholder: author]"
date: "[placeholder: publication date]"
---

# One command line for the jobs docs teams script by hand

Most documentation teams have a folder of scripts. One checks links before a
release. One turns AsciiDoc into Markdown for a migration that is always nearly finished.
Another builds an llms.txt file because someone asked whether the docs work with AI
assistants. Each script solves one problem for one repository, and each one quietly stops
working when the site generator changes.

docsbot (a working name) is an attempt to put those jobs into one tool. It is a single Go
binary that reads a local docs repository or a live site and does six things: checks,
a knowledge graph, an audit, agent readiness, AsciiDoc conversion and cited drafts. This
post explains what it does and, as importantly, what it refuses to do.

## Four rules

The tool is built around four rules, because documentation tooling fails in predictable
ways when it doesn't follow them.

**No silent edits.** Tools that "fix" content tend to fix it wrongly in a few places nobody
notices. docsbot never changes your source. The converter, the agent-file generator and
the drafting command all write to a separate folder and refuse to write into the docs.
Anything they could not carry over exactly goes into a report, with the file and line.

**No publishing.** Nothing is pushed, deployed or posted. A person decides what ships.

**Nothing leaves the machine unless you say so.** Only the drafting command can use a
language model. It works without one, it prefers a local model when you configure one, and
it refuses to send passages from your repository anywhere else unless you set
`allow_remote`.

**Evidence over opinion.** Every finding, graph edge and draft statement carries the file
and line it came from. Scores come with the method that produced them, and they are labelled
as indicators, not measurements of whether readers succeed.

## What it does

`check` covers the basics a docs CI job should catch: broken links and anchors, missing
images, orphan pages, skipped heading levels, invalid front matter and leftover TODOs. It
understands how Hugo, MkDocs, Docusaurus and friends turn files into URLs, so a link that
works on the published site is not reported as broken. In a pull request it can report on
changed files only, as annotations.

`graph` builds a knowledge graph: pages and sections, but also the commands, flags,
configuration keys, endpoints and terms the docs talk about. Each edge records whether it is
explicit (a link), extracted (found by a rule) or inferred (two pages look alike). That
makes questions such as "which pages mention this setting, and does any page define it?"
answerable with a file and line for each answer.

`audit` scores structure and findability against fixed rules and produces a fix-first list.
The weights are starting values, and the report says so.

`agent-ready` wraps [AFDocs](https://afdocs.dev), the agent-friendly documentation checks,
with a deterministic sample of pages so the same site gives the same result, and it leads
with the blocking problem when AFDocs caps a score. It can also generate llms.txt and
Markdown copies of pages into a folder you review.

`convert` turns AsciiDoc into Markdown without Pandoc. Its main feature is the report: every
admonition title it had to approximate, every conditional it could not evaluate, every cross
reference it could not resolve. A content check afterwards lists source lines whose words
did not make it into the output. During development it was run over the public Asciidoctor
documentation (95 pages); the problems that run turned up are now regression tests.

`draft` is the newest and the most careful. Given a topic, a knowledge base and interview
transcripts, it writes a best-practice draft in which every statement cites the passage it
came from. It then extracts the details a reviewer must check (numbers, versions, flags,
commands, defaults) and records whether each appears in the cited passage. A model can write
the prose, but a model is not trusted: an invented number shows up as "not found in the
cited source", and a sentence without a real citation is marked uncited. A reviewer works
through the checklist with `draft verify`, and `check` fails the page if it is marked ready
while claims are still open.

## What it isn't yet

It is not finished. The name and licence are undecided. The audit thresholds need tuning on
real sites, and an information architecture review is deferred until there is a rubric to
apply. The agent-readiness regression fixture still needs real findings added. Decisions and
their reasons are written down in the repository, so you can see why it works the way it
does and argue with it.

The code is at github.com/kakabisht/Docs-toolkit. [placeholder: how to try it, once the repository is public]
