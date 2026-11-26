#!/bin/sh
# Stands in for `npx afdocs` in tests: records its arguments and prints the
# report named by AFDOCS_FIXTURE. No network.
printf '%s\n' "$@" > "${AFDOCS_ARGS_OUT:-/dev/null}"
cat "$AFDOCS_FIXTURE"
