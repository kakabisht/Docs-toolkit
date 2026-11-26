package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/docsbot/docsbot/internal/links"
	"github.com/docsbot/docsbot/internal/readiness"
)

func newAgentReadyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent-ready",
		Short: "Check and improve how well AI agents can use the docs",
	}
	c.AddCommand(newAgentReadyCheck())
	return c
}

func newAgentReadyCheck() *cobra.Command {
	var db, afMode, site string
	var failUnder int
	var afArgs []string
	c := &cobra.Command{
		Use:   "check [SOURCE]",
		Short: "Score agent readiness: afdocs checks plus whole-site graph checks",
		Long: `Scores how well AI agents can discover, navigate and read the docs.

Two parts make up the score:
  afdocs   the Agent-Friendly Documentation Spec checks (llms.txt, markdown
           availability, page size, content negotiation ...), run with
           "npx afdocs" against the live site. Needs Node 22+.
  graph    checks that need the whole graph: llms.txt coverage, orphan pages,
           click depth, broken internal links, titles, descriptions, heading
           structure, page size, code block languages, link text.

For a local repo, afdocs runs when --site (or base_url / --base-url) names the
live site. Exit code 1 when the score is below --fail-under.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot agent-ready check https://docs.example.com
  docsbot agent-ready check ./docs --site https://docs.example.com --fail-under 70
  docsbot agent-ready check ./docs --afdocs off -f json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			cfg, err := loadConfig(firstArg(args))
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("afdocs") {
				cfg.Audit.Afdocs = afMode
			}
			if cmd.Flags().Changed("fail-under") {
				cfg.Audit.FailUnder = failUnder
			}
			if site != "" && cfg.BaseURL == "" {
				cfg.BaseURL = site
			}
			client := newClient(cfg)
			g, err := loadGraph(cmd.Context(), cfg, client, args, db)
			if err != nil {
				return err
			}
			ign, err := compileAll(cfg.Links.IgnoreHrefs, "links.ignore_hrefs")
			if err != nil {
				return err
			}
			lr := links.Check(cmd.Context(), g, client, links.Options{External: false, Ignore: ign})
			checks := readiness.GraphChecks(g, lr, readiness.Options{MaxPageChars: cfg.Audit.MaxPageChars, MaxDepth: cfg.Audit.MaxDepth})

			// afdocs
			target := site
			if target == "" {
				if g.Mode == "live" {
					target = g.Source
				} else {
					target = cfg.BaseURL
				}
			}
			var af *readiness.AfdocsResult
			note := ""
			switch mode := strings.ToLower(cfg.Audit.Afdocs); {
			case mode == "off":
				note = "afdocs skipped (--afdocs off)"
			case target == "":
				note = "afdocs skipped: no live URL (pass --site https://… for a local repo)"
				if mode == "on" {
					return fmt.Errorf("--afdocs on needs a live URL: pass --site")
				}
			case mode == "auto" && !readiness.AfdocsAvailable(cfg.Audit.AfdocsCmd):
				note = "afdocs skipped: npx not found (install Node 22+, or --afdocs off)"
			default:
				progress("running afdocs against %s (this can take a minute)", target)
				af, err = readiness.RunAfdocs(cmd.Context(), cfg.Audit.AfdocsCmd, target, afArgs, 10*time.Minute)
				if err != nil {
					if mode == "on" {
						return err
					}
					note = "afdocs failed, score uses graph checks only: " + firstLine(err.Error())
					af = nil
				}
			}
			rep := readiness.Combine(g.Source, checks, af, note, cfg.Audit.AfdocsWeight)

			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				if err := writeJSON(w, rep); err != nil {
					return err
				}
			} else {
				printAudit(w, rep)
			}
			if cfg.Audit.FailUnder > 0 && rep.Score < cfg.Audit.FailUnder {
				return findings("score %d is below --fail-under %d", rep.Score, cfg.Audit.FailUnder)
			}
			return nil
		},
	}
	c.Flags().StringVar(&db, "db", "", "audit a graph built with `graph build` instead of crawling")
	c.Flags().StringVar(&afMode, "afdocs", "auto", "run afdocs: auto (when npx is available), on (required), off")
	c.Flags().StringVar(&site, "site", "", "live URL to run afdocs against (for a local repo)")
	c.Flags().IntVar(&failUnder, "fail-under", 0, "exit 1 if the overall score is below this")
	c.Flags().StringArrayVar(&afArgs, "afdocs-arg", nil, "extra argument passed to afdocs (repeatable), e.g. --afdocs-arg=--sampling=deterministic")
	return c
}

func printAudit(w io.Writer, r *readiness.Report) {
	fmt.Fprintf(w, "Agent readiness for %s\n\n", r.Source)
	fmt.Fprintf(w, "  Overall score  %d / 100 (%s)\n", r.Score, r.Grade)
	if r.AfdocsScore != nil {
		fmt.Fprintf(w, "  afdocs         %d / 100 (%s)\n", *r.AfdocsScore, r.AfdocsGrade)
	}
	fmt.Fprintf(w, "  Graph checks   %d / 100\n", r.GraphScore)
	if r.AfdocsNote != "" {
		fmt.Fprintf(w, "  Note: %s\n", r.AfdocsNote)
	}

	fmt.Fprintln(w, "\nGraph checks")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range r.Checks {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", strings.ToUpper(c.Status), c.ID, c.Message)
	}
	tw.Flush()

	if r.Afdocs != nil {
		fmt.Fprintf(w, "\nafdocs checks (%d pass, %d warn, %d fail, %d skip)\n", r.Afdocs.Summary.Pass, r.Afdocs.Summary.Warn, r.Afdocs.Summary.Fail, r.Afdocs.Summary.Skip)
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, c := range r.Afdocs.Results {
			if c.Status == "pass" {
				continue
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", strings.ToUpper(c.Status), c.ID, firstLine(c.Message))
		}
		tw.Flush()
	}

	if len(r.Fixes) > 0 {
		fmt.Fprintln(w, "\nTop fixes")
		for i, f := range r.Fixes {
			if i == 10 {
				fmt.Fprintf(w, "  … %d more in --format json\n", len(r.Fixes)-10)
				break
			}
			fmt.Fprintf(w, "  %2d. [%s] %s\n      %s\n", i+1, f.Source, f.Check, f.Text)
		}
	}
	for _, c := range r.Checks {
		if len(c.Examples) > 0 && c.Status != readiness.Pass {
			fmt.Fprintf(w, "\n%s examples\n", c.ID)
			for _, e := range c.Examples {
				fmt.Fprintf(w, "  - %s\n", e)
			}
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
