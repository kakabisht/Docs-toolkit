// Command docsbot audits docs sites and repos: agent readiness, a knowledge
// graph of pages and sections, and broken link checks.
package main

import (
	"os"

	"github.com/docsbot/docsbot/cmd"
)

func main() { os.Exit(cmd.Execute()) }
