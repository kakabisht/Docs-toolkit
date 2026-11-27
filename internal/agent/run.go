// Package agent runs docsbot's documentation agent: find what the code
// defines that the docs miss, fetch product context over MCP, draft the
// missing or out-of-date pages with a citation for every statement, validate
// the examples, links, references and facts, and score the result against a
// gate. Nothing is published until a person has verified every claim and
// approved the draft (Approve, then Publish).
//
// The plan is fixed, not chosen by a model: every draft goes through the same
// validation and evaluation, and no step can skip the human approval.
// A model, when configured, writes the prose and can act as a judge; without
// one the agent still runs end to end, with extractive drafts.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/kakabisht/Docs-toolkit/internal/gaps"
	"github.com/kakabisht/Docs-toolkit/internal/mcpctx"
)

// Files written into each draft folder, next to draft write's three.
const (
	MetaFile     = "agent.json"
	EvalFile     = "eval.json"
	EvalReport   = "eval.md"
	ChangesFile  = "proposed-changes.md"
	ApprovalFile = "approval.json"
	RunFile      = "run.json"
	RunReport    = "report.md"
)

// Options configures one run.
type Options struct {
	Root     string // repository root
	Content  string // content root, relative to Root
	Pages    []gaps.Page
	Docs     []draft.Source // the repository's pages as passages
	Evidence *draft.Evidence
	Entities []draft.Entity
	Since    string // git ref for change detection
	Contexts []*mcpctx.Context
	Provider draft.Provider // writes prose; nil for extractive drafts
	Judge    bool           // also score drafts with the provider
	Out      string         // folder for the run and its drafts; never inside the docs
	Max      int            // most groups to draft (default 3)
	Only     []string       // group ids to draft, instead of the top Max
	Force    bool           // replace existing drafts
	Gate     Gate
	External bool // check external links over HTTP
	Date     string
	Version  string
	Progress func(string)
}

// Meta is agent.json: what a draft was made from, so eval and approve can
// work on the folder later.
type Meta struct {
	Root    string        `json:"root"`
	Content string        `json:"content"`
	Since   string        `json:"since,omitempty"`
	Group   gaps.Group    `json:"group"`
	Context []mcpctx.Call `json:"context_calls,omitempty"`
	Date    string        `json:"date,omitempty"`
}

// Result is one drafted group.
type Result struct {
	Group  gaps.Group `json:"group"`
	Dir    string     `json:"dir"`
	Eval   *Eval      `json:"eval,omitempty"`
	Error  string     `json:"error,omitempty"`
	Claims int        `json:"claims"`
}

// Run is run.json.
type Run struct {
	Date     string        `json:"date,omitempty"`
	Root     string        `json:"root"`
	Since    string        `json:"since,omitempty"`
	Gaps     *gaps.Report  `json:"gaps"`
	Context  []string      `json:"context_servers,omitempty"`
	Model    string        `json:"model"`
	Drafts   []Result      `json:"drafts"`
	Skipped  []string      `json:"groups_not_drafted,omitempty"`
	Calls    []mcpctx.Call `json:"context_calls,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return s
}

// Execute runs all five steps and writes the run report.
func Execute(ctx context.Context, opt Options) (*Run, error) {
	say := opt.Progress
	if say == nil {
		say = func(string) {}
	}
	if opt.Max == 0 {
		opt.Max = 3
	}
	if opt.Out == "" {
		return nil, fmt.Errorf("an output folder is required")
	}
	if inside(opt.Out, filepath.Join(opt.Root, opt.Content)) {
		return nil, fmt.Errorf("output folder %s is inside the docs (%s); the agent never writes there before approval", opt.Out, filepath.Join(opt.Root, opt.Content))
	}
	run := &Run{Date: opt.Date, Root: opt.Root, Since: opt.Since, Model: "none (extractive drafts)"}
	if opt.Provider != nil {
		run.Model = opt.Provider.Name()
	}
	for _, c := range opt.Contexts {
		run.Context = append(run.Context, c.Name()+" ("+c.Tool()+")")
	}

	// 1. gaps
	say("1/5 reading the code and comparing it with the docs")
	rep, err := gaps.Find(gaps.Options{Root: opt.Root, Content: opt.Content, Since: opt.Since, Pages: opt.Pages})
	if err != nil {
		return nil, err
	}
	run.Gaps = rep
	say(fmt.Sprintf("    %d surface items, %d documented, %d gaps in %d groups", rep.Items, rep.Covered, len(rep.Gaps), len(rep.Groups)))
	picked := pick(rep.Groups, opt)
	for _, g := range rep.Groups {
		if !containsGroup(picked, g.ID) {
			run.Skipped = append(run.Skipped, g.ID+" "+g.Topic)
		}
	}
	if len(opt.Contexts) == 0 {
		run.Warnings = append(run.Warnings, "no MCP context server configured: drafts use the repository only (set agent.mcp in docsbot.yml)")
	}
	if opt.Provider == nil {
		run.Warnings = append(run.Warnings, "no model provider: drafts are extractive and must be rewritten before approval")
	}

	for _, g := range picked {
		dir := filepath.Join(opt.Out, strings.ToLower(g.ID)+"-"+slug(g.Topic))
		res := Result{Group: g, Dir: dir}
		if err := draftGroup(ctx, opt, g, dir, &res, say); err != nil {
			res.Error = err.Error()
			say("    " + g.ID + ": " + err.Error())
		}
		run.Drafts = append(run.Drafts, res)
	}
	for _, c := range opt.Contexts {
		run.Calls = append(run.Calls, c.Log...)
	}
	if err := os.MkdirAll(opt.Out, 0o755); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(opt.Out, RunFile), run); err != nil {
		return nil, err
	}
	return run, os.WriteFile(filepath.Join(opt.Out, RunReport), []byte(runReport(run)), 0o644)
}

func pick(groups []gaps.Group, opt Options) []gaps.Group {
	if len(opt.Only) > 0 {
		var out []gaps.Group
		for _, g := range groups {
			for _, id := range opt.Only {
				if strings.EqualFold(strings.TrimSpace(id), g.ID) {
					out = append(out, g)
				}
			}
		}
		return out
	}
	if len(groups) > opt.Max {
		return groups[:opt.Max]
	}
	return groups
}

func containsGroup(gs []gaps.Group, id string) bool {
	for _, g := range gs {
		if g.ID == id {
			return true
		}
	}
	return false
}

func draftGroup(ctx context.Context, opt Options, g gaps.Group, dir string, res *Result, say func(string)) error {
	say(fmt.Sprintf("%s  %s (%s %s, %d items)", g.ID, g.Topic, g.Mode, g.Target, len(g.Gaps)))

	// 2. context over MCP. The draft cites only passages about this group's
	// items: what the code defines, what the context servers return, and the
	// repository's own pages where they name an item.
	srcs := CodeSources(g, opt.Since)
	// the docs' own text about a changed or removed item is what is out of
	// date, so pages are sources only for items they don't cover yet
	undoc := g
	undoc.Gaps = nil
	for _, gap := range g.Gaps {
		if gap.Type == "undocumented" {
			undoc.Gaps = append(undoc.Gaps, gap)
		}
	}
	if len(undoc.Gaps) > 0 {
		srcs = append(srcs, Relevant(undoc, opt.Docs, 0)...)
	}
	var calls []mcpctx.Call
	seen := map[string]bool{}
	for _, c := range opt.Contexts {
		before := len(c.Log)
		var ps []draft.Source
		for _, q := range Queries(g) {
			say("    2/5 asking " + c.Name() + "/" + c.Tool() + ": " + q)
			got, err := c.Retrieve(ctx, q)
			if err != nil {
				say("        " + err.Error() + " (drafting without it)")
			}
			for _, p := range got {
				if k := p.File + "\x00" + p.Text; !seen[k] {
					seen[k] = true
					ps = append(ps, p)
				}
			}
		}
		srcs = append(srcs, Relevant(g, ps, 2)...)
		calls = append(calls, c.Log[before:]...)
	}

	// 3. draft
	say("    3/5 drafting")
	dopt := draft.Options{
		Docs: srcs, DocsRoot: filepath.Join(opt.Root, opt.Content), Evidence: opt.Evidence, Entities: opt.Entities,
		Topic: g.Topic, TemplateText: Template(g), Out: dir, Force: opt.Force, Provider: opt.Provider, Date: opt.Date,
	}
	dres, err := draft.Generate(ctx, dopt)
	if err != nil {
		return err
	}
	res.Claims = total(dres.Counts)
	meta := Meta{Root: opt.Root, Content: opt.Content, Since: opt.Since, Group: g, Context: calls, Date: opt.Date}
	if err := writeJSON(filepath.Join(dir, MetaFile), meta); err != nil {
		return err
	}
	if g.Mode == "update" {
		if err := os.WriteFile(filepath.Join(dir, ChangesFile), []byte(ProposedChanges(g, opt.Since)), 0o644); err != nil {
			return err
		}
	}

	// 4 and 5. validate and evaluate
	say("    4/5 validating examples, links, references and facts")
	ev, err := Evaluate(ctx, EvalOptions{Dir: dir, Meta: meta, Evidence: opt.Evidence, External: opt.External,
		Gate: opt.Gate, Judge: opt.Judge, Provider: opt.Provider, Date: opt.Date})
	if err != nil {
		return err
	}
	res.Eval = ev
	verdict := "blocked"
	if ev.Gate.Passed {
		verdict = "ready for human review"
	}
	say(fmt.Sprintf("    5/5 score %d/100: %s", ev.Score, verdict))
	return nil
}

func total(m map[string]int) int {
	return m[draft.Verified] + m[draft.Unverified] + m[draft.NeedsChange] + m[draft.Rejected]
}

// Relevant keeps the passages that name one of the group's items. When none
// does, the first keep passages are kept anyway (a context server has
// already ranked them for the query).
func Relevant(g gaps.Group, ps []draft.Source, keep int) []draft.Source {
	var out []draft.Source
	for _, p := range ps {
		page := []gaps.Page{{File: p.File, Text: p.Text}}
		for _, gap := range g.Gaps {
			if len(gaps.Mentions(page, gap.Item)) > 0 {
				out = append(out, p)
				break
			}
		}
	}
	// keep the code blocks that came with a kept passage from the same source
	kept := map[string]bool{}
	for _, p := range out {
		kept[p.File] = true
	}
	for _, p := range ps {
		if p.Code && p.Kind == "mcp" && kept[p.File] && !containsSource(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 && keep > 0 {
		out = ps[:min(keep, len(ps))]
	}
	return out
}

func containsSource(ss []draft.Source, p draft.Source) bool {
	for _, s := range ss {
		if s.File == p.File && s.Text == p.Text {
			return true
		}
	}
	return false
}

// Queries are what the agent asks each context server for a group: the
// topic with the item names, then examples of them.
func Queries(g gaps.Group) []string {
	q := Query(g)
	return []string{q, strings.TrimPrefix(q, g.Topic+": ") + " example"}
}

// Query is the main question for a group.
func Query(g gaps.Group) string {
	names := make([]string, 0, len(g.Gaps))
	for _, gap := range g.Gaps {
		names = append(names, gap.Item.Name)
	}
	if len(names) > 8 {
		names = names[:8]
	}
	return g.Topic + ": " + strings.Join(names, " ")
}

// Template is the draft template for a group, with the item names as search
// words so each section finds the passages about them.
func Template(g gaps.Group) string {
	names := make([]string, 0, len(g.Gaps))
	for _, gap := range g.Gaps {
		names = append(names, strings.TrimLeft(gap.Item.Name, "-"))
	}
	q := strings.Join(names, " ")
	kind := map[string]string{"flag": "flags", "env": "environment variables", "endpoint": "endpoints", "config": "settings"}[g.Gaps[0].Item.Kind]
	if g.Mode == "update" && g.Gaps[0].Type != "undocumented" {
		return fmt.Sprintf(`## What changed
<!-- The code changes that make %[1]s out of date. query: %[2]s changed removed default renamed -->

## Updated reference
<!-- The current behaviour of each item, with its default. passages: prose query: %[2]s default -->

## Examples
<!-- Examples that work with the current code. passages: code query: %[2]s example usage -->
`, g.Target, q)
	}
	return fmt.Sprintf(`## Reference
<!-- Each of these %[1]s with what it does and its default. passages: prose query: %[2]s default -->

## Examples
<!-- A working example that uses them. passages: code query: %[2]s example usage -->

## Before you change them
<!-- Constraints, caveats and related settings. passages: prose query: %[2]s note warning limit caution set -->
`, kind, q)
}

// CodeSources turns each gap into a passage stating what the code defines,
// so the draft can cite the code itself.
func CodeSources(g gaps.Group, since string) []draft.Source {
	var out []draft.Source
	for _, gap := range g.Gaps {
		it := gap.Item
		var b strings.Builder
		switch it.Kind {
		case "flag":
			fmt.Fprintf(&b, "The `%s` flag is defined in %s", it.Name, it.File)
		case "env":
			fmt.Fprintf(&b, "The `%s` environment variable is read in %s", it.Name, it.File)
		case "endpoint":
			fmt.Fprintf(&b, "The endpoint `%s` is defined in %s", it.Name, it.File)
		default:
			fmt.Fprintf(&b, "The setting `%s` is defined in %s", it.Name, it.File)
		}
		if it.Default != "" {
			fmt.Fprintf(&b, " with the default `%s`", it.Default)
		}
		b.WriteString(".")
		if it.Help != "" {
			fmt.Fprintf(&b, " Its description in the code reads: “%s”.", strings.TrimSuffix(it.Help, "."))
		}
		src := draft.Source{Kind: "code", File: it.File, Line: it.Line, Heading: it.Name, Text: b.String()}
		if gap.Change != nil {
			ref := since
			switch gap.Change.Status {
			case "removed":
				src.Text = fmt.Sprintf("The `%s` %s was removed from %s since %s, but the docs still mention it.", it.Name, kindWord(it.Kind), gap.Change.File, ref)
				src.File = "git diff " + ref + ": " + gap.Change.File
			case "changed":
				src.Text += fmt.Sprintf(" Since %s its default changed from `%s` to `%s`.", ref, gap.Change.OldDefault, it.Default)
			case "added":
				src.Text += fmt.Sprintf(" It was added since %s.", ref)
			}
		}
		out = append(out, src)
	}
	return out
}

func kindWord(k string) string {
	return map[string]string{"flag": "flag", "env": "environment variable", "endpoint": "endpoint", "config": "setting"}[k]
}

// ProposedChanges lists, for an update group, each doc line to change: an
// outdated default with the line rewritten, a removed item with the line to
// delete or rewrite, and undocumented items to add to the page.
func ProposedChanges(g gaps.Group, since string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Proposed changes to %s\n\n", g.Target)
	b.WriteString("The agent does not edit existing pages. Apply these by hand after review, then run `docsbot check`.\n\n")
	var add []gaps.Gap
	for _, gap := range g.Gaps {
		it := gap.Item
		switch gap.Type {
		case "outdated":
			for _, m := range gap.Mentions {
				fmt.Fprintf(&b, "## %s:%d — default of `%s` changed\n\n", m.File, m.Line, it.Name)
				fmt.Fprintf(&b, "Since %s the code (%s:%d) gives `%s`, not `%s`.\n\n", since, it.File, it.Line, it.Default, gap.Change.OldDefault)
				fmt.Fprintf(&b, "```diff\n- %s\n+ %s\n```\n\n", m.Text, replaceWord(m.Text, gap.Change.OldDefault, it.Default))
			}
		case "stale":
			for _, m := range gap.Mentions {
				fmt.Fprintf(&b, "## %s:%d — `%s` no longer exists\n\n", m.File, m.Line, it.Name)
				fmt.Fprintf(&b, "It was removed from %s since %s. Delete or rewrite this line:\n\n```diff\n- %s\n```\n\n", gap.Change.File, since, m.Text)
			}
		default:
			add = append(add, gap)
		}
	}
	if len(add) > 0 {
		fmt.Fprintf(&b, "## Add to %s\n\n", g.Target)
		b.WriteString("These are defined in the code but not on the page; the draft has text for them.\n\n| Item | Default | Defined in |\n| --- | --- | --- |\n")
		for _, gap := range add {
			fmt.Fprintf(&b, "| `%s` | %s | %s:%d |\n", gap.Item.Name, code(gap.Item.Default), gap.Item.File, gap.Item.Line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func code(s string) string {
	if s == "" {
		return "—"
	}
	return "`" + strings.ReplaceAll(s, "|", `\|`) + "`"
}

func replaceWord(text, old, new string) string {
	if old == "" {
		return text
	}
	re := regexp.MustCompile(`(^|[^\w.-])` + regexp.QuoteMeta(old) + `([^\w-]|$)`)
	return re.ReplaceAllString(text, "${1}"+strings.ReplaceAll(new, "$", "$$")+"${2}")
}

func inside(p, dir string) bool {
	pa, _ := filepath.Abs(p)
	da, _ := filepath.Abs(dir)
	return pa == da || strings.HasPrefix(pa, da+string(filepath.Separator))
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func runReport(r *Run) string {
	var b strings.Builder
	b.WriteString("# Documentation agent run\n\n")
	if r.Date != "" {
		fmt.Fprintf(&b, "Date: %s. ", r.Date)
	}
	fmt.Fprintf(&b, "Repository: %s. Model: %s.", r.Root, r.Model)
	if r.Since != "" {
		fmt.Fprintf(&b, " Changes since: %s.", r.Since)
	}
	b.WriteString("\n\n")
	if len(r.Context) > 0 {
		fmt.Fprintf(&b, "Context servers (MCP): %s.\n\n", strings.Join(r.Context, ", "))
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "> **Note:** %s\n\n", w)
	}
	g := r.Gaps
	b.WriteString("## 1. Gaps\n\n")
	kinds := make([]string, 0, len(g.ByKind))
	for _, k := range gaps.Kinds {
		if g.ByKind[k] > 0 {
			kinds = append(kinds, fmt.Sprintf("%d %s", g.ByKind[k], k))
		}
	}
	fmt.Fprintf(&b, "The code defines %d items (%s); %d are documented. %d gaps:\n\n", g.Items, strings.Join(kinds, ", "), g.Covered, len(g.Gaps))
	b.WriteString("| Group | Topic | Mode | Target | Gaps | Priority |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, gr := range g.Groups {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d |\n", gr.ID, gr.Topic, gr.Mode, gr.Target, gapList(gr.Gaps), gr.Score)
	}
	b.WriteString("\n## 2–5. Drafts, validation and evaluation\n\n")
	if len(r.Drafts) == 0 {
		b.WriteString("Nothing to draft.\n\n")
	}
	b.WriteString("| Group | Folder | Claims | Score | Gate | Blocking |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, d := range r.Drafts {
		if d.Error != "" {
			fmt.Fprintf(&b, "| %s | %s | — | — | error | %s |\n", d.Group.ID, d.Dir, d.Error)
			continue
		}
		gate := "blocked"
		if d.Eval.Gate.Passed {
			gate = "passed"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s |\n", d.Group.ID, d.Dir, d.Claims, d.Eval.Score, gate, orDash(strings.Join(d.Eval.Gate.Blocking, "; ")))
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\nNot drafted this run (use --group): %s.\n", strings.Join(r.Skipped, "; "))
	}
	if len(r.Calls) > 0 {
		b.WriteString("\n## Context calls\n\n| Server | Tool | Query | Passages |\n| --- | --- | --- | --- |\n")
		for _, c := range r.Calls {
			p := fmt.Sprint(c.Passages)
			if c.Error != "" {
				p = "error: " + c.Error
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Server, c.Tool, strings.ReplaceAll(c.Query, "|", `\|`), p)
		}
	}
	b.WriteString(`
## Next: human review

For each draft folder:

1. Read ` + "`eval.md`" + ` and fix what blocks the gate. Edit ` + "`draft.md`" + ` as needed.
2. Check every claim in ` + "`review.md`" + ` and record it: ` + "`docsbot draft verify DIR --claim C1,C2 --status verified --by NAME`" + `.
3. Re-run the checks on the edited draft: ` + "`docsbot agent eval DIR`" + `.
4. Approve: ` + "`docsbot agent approve DIR --by NAME`" + ` (refused until the gate passes and every claim is verified).
5. New pages: ` + "`docsbot agent approve DIR --by NAME --publish`" + ` writes the page into the docs. Updates: apply ` + "`proposed-changes.md`" + ` by hand.
`)
	return b.String()
}

func gapList(gs []gaps.Gap) string {
	var names []string
	for _, g := range gs {
		n := "`" + g.Item.Name + "`"
		if g.Type != "undocumented" {
			n += " (" + g.Type + ")"
		} else if g.Change != nil && g.Change.Status == "added" {
			n += " (new)"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 6 {
		names = append(names[:6], fmt.Sprintf("and %d more", len(gs)-6))
	}
	return strings.Join(names, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
