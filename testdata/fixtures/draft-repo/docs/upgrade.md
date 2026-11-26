---
title: Upgrade lumen
description: Upgrade a workspace without losing data.
---

# Upgrade lumen

Always back up the workspace before an upgrade. Run `lumen upgrade --backup` to take a backup and upgrade in one step.

Use `lumen upgrade --dry-run` first to see what will change.

If a download fails, lumen retries it. The default `retries` is 5.

Upgrades to version 2.1.0 or later need a restart of the agent.
