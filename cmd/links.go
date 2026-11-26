package cmd

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/docsbot/docsbot/internal/links"
)

func newLinksCmd() *cobra.Command {
	var db string
	var noExternal, redirects bool
	var failOn []string
	var limit int
	c := &cobra.Command{
		Use:   "links [SOURCE]",
		Short: "Find broken links, missing anchors, redirects and dead external URLs",
		Long: `Checks every link in the docs:

  internal  links to other docs pages (missing page = broken)
  anchor    #section links, on the same page or another page
  asset     images and downloads
  external  other sites, checked with HEAD (GET fallback), cached between runs

Problems are grouped by target and ranked by impact: the summed PageRank of
the pages carrying the bad link, so a break on the home page sorts first.

Exit code 1 when a problem listed in --fail-on is found.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot links ./docs
  docsbot links https://docs.example.com --fail-on broken,broken-anchor,redirect
  docsbot links ./docs --base-url https://docs.example.com --no-external -f json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			client := newClient(cfg)
			g, err := loadGraph(cmd.Context(), cfg, client, args, db)
			if err != nil {
				return err
			}
			var excl []*regexp.Regexp
			for _, p := range cfg.Links.ExcludeURLs {
				re, err := regexp.Compile(p)
				if err != nil {
					return fmt.Errorf("links.exclude_urls %q: %w", p, err)
				}
				excl = append(excl, re)
			}
			ign, err := compileAll(cfg.Links.IgnoreHrefs, "links.ignore_hrefs")
			if err != nil {
				return err
			}
			checkExt := cfg.CheckExternal() && !noExternal
			if checkExt {
				progress("checking links (external included; --no-external to skip)")
			}
			rep := links.Check(cmd.Context(), g, client, links.Options{
				External: checkExt, Redirects: redirects, Exclude: excl, Ignore: ign, Workers: cfg.Concurrency * 2,
				Progress: throttle(func(d, t int) { progress("  checked %d/%d URLs", d, t) }),
			})
			if cmd.Flags().Changed("fail-on") {
				cfg.Links.FailOn = failOn
			}
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
				printLinks(w, rep, limit)
			}
			if rep.Failed(cfg.Links.FailOn) {
				return &exitError{code: ExitFindings}
			}
			return nil
		},
	}
	c.Flags().StringVar(&db, "db", "", "check links in a graph built with `graph build` instead of crawling")
	c.Flags().BoolVar(&noExternal, "no-external", false, "skip external URLs")
	c.Flags().BoolVar(&redirects, "redirects", false, "also report external links that redirect (internal redirects are always reported)")
	c.Flags().StringSliceVar(&failOn, "fail-on", []string{"broken", "broken-anchor"}, "problems that exit 1: broken, broken-anchor, unreachable, blocked, redirect (empty for never)")
	c.Flags().IntVar(&limit, "limit", 50, "max issues to print in text output (0 = all)")
	return c
}

func printLinks(w io.Writer, rep *links.Report, limit int) {
	total := 0
	for _, n := range rep.Checked {
		total += n
	}
	fmt.Fprintf(w, "Checked %d link targets across %d pages", total, rep.Pages)
	var parts []string
	for _, k := range []string{"internal", "anchor", "asset", "external"} {
		if rep.Checked[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, rep.Checked[k]))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(parts, ", "))
	}
	fmt.Fprintln(w)
	if len(rep.Issues) == 0 {
		fmt.Fprintln(w, "\nNo problems found.")
		return
	}
	fmt.Fprintf(w, "\n%d broken, %d broken anchors, %d unreachable, %d blocked, %d redirects\n",
		rep.Counts[links.Broken], rep.Counts[links.BrokenAnchor], rep.Counts[links.Unreachable], rep.Counts[links.Blocked], rep.Counts[links.Redirect])
	cur := ""
	for i, is := range rep.Issues {
		if limit > 0 && i >= limit {
			fmt.Fprintf(w, "\n… %d more (use --limit 0 or --format json)\n", len(rep.Issues)-limit)
			break
		}
		if is.Problem != cur {
			cur = is.Problem
			fmt.Fprintf(w, "\n%s\n", strings.ToUpper(strings.ReplaceAll(cur, "-", " ")))
		}
		status := ""
		if is.Status > 0 {
			status = fmt.Sprintf(" [%d]", is.Status)
		}
		fmt.Fprintf(w, "  %s %s%s  impact %.2f\n", is.Kind, is.Target, status, is.Impact)
		if is.FinalURL != "" {
			fmt.Fprintf(w, "      → %s\n", is.FinalURL)
		}
		if is.Detail != "" {
			fmt.Fprintf(w, "      %s\n", is.Detail)
		}
		for j, s := range is.Sources {
			if j == 3 {
				fmt.Fprintf(w, "      … linked from %d more places\n", len(is.Sources)-3)
				break
			}
			loc := s.Page
			if s.Path != "" {
				loc = s.Path
				if s.Line > 0 {
					loc = fmt.Sprintf("%s:%d", s.Path, s.Line)
				}
			} else if s.URL != "" {
				loc = s.URL
			}
			fmt.Fprintf(w, "      from %s\n", loc)
		}
	}
}

func compileAll(pats []string, name string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", name, p, err)
		}
		out = append(out, re)
	}
	return out, nil
}
