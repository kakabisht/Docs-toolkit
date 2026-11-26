package cmd

import "flag"

// upgrade flags (synthetic fixture code)
var (
	backup = flag.Bool("backup", false, "back up the workspace before upgrading")
	dryRun = flag.Bool("dry-run", false, "show what would change")
)
