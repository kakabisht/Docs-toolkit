---
title: Lumen 2.1 release notes
---

# Lumen 2.1 release notes

## Backup retention

Lumen 2.1 deletes old upgrade backups automatically. The `--keep-days` flag
sets how many days a backup is kept; the default is 14 days. Set it to 0 to
keep backups forever.

```sh
lumen upgrade --backup --keep-days 30
```

## Logging

Set `LUMEN_LOG_LEVEL` to `debug`, `info`, `warn` or `error` to choose how much
lumen logs. When it is not set, lumen logs at `info`.

## Removed

The `--force-upgrade` flag was removed in 2.1. Upgrades now always check the
version first.
