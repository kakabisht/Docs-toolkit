package cmd

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kakabisht/Docs-toolkit/internal/check"
	"github.com/kakabisht/Docs-toolkit/internal/config"
	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/finding"
	"github.com/kakabisht/Docs-toolkit/internal/vocab"
)

type checkFlags struct {
	only        []string
	offline     bool
	changedOnly bool
	base        string
	failLevel   string
	rules       []string
	extAnchors  bool
}

func newCheckCmd() *cobra.Command {
	var f checkFlags
	c := &cobra.Command{
		Use:   "check [SOURCE]",
		Short: "Validate links, images, navigation, structure and front matter",
		Long: `Validates a docs set and reports findings with file, line and a hint.
It never edits files.

SOURCE is a docs directory (default "."), a single file, or a site URL.

Rule groups (--only): links, images, structure, metadata, drafts.
Run "docsbot rules" to list every rule and its default severity.

Exit codes: 0 clean or only findings below --fail-level, 1 findings at or
above it, 2 usage or internal error.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot check
  docsbot check docs --only links --offline
  docsbot check --changed-only --base origin/main -f github
  docsbot check docs/guide/install.md
  docsbot check -f sarif -o docsbot.sarif`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd, args, f)
		},
	}
	addCheckFlags(c, &f, true)
	return c
}

func newLinksCmd() *cobra.Command {
	var f checkFlags
	c := &cobra.Command{
		Use:   "links [SOURCE]",
		Short: "Check links and images only (shortcut for check --only links,images)",
		Args:  cobra.MaximumNArgs(1),
		Example: `  docsbot links docs
  docsbot links docs --offline
  docsbot links https://docs.example.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.only = []string{"links", "images"}
			return runCheck(cmd, args, f)
		},
	}
	addCheckFlags(c, &f, false)
	return c
}

func addCheckFlags(c *cobra.Command, f *checkFlags, withOnly bool) {
	if withOnly {
		c.Flags().StringSliceVar(&f.only, "only", nil, "rule groups to run: links, images, structure, metadata, drafts")
	}
	c.Flags().BoolVar(&f.offline, "offline", false, "don't fetch external URLs")
	c.Flags().BoolVar(&f.changedOnly, "changed-only", false, "report only on files changed against --base (for pull requests)")
	c.Flags().StringVar(&f.base, "base", "origin/main", "git ref to compare against with --changed-only")
	c.Flags().StringVar(&f.failLevel, "fail-level", "", "lowest severity that exits 1: error (default), warning, info, off")
	c.Flags().StringArrayVar(&f.rules, "rule", nil, "override a rule's severity, e.g. --rule page-orphan=off (repeatable)")
	c.Flags().BoolVar(&f.extAnchors, "external-anchors", false, "also check #fragments on external pages")
}

func runCheck(cmd *cobra.Command, args []string, f checkFlags) error {
	if err := findingFormat(); err != nil {
		return err
	}
	cfg, err := loadConfig(firstArg(args))
	if err != nil {
		return err
	}
	source := "."
	if len(args) > 0 {
		source = args[0]
	}
	opt, err := checkOptions(cfg, f)
	if err != nil {
		return err
	}
	client := newClient(cfg)
	opt.Client = client
	s, err := loadSite(cmd.Context(), cfg, client, source)
	if err != nil {
		return err
	}
	if f.changedOnly {
		if s.Mode != crawl.Local {
			return fmt.Errorf("--changed-only needs a local directory")
		}
		changed, err := crawl.ChangedFiles(s.Root, f.base)
		if err != nil {
			return fmt.Errorf("--changed-only: git diff against %s failed (fetch the base ref first, e.g. actions/checkout with fetch-depth: 0): %w", f.base, err)
		}
		opt.Filter = changed
		progress("checking %d changed files against %s", len(changed), f.base)
	}
	if !opt.Offline && !gf.quiet {
		opt.Progress = throttle(func(d, t int) { progress("  checked %d/%d external URLs", d, t) })
	}
	fs := check.Run(cmd.Context(), s, opt)
	if s.Mode == crawl.Local {
		// paths relative to the repository root; print them relative to the working directory when inside it
		for i := range fs {
			fs[i].File = displayPath(s.Root, fs[i].File)
		}
	}
	w, done, err := out()
	if err != nil {
		return err
	}
	defer done()
	if err := finding.Write(w, gf.format, fs, "docsbot", Version); err != nil {
		return err
	}
	level := finding.Error
	if lv := firstNonEmpty(f.failLevel, cfg.Check.FailLevel); lv != "" {
		if level, err = finding.ParseSeverity(lv); err != nil {
			return err
		}
	}
	if finding.Fails(fs, level) {
		return &exitError{code: ExitFindings}
	}
	return nil
}

func checkOptions(cfg config.Config, f checkFlags) (check.Options, error) {
	opt := check.Options{Offline: f.offline || !cfg.CheckExternal(), ExternalAnchor: f.extAnchors || cfg.Links.ExternalAnchors}
	pol := finding.Policy{Overrides: map[string]finding.Severity{}}
	for id, sev := range cfg.Check.Rules {
		s, err := finding.ParseSeverity(sev)
		if err != nil {
			return opt, fmt.Errorf("check.rules.%s: %w", id, err)
		}
		if _, ok := finding.Rules[id]; !ok {
			return opt, fmt.Errorf("check.rules: unknown rule %q (see `docsbot rules`)", id)
		}
		pol.Overrides[id] = s
	}
	for _, r := range f.rules {
		id, sev, ok := strings.Cut(r, "=")
		if !ok {
			return opt, fmt.Errorf("--rule %q: want RULE=SEVERITY", r)
		}
		s, err := finding.ParseSeverity(sev)
		if err != nil {
			return opt, err
		}
		if _, ok := finding.Rules[id]; !ok {
			return opt, fmt.Errorf("--rule: unknown rule %q (see `docsbot rules`)", id)
		}
		pol.Overrides[id] = s
	}
	if len(f.only) > 0 {
		pol.Only = map[finding.Category]bool{}
		for _, o := range f.only {
			cat := finding.Category(strings.TrimSpace(o))
			switch cat {
			case finding.Links, finding.Images, finding.Structure, finding.Metadata, finding.Drafts:
				pol.Only[cat] = true
			default:
				return opt, fmt.Errorf("--only %q: want links, images, structure, metadata or drafts", o)
			}
		}
	}
	opt.Policy = pol
	var err error
	if opt.IgnoreURLs, err = compileAll(cfg.Links.ExcludeURLs, "links.exclude_urls"); err != nil {
		return opt, err
	}
	if opt.IgnoreHrefs, err = compileAll(cfg.Links.IgnoreHrefs, "links.ignore_hrefs"); err != nil {
		return opt, err
	}
	if len(cfg.Check.TodoPatterns) > 0 {
		if opt.TodoPatterns, err = compileAll(cfg.Check.TodoPatterns, "check.todo_patterns"); err != nil {
			return opt, err
		}
	}
	if cfg.Check.Schema != "" {
		if opt.Schema, err = check.CompileSchema(cfg.Check.Schema); err != nil {
			return opt, fmt.Errorf("check.schema: %w", err)
		}
	}
	if opt.Vocab, err = vocab.Load(cfg.Vocabulary); err != nil {
		return opt, fmt.Errorf("vocabulary: %w", err)
	}
	for _, e := range cfg.Check.EntryPages {
		opt.EntryKeys = append(opt.EntryKeys, crawl.RouteKey(e))
	}
	return opt, nil
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

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// displayPath shows repo-relative paths relative to the working directory.
func displayPath(root, rel string) string {
	if rel == "" || strings.Contains(rel, "://") {
		return rel
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if wd, err := filepath.Abs("."); err == nil {
		if r, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return rel
}

func newRulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rules",
		Short: "List every check rule with its group and default severity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if gf.format == "json" {
				var rs []finding.Rule
				for _, id := range finding.RuleIDs() {
					rs = append(rs, finding.Rules[id])
				}
				return writeJSON(w, rs)
			}
			for _, id := range finding.RuleIDs() {
				r := finding.Rules[id]
				fmt.Fprintf(w, "%-28s %-10s %-8s %s\n", id, r.Category, r.Severity, r.Summary)
			}
			return nil
		},
	}
}
