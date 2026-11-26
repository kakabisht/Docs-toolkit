---
title: "Deploying lumen"
status: draft
review: required
topic: "Deploying lumen"
generator: "docsbot draft (extractive)"
generated: 2026-01-01
claims: claims.json
---

<!-- Draft for review: every statement cites a source passage. Check each claim in review.md and record it with `docsbot draft verify`; `docsbot check` fails while a page marked ready has unverified claims. -->

<!-- Extractive draft: sentences are taken from the sources as written. Rewrite them in your own words and keep the citations. -->

## Summary

<!-- What the practice achieves and when it applies, in two or three sentences. -->

Source:[^s17]

```
$ lumen deploy --force
```

Source:[^s8]

```
lumen init --workspace demo
lumen deploy --force
```

## Before you start

<!-- Prerequisites, versions, access. -->

Start with Install, then Configure.[^s6] The CLI reference lists every command.[^s6]

Restart the agent after every upgrade to version 2.1 or later.[^s29]

See also a page that is gone.[^s10]

Source:[^s12]

```
lumen init
```

## Recommended practices

<!-- The practices themselves, each with the reason. -->

The first thing, always run lumen init with a named workspace.[^s24] We keep one workspace per environment, so staging and production never share a config file.[^s24]

Run `lumen deploy` after you change `server.port`.[^s3]

The default port is 8080.[^s26] If you change server.port you have to run lumen deploy again, otherwise nothing changes.[^s26] The most common mistake is running lumen deploy --force on production without a backup.[^s26]

## Common mistakes

<!-- What goes wrong and how to recognise it. -->

Source:[^s22]

```
Error: connection refused by server 10.0.0.1
```

## Check that it works

<!-- How to verify the result. -->

To check that it works, run lumen status and look for the word ready.[^s28]

The API serves `GET /v1/status`.[^s20]

[^s3]: docs/configure.md, line 18: “Run `lumen deploy` after you change `server.port`.”
[^s6]: docs/index.md, lines 8–9: “Start with [Install](install.md), then [Configure](configure.md#settings). The [CLI reference](reference/cli.md) lists every command.”
[^s8]: docs/install.md, lines 15–16: “lumen init --workspace demo lumen deploy --force”
[^s10]: docs/install.md, line 21: “Next, [configure lumen](configure.md). See also [a page that is gone](missing.md).”
[^s12]: docs/old-install.md, line 11: “lumen init”
[^s17]: docs/reference/cli.md, line 22: “$ lumen deploy --force”
[^s20]: docs/reference/config.md, line 13: “The API serves `GET /v1/status`.”
[^s22]: docs/troubleshoot.md, line 10: “Error: connection refused by server 10.0.0.1”
[^s24]: call.vtt at 00:00:01, Expert A: “Um, so the first thing, always run lumen init with a named workspace. We keep one workspace per environment, so staging and production never share a config file.”
[^s26]: call.vtt at 00:00:18, Expert A: “The default port is 8080. If you change server.port you have to run lumen deploy again, otherwise nothing changes. The most common mistake is running lumen deploy --force on production without a backu…”
[^s28]: notes.txt at 00:03:10, Expert A: “To check that it works, run lumen status and look for the word ready.”
[^s29]: notes.txt, line 3: “Restart the agent after every upgrade to version 2.1 or later.”
