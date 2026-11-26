package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kakabisht/Docs-toolkit/internal/audit"
	"github.com/kakabisht/Docs-toolkit/internal/config"
	"github.com/kakabisht/Docs-toolkit/internal/doctype"
	"github.com/kakabisht/Docs-toolkit/internal/kg"
	"github.com/kakabisht/Docs-toolkit/internal/vocab"
)

type auditFlags struct {
	tasks, queryLog string
	failUnder       int
	items           int
}

func newAuditCmd() *cobra.Command {
	var f auditFlags
	c := &cobra.Command{
		Use:   "audit [SOURCE]",
		Short: "Score docs structure and findability, with a fix-first list",
		Long: `Audits a docs set and writes a Markdown report (or JSON with -f json).

  structure    content types, template conformance, titles and headings, page
               shape, metadata, duplication, terminology, navigation depth and
               breadth, ownership and freshness, URL quality
  findability  click paths, search (local BM25 index; tasks file and query log),
               metadata for discovery, cross-linking, and an agent summary

Every score says how it is computed; the weights are listed in the report and
can be changed in the config (audit.weights). Scores are indicators for
deciding what to fix first, not measurements of reader success.

Exit code 1 when the score is below --fail-under.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot audit docs
  docsbot audit docs -o audit.md
  docsbot audit findability docs --tasks tasks.yaml --query-log searches.csv
  docsbot audit structure docs -f json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAudit(cmd, args, f, nil)
		},
	}
	addAuditFlags(c, &f)
	structure := &cobra.Command{
		Use:   "structure [SOURCE]",
		Short: "Structure only",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAudit(cmd, args, f, []string{audit.Structure})
		},
	}
	findability := &cobra.Command{
		Use:   "findability [SOURCE]",
		Short: "Findability only: click paths, search tasks, query log, discovery metadata, cross-links",
		Long: `Findability only. A tasks file lists reader goals, the searches they might
type, and the pages that answer them:

  tasks:
    - task: Install on Windows
      queries: [install windows, windows installer]
      expect: [install/windows.md]      # route, file path or URL; any one answers it

A query log is a CSV of real searches with a query column and an optional count
column. Without a tasks file, search is scored with a proxy (each page searched
by its own title) and the report says so.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAudit(cmd, args, f, []string{audit.Findability})
		},
	}
	architecture := &cobra.Command{
		Use:   "architecture",
		Short: "Information architecture review (deferred until a rubric is supplied)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return &exitError{code: ExitError, msg: "audit architecture is deferred: it needs a rubric (what good looks like for this docs set) before it can score anything. See docs/decisions.md."}
		},
	}
	addAuditFlags(structure, &f)
	addAuditFlags(findability, &f)
	c.AddCommand(structure, findability, architecture)
	return c
}

func addAuditFlags(c *cobra.Command, f *auditFlags) {
	c.Flags().StringVar(&f.tasks, "tasks", "", "findability tasks file (YAML)")
	c.Flags().StringVar(&f.queryLog, "query-log", "", "search query log (CSV)")
	c.Flags().IntVar(&f.failUnder, "fail-under", 0, "exit 1 when the score is below this")
	c.Flags().IntVar(&f.items, "items", 10, "items listed per dimension in the Markdown report (0 for all)")
}

func auditOptions(cfg config.Config, f auditFlags) (audit.Options, error) {
	ac := cfg.Audit
	opt := audit.Options{
		ContentTypes: doctype.Overrides(cfg.Graph.ContentTypes), RequiredMeta: ac.RequiredMeta, RecommendedMeta: ac.RecommendedMeta,
		StaleDays: ac.StaleDays, MaxDepth: ac.MaxClicks, MaxNavChildren: ac.MaxNavChildren, MaxNavLevels: ac.MaxNavLevels,
		ShortWords: ac.ShortWords, LongWords: ac.LongWords, MaxSectionWords: ac.MaxSectionWords, Weights: ac.Weights, TopK: ac.TopK,
	}
	if ac.Templates != nil {
		opt.Templates = map[string]audit.Template{}
		for k, t := range ac.Templates {
			opt.Templates[doctype.Normalize(k)] = audit.Template(t)
		}
	}
	for id := range ac.Weights {
		if _, ok := audit.DefaultWeights[id]; !ok {
			return opt, fmt.Errorf("audit.weights: unknown dimension %q", id)
		}
	}
	var err error
	if opt.Vocab, err = vocab.Load(cfg.Vocabulary); err != nil {
		return opt, fmt.Errorf("vocabulary: %w", err)
	}
	if p := firstNonEmpty(f.tasks, ac.Tasks); p != "" {
		if opt.Tasks, err = audit.LoadTasks(p); err != nil {
			return opt, fmt.Errorf("tasks: %w", err)
		}
	}
	if p := firstNonEmpty(f.queryLog, ac.QueryLog); p != "" {
		if opt.Queries, err = audit.LoadQueryLog(p); err != nil {
			return opt, fmt.Errorf("query log: %w", err)
		}
	}
	return opt, nil
}

func runAudit(cmd *cobra.Command, args []string, f auditFlags, parts []string) error {
	if gf.format == "md" || gf.format == "markdown" {
		gf.format = "text"
	}
	if err := textOrJSON(); err != nil {
		return err
	}
	cfg, err := loadConfig(firstArg(args))
	if err != nil {
		return err
	}
	opt, err := auditOptions(cfg, f)
	if err != nil {
		return &exitError{code: ExitError, msg: "error: " + err.Error()}
	}
	opt.Parts = parts
	source := firstArg(args)
	if source == "" {
		source = "."
	}
	s, err := loadSite(cmd.Context(), cfg, newClient(cfg), source)
	if err != nil {
		return err
	}
	kopt := kgOptions(cfg, opt.Vocab)
	if kopt.IgnoreHrefs, err = compileAll(cfg.Links.IgnoreHrefs, "links.ignore_hrefs"); err != nil {
		return err
	}
	opt.Graph = kg.Build(s, kopt)
	rep := audit.Run(s, opt)
	w, done, err := out()
	if err != nil {
		return err
	}
	defer done()
	if gf.format == "json" {
		err = writeJSON(w, rep)
	} else {
		rep.Markdown(w, f.items)
	}
	if err != nil {
		return err
	}
	threshold := f.failUnder
	if threshold == 0 {
		threshold = cfg.Audit.FailUnder
	}
	if threshold > 0 && rep.Score < float64(threshold) {
		return findings("score %.0f is below %d", rep.Score, threshold)
	}
	return nil
}
