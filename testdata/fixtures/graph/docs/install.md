---
title: Install lumen
tags: [setup, cli]
audience: [operators]
version: "2.1"
supersedes: old-install.md
---

# Install lumen

Download the binary and create a workspace. The config file is created for you
in the workspace folder, next to your project files and data directories.

```bash
lumen init --workspace demo
lumen deploy --force
```

--8<-- "snippets/note.md"

Next, [configure lumen](configure.md). See also [a page that is gone](missing.md).
