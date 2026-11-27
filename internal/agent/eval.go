package agent

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/kakabisht/Docs-toolkit/internal/gaps"
	"gopkg.in/yaml.v3"
)

// JudgePromptVersion names the judge prompt; it is recorded in eval.json.
const JudgePromptVersion = "judge-v1"

//go:embed prompts/judge-v1.md
var judgePrompt string

// Gate is what a draft must reach before a person can approve it.
type Gate struct {
	MinScore int `yaml:"min_score" json:"min_score"` // 0–100 (default 80)
	MinJudge int `yaml:"min_judge" json:"min_judge"` // lowest judge score per criterion, 1–5 (default 3)
}

// Eval is eval.json.
type Eval struct {
	Draft       string     `json:"draft"`
	DraftSHA256 string     `json:"draft_sha256"` // approve refuses a draft changed since
	Date        string     `json:"date,omitempty"`
	Generator   string     `json:"generator"`
	Metrics     Metrics    `json:"metrics"`
	Examples    []Example  `json:"examples"`
	Links       []Link     `json:"links"`
	References  []Ref      `json:"unknown_references,omitempty"`
	Facts       []FactNote `json:"facts_to_check,omitempty"`
	Missing     []string   `json:"items_not_in_draft,omitempty"`
	Judge       *Judgement `json:"judge,omitempty"`
	Score       int        `json:"score"`
	Gate        GateResult `json:"gate"`
}

// Metrics are the counts the score is made from.
type Metrics struct {
	Claims           int `json:"claims"`
	Cited            int `json:"cited"`
	InvalidCitations int `json:"invalid_citations"`
	Facts            int `json:"facts"`
	Grounded         int `json:"facts_grounded"` // in a cited passage or confirmed in the repository
	Conflicts        int `json:"facts_conflicting"`
	Items            int `json:"items"`
	Covered          int `json:"items_covered"`
	Sections         int `json:"sections"`
	Placeholders     int `json:"sections_without_sources"`
	UnknownRefs      int `json:"references_unknown"` // flags and endpoints the code does not define
	Examples         int `json:"examples"`
	BadExamples      int `json:"examples_invalid"`
	Links            int `json:"links"`
	BrokenLinks      int `json:"links_broken"`
}

// Example is one fenced code block and what was checked.
type Example struct {
	Line    int      `json:"line"`
	Lang    string   `json:"lang,omitempty"`
	Checked string   `json:"checked"` // what was checked: syntax, flags, nothing
	OK      bool     `json:"ok"`
	Problem []string `json:"problems,omitempty"`
}

// Link is one link in the draft.
type Link struct {
	Line    int    `json:"line"`
	Href    string `json:"href"`
	Status  string `json:"status"` // ok, broken, not-checked
	Problem string `json:"problem,omitempty"`
}

// Ref is a flag or endpoint the draft names that the code does not define.
type Ref struct {
	Line int    `json:"line"`
	Name string `json:"name"`
}

// FactNote is a fact neither the cited passage nor the repository confirms,
// or one the repository contradicts.
type FactNote struct {
	Claim    string `json:"claim"`
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Conflict string `json:"conflict,omitempty"`
}

// Judgement is the model judge's scores, 1–5.
type Judgement struct {
	Model         string   `json:"model"`
	PromptVersion string   `json:"prompt_version"`
	Faithfulness  int      `json:"faithfulness"`
	Completeness  int      `json:"completeness"`
	Clarity       int      `json:"clarity"`
	Examples      int      `json:"examples"`
	Problems      []string `json:"problems,omitempty"`
	Error         string   `json:"error,omitempty"`
}

// GateResult says whether the draft may go to approval.
type GateResult struct {
	Passed   bool     `json:"passed"`
	MinScore int      `json:"min_score"`
	Blocking []string `json:"blocking,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// EvalOptions configures Evaluate.
type EvalOptions struct {
	Dir      string
	Meta     Meta
	Evidence *draft.Evidence
	External bool
	Gate     Gate
	Judge    bool
	Provider draft.Provider
	Date     string
	HTTP     *http.Client // for external links; default 10s timeout
}

// LoadMeta reads agent.json from a draft folder.
func LoadMeta(dir string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("%s has no %s: it was not written by docsbot agent run", dir, MetaFile)
		}
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func sha(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

var (
	fenceRe    = regexp.MustCompile("^\\s*(```+|~~~+)\\s*([\\w+-]*)")
	linkRe     = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	flagUse    = regexp.MustCompile(`(?:^|\s)(--[a-z][\w-]*)`)
	footnoteRe = regexp.MustCompile(`^\[\^[^\]]+\]:`)
)

var shellLang = map[string]bool{"sh": true, "bash": true, "shell": true, "console": true, "zsh": true, "shell-session": true}

// Evaluate validates a draft folder and scores it, then writes eval.json and
// eval.md. It is run by the agent after drafting and again with
// `docsbot agent eval` after a person edits the draft.
func Evaluate(ctx context.Context, o EvalOptions) (*Eval, error) {
	if o.Gate.MinScore == 0 {
		o.Gate.MinScore = 80
	}
	if o.Gate.MinJudge == 0 {
		o.Gate.MinJudge = 3
	}
	mdPath := filepath.Join(o.Dir, draft.DraftFile)
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, err
	}
	led, err := draft.ReadLedger(filepath.Join(o.Dir, draft.LedgerFile))
	if err != nil {
		return nil, err
	}
	if o.Evidence != nil {
		led.SetRepo(o.Evidence, nil)
	}
	led.Sync(string(raw))
	md := string(raw)
	e := &Eval{Draft: draft.DraftFile, DraftSHA256: sha(raw), Date: o.Date, Generator: led.Generator}
	m := &e.Metrics

	// claims, citations and facts, from the ledger
	for _, c := range led.Claims {
		if c.Removed {
			continue
		}
		if !c.Code {
			m.Claims++
			if !c.Uncited {
				m.Cited++
			}
		}
		if len(c.Invalid) > 0 {
			m.InvalidCitations++
		}
		for _, f := range c.Facts {
			m.Facts++
			switch {
			case f.Conflict != "":
				m.Conflicts++
				e.Facts = append(e.Facts, FactNote{Claim: c.ID, Kind: f.Kind, Value: f.Value, Conflict: f.Conflict})
			case f.InSource || len(f.Repo) > 0:
				m.Grounded++
			default:
				e.Facts = append(e.Facts, FactNote{Claim: c.ID, Kind: f.Kind, Value: f.Value})
			}
		}
	}

	// sections and the items the group must cover
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "## ") {
			m.Sections++
		}
	}
	m.Placeholders = strings.Count(md, "**[placeholder]**")
	body := []gaps.Page{{File: draft.DraftFile, Text: md}}
	for _, g := range o.Meta.Group.Gaps {
		m.Items++
		if g.Type == "stale" || len(gaps.Mentions(body, g.Item)) > 0 {
			// a stale item is covered by saying it was removed, or by leaving it out
			m.Covered++
		} else {
			e.Missing = append(e.Missing, g.Item.Name)
		}
	}

	// references, examples and links
	known, endpoints := knownSurface(o.Meta)
	e.References = checkRefs(md, known, endpoints, o.Meta)
	m.UnknownRefs = len(e.References)
	e.Examples = checkExamples(md, known, o.Evidence)
	for _, x := range e.Examples {
		m.Examples++
		if !x.OK {
			m.BadExamples++
		}
	}
	e.Links = checkLinks(ctx, md, o)
	for _, l := range e.Links {
		m.Links++
		if l.Status == "broken" {
			m.BrokenLinks++
		}
	}

	// the judge
	if o.Judge {
		e.Judge = judge(ctx, o, led, md)
	}

	e.Score = score(*m)
	e.Gate = gate(e, o)
	if err := writeJSON(filepath.Join(o.Dir, EvalFile), e); err != nil {
		return nil, err
	}
	return e, os.WriteFile(filepath.Join(o.Dir, EvalReport), []byte(evalReport(e, o.Meta)), 0o644)
}

func ratio(a, b int, empty float64) float64 {
	if b == 0 {
		return empty
	}
	return float64(a) / float64(b)
}

// score weighs citation coverage, grounded facts, items covered and sections
// filled: 30, 30, 25 and 15 points.
func score(m Metrics) int {
	s := 30*ratio(m.Cited, m.Claims, 0) + 30*ratio(m.Grounded, m.Facts, 1) +
		25*ratio(m.Covered, m.Items, 1) + 15*ratio(m.Sections-m.Placeholders, m.Sections, 0)
	return int(math.Round(s))
}

func gate(e *Eval, o EvalOptions) GateResult {
	m := e.Metrics
	g := GateResult{MinScore: o.Gate.MinScore}
	block := func(f string, a ...any) { g.Blocking = append(g.Blocking, fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { g.Warnings = append(g.Warnings, fmt.Sprintf(f, a...)) }
	if m.Claims == 0 {
		block("the draft has no statements")
	}
	if m.Conflicts > 0 {
		block("%d fact(s) contradict the repository", m.Conflicts)
	}
	if m.InvalidCitations > 0 {
		block("%d statement(s) cite passages that do not exist", m.InvalidCitations)
	}
	if m.UnknownRefs > 0 {
		names := make([]string, 0, len(e.References))
		for _, r := range e.References {
			names = append(names, r.Name)
		}
		block("names %d flag(s) or endpoint(s) the code does not define: %s", m.UnknownRefs, strings.Join(names, ", "))
	}
	if m.BadExamples > 0 {
		block("%d example(s) fail validation", m.BadExamples)
	}
	if m.BrokenLinks > 0 {
		block("%d broken link(s)", m.BrokenLinks)
	}
	if e.Score < o.Gate.MinScore {
		block("score %d is below %d", e.Score, o.Gate.MinScore)
	}
	if j := e.Judge; j != nil {
		switch {
		case j.Error != "":
			block("the judge did not run: %s", j.Error)
		default:
			for name, v := range map[string]int{"faithfulness": j.Faithfulness, "completeness": j.Completeness, "clarity": j.Clarity, "examples": j.Examples} {
				if v < o.Gate.MinJudge {
					block("judge scored %s %d (minimum %d)", name, v, o.Gate.MinJudge)
				}
			}
		}
	}
	if n := m.Claims - m.Cited; n > 0 {
		warn("%d statement(s) without a citation", n)
	}
	if n := m.Facts - m.Grounded - m.Conflicts; n > 0 {
		warn("%d fact(s) not found in a cited passage or the repository", n)
	}
	if len(e.Missing) > 0 {
		warn("not in the draft: %s", strings.Join(e.Missing, ", "))
	}
	if m.Placeholders > 0 {
		warn("%d section(s) have no source", m.Placeholders)
	}
	if m.Examples == 0 {
		warn("no examples")
	}
	for _, l := range e.Links {
		if l.Status == "not-checked" {
			warn("external links were not checked (run with --external-links)")
			break
		}
	}
	if strings.Contains(e.Generator, "extractive") {
		warn("extractive draft: sentences are copied from the sources; rewrite them before approval")
	}
	g.Passed = len(g.Blocking) == 0
	return g
}

func knownSurface(meta Meta) (flags, endpoints map[string]bool) {
	flags, endpoints = map[string]bool{}, map[string]bool{}
	items, _ := gaps.Surface(meta.Root)
	for _, it := range items {
		switch it.Kind {
		case "flag":
			flags[it.Name] = true
		case "endpoint":
			endpoints[it.Name] = true
		}
	}
	return flags, endpoints
}

var endpointUse = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD) (/[\w{}/:.<>-]*)`)

// checkRefs finds flags and endpoints named in the prose that the code does
// not define, except items this group reports as removed: a draft may say
// they are gone. Footnotes, comments and code blocks are skipped (code blocks
// are checked as examples).
func checkRefs(md string, flags, endpoints map[string]bool, meta Meta) []Ref {
	if len(flags)+len(endpoints) == 0 {
		return nil
	}
	removed := map[string]bool{}
	for _, g := range meta.Group.Gaps {
		if g.Type == "stale" {
			removed[g.Item.Name] = true
		}
	}
	_, body := splitFrontMatter(md)
	offset := strings.Count(md[:len(md)-len(body)], "\n")
	var out []Ref
	seen := map[string]bool{}
	inFence := false
	for i, l := range strings.Split(body, "\n") {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence || footnoteRe.MatchString(l) || strings.HasPrefix(strings.TrimSpace(l), "<!--") {
			continue
		}
		var names []string
		if len(flags) > 0 {
			for _, m := range flagUse.FindAllStringSubmatch(strings.ReplaceAll(l, "`", " "), -1) {
				names = append(names, strings.SplitN(m[1], "=", 2)[0])
			}
		}
		for _, m := range endpointUse.FindAllStringSubmatch(l, -1) {
			if len(endpoints) > 0 {
				names = append(names, gaps.Endpoint(m[1], strings.TrimRight(m[2], ".")))
			}
		}
		for _, n := range names {
			isFlag := strings.HasPrefix(n, "--")
			if seen[n] || removed[n] || isFlag && flags[n] || !isFlag && endpoints[n] {
				continue
			}
			seen[n] = true
			out = append(out, Ref{Line: offset + i + 1, Name: n})
		}
	}
	return out
}

// checkExamples validates each fenced block: JSON, YAML and TOML must parse,
// and every --flag in a shell block must be defined in the code (or appear
// in a flag definition or reference table the repository has).
func checkExamples(md string, known map[string]bool, ev *draft.Evidence) []Example {
	var out []Example
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		fence, lang := m[1], strings.ToLower(m[2])
		start := i
		var body []string
		for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
			body = append(body, lines[i])
		}
		src := strings.Join(body, "\n")
		x := Example{Line: start + 1, Lang: lang, OK: true, Checked: "nothing (no check for this language)"}
		switch {
		case lang == "json" || lang == "" && (strings.HasPrefix(strings.TrimSpace(src), "{") || strings.HasPrefix(strings.TrimSpace(src), "[")):
			x.Checked = "JSON syntax"
			var v any
			if err := json.Unmarshal([]byte(src), &v); err != nil {
				x.Problem = append(x.Problem, "invalid JSON: "+err.Error())
			}
		case lang == "yaml" || lang == "yml":
			x.Checked = "YAML syntax"
			var v any
			if err := yaml.Unmarshal([]byte(src), &v); err != nil {
				x.Problem = append(x.Problem, "invalid YAML: "+err.Error())
			}
		case lang == "toml":
			x.Checked = "TOML syntax"
			var v map[string]any
			if _, err := toml.Decode(src, &v); err != nil {
				x.Problem = append(x.Problem, "invalid TOML: "+err.Error())
			}
		case shellLang[lang] || lang == "" && strings.Contains(src, "--"):
			x.Checked = "flags against the code"
			seen := map[string]bool{}
			for _, l := range body {
				l = strings.SplitN(l, "#", 2)[0] // comments may mention anything
				for _, fm := range flagUse.FindAllStringSubmatch(l, -1) {
					f := strings.SplitN(fm[1], "=", 2)[0]
					if seen[f] || known[f] {
						continue
					}
					seen[f] = true
					if ev != nil && len(ev.Flag(f).Found) > 0 {
						continue
					}
					x.Problem = append(x.Problem, f+" is not defined in the code")
				}
			}
		}
		x.OK = len(x.Problem) == 0
		out = append(out, x)
	}
	return out
}

// checkLinks resolves relative links from where the page will live (the
// group's target) and checks external ones over HTTP when asked.
func checkLinks(ctx context.Context, md string, o EvalOptions) []Link {
	var out []Link
	inFence := false
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	for i, l := range strings.Split(md, "\n") {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence || footnoteRe.MatchString(l) {
			continue
		}
		for _, m := range linkRe.FindAllStringSubmatch(l, -1) {
			href := m[1]
			lk := Link{Line: i + 1, Href: href, Status: "ok"}
			switch {
			case strings.HasPrefix(href, "#"), strings.HasPrefix(href, "mailto:"):
				continue
			case strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://"):
				if !o.External {
					lk.Status = "not-checked"
					break
				}
				if err := fetchOK(ctx, client, href); err != nil {
					lk.Status, lk.Problem = "broken", err.Error()
				}
			default:
				if p := localTarget(o.Meta, href); p == "" {
					lk.Status, lk.Problem = "broken", "no such file from "+o.Meta.Group.Target
				}
			}
			out = append(out, lk)
		}
	}
	return out
}

func fetchOK(ctx context.Context, c *http.Client, u string) error {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "docsbot-agent")
		resp, err := c.Do(req)
		if err != nil {
			if method == http.MethodGet {
				return err
			}
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			return nil
		}
		if method == http.MethodGet || resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusForbidden {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return nil
}

// localTarget returns the file a relative link reaches from the target page,
// trying the usual Markdown and route forms, or "" when none exists.
func localTarget(meta Meta, href string) string {
	h := strings.SplitN(strings.SplitN(href, "#", 2)[0], "?", 2)[0]
	if h == "" {
		return meta.Group.Target
	}
	var base string
	if strings.HasPrefix(h, "/") {
		base = path.Join(filepath.ToSlash(meta.Content), h)
	} else {
		base = path.Join(path.Dir(meta.Group.Target), h)
	}
	cands := []string{base, base + ".md", base + ".mdx", path.Join(base, "index.md"), path.Join(base, "README.md"),
		strings.TrimSuffix(base, ".html") + ".md", strings.TrimSuffix(base, "/") + ".md"}
	for _, c := range cands {
		if c == "" || strings.HasPrefix(c, "..") {
			continue
		}
		if st, err := os.Stat(filepath.Join(meta.Root, filepath.FromSlash(c))); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

var jsonObj = regexp.MustCompile(`(?s)\{.*\}`)

func judge(ctx context.Context, o EvalOptions, led *draft.Ledger, md string) *Judgement {
	j := &Judgement{PromptVersion: JudgePromptVersion}
	if o.Provider == nil {
		j.Error = "no model provider is configured"
		return j
	}
	j.Model = o.Provider.Name()
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\nPage: %s (%s)\n\nItems the page must document:\n", o.Meta.Group.Topic, o.Meta.Group.Target, o.Meta.Group.Mode)
	for _, g := range o.Meta.Group.Gaps {
		fmt.Fprintf(&b, "- %s %s", g.Item.Kind, g.Item.Name)
		if g.Item.Default != "" {
			fmt.Fprintf(&b, " (default %s)", g.Item.Default)
		}
		if g.Type != "undocumented" {
			fmt.Fprintf(&b, " [%s]", g.Type)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nSource passages:\n")
	for _, s := range led.Sources {
		fmt.Fprintf(&b, "\n[%s] (%s)\n%s\n", s.ID, s.Location(), s.Text)
	}
	b.WriteString("\nDraft:\n\n" + md)
	out, err := o.Provider.Complete(ctx, judgePrompt, b.String())
	if err != nil {
		j.Error = err.Error()
		return j
	}
	raw := jsonObj.FindString(out)
	if raw == "" || json.Unmarshal([]byte(raw), j) != nil {
		j.Error = "the judge's reply was not the JSON object asked for"
		return j
	}
	j.Model, j.PromptVersion = o.Provider.Name(), JudgePromptVersion
	for _, v := range []int{j.Faithfulness, j.Completeness, j.Clarity, j.Examples} {
		if v < 1 || v > 5 {
			j.Error = "the judge gave a score outside 1–5"
		}
	}
	return j
}

func evalReport(e *Eval, meta Meta) string {
	var b strings.Builder
	m := e.Metrics
	verdict := "**Blocked.** Fix what is listed below, then run `docsbot agent eval` on this folder."
	if e.Gate.Passed {
		verdict = "**Passed.** A person can now verify each claim and approve the draft."
	}
	fmt.Fprintf(&b, "# Evaluation: %s\n\n", meta.Group.Topic)
	fmt.Fprintf(&b, "Score **%d/100** (gate %d). %s\n\n", e.Score, e.Gate.MinScore, verdict)
	if len(e.Gate.Blocking) > 0 {
		b.WriteString("## Blocking\n\n")
		for _, s := range e.Gate.Blocking {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
	}
	if len(e.Gate.Warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, s := range e.Gate.Warnings {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Scores\n\n| Check | Result | Points |\n| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| Statements with a citation | %d of %d | %.0f/30 |\n", m.Cited, m.Claims, 30*ratio(m.Cited, m.Claims, 0))
	fmt.Fprintf(&b, "| Facts in a cited passage or the repository | %d of %d | %.0f/30 |\n", m.Grounded, m.Facts, 30*ratio(m.Grounded, m.Facts, 1))
	fmt.Fprintf(&b, "| Gap items covered | %d of %d | %.0f/25 |\n", m.Covered, m.Items, 25*ratio(m.Covered, m.Items, 1))
	fmt.Fprintf(&b, "| Sections with sources | %d of %d | %.0f/15 |\n", m.Sections-m.Placeholders, m.Sections, 15*ratio(m.Sections-m.Placeholders, m.Sections, 0))
	fmt.Fprintf(&b, "| Flags and endpoints the code defines | %d unknown | gate |\n", m.UnknownRefs)
	fmt.Fprintf(&b, "| Examples valid | %d of %d | gate |\n", m.Examples-m.BadExamples, m.Examples)
	fmt.Fprintf(&b, "| Links resolve | %d of %d | gate |\n", m.Links-m.BrokenLinks, m.Links)
	fmt.Fprintf(&b, "| Facts contradicting the repository | %d | gate |\n\n", m.Conflicts)
	if j := e.Judge; j != nil {
		b.WriteString("## Judge\n\n")
		if j.Error != "" {
			fmt.Fprintf(&b, "Did not run: %s\n\n", j.Error)
		} else {
			fmt.Fprintf(&b, "%s, prompt %s: faithfulness %d, completeness %d, clarity %d, examples %d (each out of 5).\n\n", j.Model, j.PromptVersion, j.Faithfulness, j.Completeness, j.Clarity, j.Examples)
			for _, p := range j.Problems {
				b.WriteString("- " + p + "\n")
			}
			b.WriteString("\n")
		}
	}
	var bad []string
	for _, x := range e.Examples {
		for _, p := range x.Problem {
			bad = append(bad, fmt.Sprintf("- line %d (%s): %s", x.Line, x.Lang, p))
		}
	}
	for _, l := range e.Links {
		if l.Status == "broken" {
			bad = append(bad, fmt.Sprintf("- line %d: link %s: %s", l.Line, l.Href, l.Problem))
		}
	}
	for _, r := range e.References {
		bad = append(bad, fmt.Sprintf("- line %d: %s is not defined in the code", r.Line, r.Name))
	}
	for _, f := range e.Facts {
		if f.Conflict != "" {
			bad = append(bad, fmt.Sprintf("- %s: %s `%s` conflicts with the repository: %s", f.Claim, f.Kind, f.Value, f.Conflict))
		}
	}
	if len(bad) > 0 {
		b.WriteString("## Problems\n\n" + strings.Join(bad, "\n") + "\n\n")
	}
	var unconfirmed []string
	for _, f := range e.Facts {
		if f.Conflict == "" {
			unconfirmed = append(unconfirmed, fmt.Sprintf("%s `%s` (%s)", f.Kind, f.Value, f.Claim))
		}
	}
	if len(unconfirmed) > 0 {
		b.WriteString("## Facts to check by hand\n\nNot found in a cited passage or the repository: " + strings.Join(unconfirmed, ", ") + ".\n\n")
	}
	b.WriteString("These checks are text matches: they find invented values and broken references; they do not prove a statement true. Every claim still needs a person's review in `review.md`.\n")
	return b.String()
}
