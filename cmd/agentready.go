package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kakabisht/Docs-toolkit/internal/agentready"
	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/graph"
	"github.com/kakabisht/Docs-toolkit/internal/links"
	"github.com/kakabisht/Docs-toolkit/internal/readiness"
)

func newAgentReadyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent-ready",
		Short: "Check and improve how well AI agents can use the docs",
	}
	c.AddCommand(newAgentReadyCheck(), newAgentReadyGenerate())
	return c
}

func newAgentReadyCheck() *cobra.Command {
	var afMode, site string
	var failUnder, sampleN int
	var afArgs []string
	c := &cobra.Command{
		Use:   "check [SOURCE]",
		Short: "Score agent readiness: afdocs checks plus whole-site checks",
		Long: `Scores how well AI agents can discover, navigate and read the docs.

  afdocs   the Agent-Friendly Documentation Spec checks (llms.txt, Markdown
           availability, page size, content negotiation …), run with
           "npx afdocs" against the live site. Needs Node 22+.
  docsbot  whole-site checks: llms.txt coverage, orphan pages, click depth,
           broken internal links, titles, descriptions, heading structure,
           page size, code block languages, link text.

Sampling. docsbot discovers pages through llms.txt, the sitemap and crawling,
then sends afdocs a sample spread across top-level sections (--sample, default
50; 0 checks every page). The same site always gives the same sample. Results
are labelled "audit" when every discovered page was checked, and "indicative"
otherwise; small samples get a warning.

Caps. afdocs caps its score when a critical check fails (for example, no
llms.txt). A capped number says "blocked", not "how good", so the report leads
with the blocking issue and gives the score before the cap.

For a local repo, the docsbot checks run on every page, and afdocs runs when
--site (or base_url) names the live site. Exit code 1 when the score is below
--fail-under.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot agent-ready check https://docs.example.com
  docsbot agent-ready check https://docs.example.com --sample 0      # every page: an audit
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
			source := firstArg(args)
			if source == "" {
				source = "."
			}
			client := newClient(cfg)
			s, err := loadSite(cmd.Context(), cfg, client, source)
			if err != nil {
				return err
			}
			g := graph.Build(s)
			ign, err := compileAll(cfg.Links.IgnoreHrefs, "links.ignore_hrefs")
			if err != nil {
				return err
			}
			lr := links.Check(cmd.Context(), g, client, links.Options{External: false, Ignore: ign})
			checks := readiness.GraphChecks(g, lr, readiness.Options{MaxPageChars: cfg.Audit.MaxPageChars, MaxDepth: cfg.Audit.MaxDepth})

			target := site
			if target == "" {
				if s.Mode == crawl.Live {
					target = s.Source
				} else {
					target = cfg.BaseURL
				}
			}
			sample := readiness.SamplePages(s, sampleN, cfg.MaxPages)
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
				extra := afdocsSampleArgs(sample)
				progress("running afdocs against %s on %d pages (this can take a minute)", target, sample.Size)
				af, err = readiness.RunAfdocs(cmd.Context(), cfg.Audit.AfdocsCmd, target, append(extra, afArgs...), 10*time.Minute)
				if err != nil {
					if mode == "on" {
						return err
					}
					note = "afdocs failed, score uses docsbot checks only: " + firstLine(err.Error())
					af = nil
				}
			}
			rep := readiness.Combine(s.Source, checks, af, note, cfg.Audit.AfdocsWeight)
			label(rep, s, sample, af)

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
	c.Flags().StringVar(&afMode, "afdocs", "auto", "run afdocs: auto (when npx is available), on (required), off")
	c.Flags().StringVar(&site, "site", "", "live URL to run afdocs against (for a local repo)")
	c.Flags().IntVar(&failUnder, "fail-under", 0, "exit 1 if the overall score is below this")
	c.Flags().IntVar(&sampleN, "sample", 50, "pages sent to afdocs' page-level checks, spread across sections (0: every page)")
	c.Flags().StringArrayVar(&afArgs, "afdocs-arg", nil, "extra argument passed to afdocs (repeatable)")
	return c
}

// afdocsSampleArgs passes docsbot's sample to afdocs, so afdocs checks the
// same pages on every run instead of a random selection.
func afdocsSampleArgs(sm readiness.Sample) []string {
	var abs []string
	for _, u := range sm.URLs {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			abs = append(abs, u)
		}
	}
	if len(abs) == 0 {
		// local repo without URLs: let afdocs discover, but deterministically
		return []string{"--sampling", "deterministic", "--max-links", fmt.Sprint(max(sm.Size, 1))}
	}
	return []string{"--urls", strings.Join(abs, ","), "--max-links", fmt.Sprint(len(abs))}
}

// label sets audit or indicative, and collects sampling warnings.
func label(r *readiness.Report, s *crawl.Site, sm readiness.Sample, af *readiness.AfdocsResult) {
	r.Sample = &sm
	r.Warnings = append(r.Warnings, sm.Warnings...)
	r.Mode = readiness.Audit
	if s.Mode == crawl.Live || af != nil {
		r.Mode = sm.Mode
	}
	if af != nil && af.TestedPages > 0 && af.TestedPages < sm.Size {
		r.Mode = readiness.Indicative
		r.Warnings = append(r.Warnings, fmt.Sprintf("afdocs tested %d of the %d sampled pages.", af.TestedPages, sm.Size))
	}
	if s.Mode == crawl.Local && af == nil {
		r.Warnings = append(r.Warnings, "Local checks only: delivery checks (content negotiation, Markdown URLs, rendering, status codes) need the live site (--site).")
	}
	sort.Strings(r.Warnings)
}

func printAudit(w io.Writer, r *readiness.Report) {
	label := r.Mode
	if r.Sample != nil && r.Mode == readiness.Indicative {
		label = fmt.Sprintf("indicative: %d of %d discovered pages sampled", r.Sample.Size, r.Sample.Discovered)
	} else if r.Sample != nil {
		label = fmt.Sprintf("audit: all %d pages", r.Sample.Discovered)
	}
	fmt.Fprintf(w, "Agent readiness for %s (%s)\n\n", r.Source, label)
	if r.Capped != nil {
		fmt.Fprintf(w, "  Blocking issue  %s\n", r.Capped.Reason)
		fmt.Fprintf(w, "                  afdocs caps its score at %d while %s fails; fix this first.\n", r.Capped.Cap, r.Capped.CheckID)
		fmt.Fprintf(w, "  Score           %d / 100 (%s), before the cap\n", r.Score, r.Grade)
	} else {
		fmt.Fprintf(w, "  Score           %d / 100 (%s)\n", r.Score, r.Grade)
	}
	if r.AfdocsScore != nil {
		fmt.Fprintf(w, "  afdocs          %d / 100 (%s)\n", *r.AfdocsScore, r.AfdocsGrade)
	}
	fmt.Fprintf(w, "  docsbot checks  %d / 100\n", r.GraphScore)
	if r.AfdocsNote != "" {
		fmt.Fprintf(w, "  Note: %s\n", r.AfdocsNote)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "  Warning: %s\n", warn)
	}
	if r.Mode == readiness.Indicative {
		fmt.Fprintln(w, "  Indicative results come from a sample: use them to find problems, not to compare sites or releases.")
	}

	fmt.Fprintln(w, "\ndocsbot checks")
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

func newAgentReadyGenerate() *cobra.Command {
	var opt agentready.Options
	var noDirective bool
	c := &cobra.Command{
		Use:   "generate [DIR]",
		Short: "Write llms.txt, llms-full.txt, Markdown copies of pages and discovery snippets",
		Long: `Generates the files that help AI agents use the docs, into an output folder
(default build/agent-ready). The docs source is never changed, and nothing is
published: copy the files into the site build yourself.

  llms.txt                index from the navigation and page descriptions
  llms-full.txt           every page in one Markdown file
  <page>.md               a Markdown copy of each page at its URL with .md added:
                          front matter removed, includes inlined, internal links
                          pointed at the .md copies
  copy-as-markdown.js     a "Copy page as Markdown" button
  discovery-snippet.html  template additions so agents find llms.txt and the copies

Pages without a description are listed: their llms.txt entries have no summary.
Shortcodes and JSX are kept as written and listed.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot agent-ready generate docs --base-url https://docs.example.com
  docsbot agent-ready generate . --out site-extra --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			cfg, err := loadConfig(firstArg(args))
			if err != nil {
				return err
			}
			source := firstArg(args)
			if source == "" {
				source = "."
			}
			if isURL(source) {
				return &exitError{code: ExitError, msg: "error: generate reads a local docs folder, not a URL"}
			}
			s, err := loadSite(cmd.Context(), cfg, newClient(cfg), source)
			if err != nil {
				return err
			}
			opt.Directive = !noDirective
			res, err := agentready.Generate(s, opt)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				return writeJSON(w, res)
			}
			verb := "Wrote"
			if opt.DryRun {
				verb = "Would write"
			}
			fmt.Fprintf(w, "%s %d files to %s: %d page copies, %d llms.txt entries (%d under Optional).\n", verb, len(res.Files), res.Out, res.Pages, res.InLLMSTxt, len(res.Optional))
			fmt.Fprintf(w, "Rewrote %d internal links to the .md copies and inlined %d includes.\n", res.Rewritten, res.Inlined)
			for _, wn := range res.Warnings {
				fmt.Fprintf(w, "Warning: %s\n", wn)
			}
			list := func(title string, items []string) {
				if len(items) == 0 {
					return
				}
				fmt.Fprintf(w, "\n%s (%d):\n", title, len(items))
				for i, it := range items {
					if i == 10 {
						fmt.Fprintf(w, "  … and %d more (-f json lists all)\n", len(items)-10)
						break
					}
					fmt.Fprintf(w, "  %s\n", it)
				}
			}
			notes := func(ns []agentready.Note) []string {
				var out []string
				for _, n := range ns {
					out = append(out, n.File+": "+n.Reason)
				}
				return out
			}
			list("Pages without a description (llms.txt entry has no summary)", res.NoDesc)
			list("Pages not in the navigation (listed under Optional)", res.Optional)
			list("Skipped", notes(res.Skipped))
			list("Markup kept as written (agents see it raw)", notes(res.KeptMarkup))
			return nil
		},
	}
	c.Flags().StringVar(&opt.Out, "out", "build/agent-ready", "output folder (must be outside the content root)")
	c.Flags().StringVar(&opt.Title, "title", "", "llms.txt title (default: the home page title)")
	c.Flags().StringVar(&opt.Summary, "summary", "", "llms.txt summary (default: the home page description)")
	c.Flags().BoolVar(&noDirective, "no-directive", false, "don't start each Markdown copy with a pointer to llms.txt")
	c.Flags().BoolVar(&opt.DryRun, "dry-run", false, "list what would be written without writing")
	return c
}
