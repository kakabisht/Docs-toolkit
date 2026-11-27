---
title: Upgrade lumen
description: Upgrade a workspace safely.
---

# Upgrade lumen

Run the upgrade with a backup first:

```sh
lumen upgrade --backup
```

Use `--dry-run` to see what would change. To skip the version check, add
`--force-upgrade`.

See [Upgrade flags](reference/flags.md) for every flag.
