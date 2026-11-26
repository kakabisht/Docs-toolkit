package draft

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/kakabisht/Docs-toolkit/internal/search"
)

// Options configures one draft.
type Options struct {
	Topic       string
	Title       string // defaults to the topic
	KB          []string
	Transcripts []string
	Template    string // path; empty for the default
	Out         string // output folder
	Force       bool   // replace an existing draft
	Provider    Provider
	PerSection  int    // passages per section (default 5)
	Date        string // generated: value, YYYY-MM-DD
	Progress    func(string)
}

// Result is what Generate wrote.
type Result struct {
	Dir     string         `json:"dir"`
	Files   []string       `json:"files"`
	Sources int            `json:"sources"`
	Counts  map[string]int `json:"claims"`
	Empty   []string       `json:"sections_without_sources,omitempty"`
}

// Files written into the output folder.
const (
	DraftFile  = "draft.md"
	LedgerFile = "claims.json"
	ReviewFile = "review.md"
)

// Generate writes draft.md, claims.json and review.md into opt.Out.
func Generate(ctx context.Context, opt Options) (*Result, error) {
	if strings.TrimSpace(opt.Topic) == "" {
		return nil, fmt.Errorf("a topic is required")
	}
	if opt.Out == "" {
		return nil, fmt.Errorf("an output folder is required")
	}
	if len(opt.KB) == 0 && len(opt.Transcripts) == 0 {
		return nil, fmt.Errorf("give a knowledge base (--kb) or transcripts (--transcript) to draft from")
	}
	outAbs, _ := filepath.Abs(opt.Out)
	for _, k := range opt.KB {
		ka, _ := filepath.Abs(k)
		if outAbs == ka || strings.HasPrefix(outAbs, ka+string(filepath.Separator)) {
			return nil, fmt.Errorf("output folder %s is inside the knowledge base %s; drafts are never written into the docs", opt.Out, k)
		}
	}
	if _, err := os.Stat(filepath.Join(opt.Out, DraftFile)); err == nil && !opt.Force {
		return nil, fmt.Errorf("%s already exists; use --force to replace it and its review record", filepath.Join(opt.Out, DraftFile))
	}
	if opt.PerSection == 0 {
		opt.PerSection = 5
	}
	progress := opt.Progress
	if progress == nil {
		progress = func(string) {}
	}
	sections, err := LoadTemplate(opt.Template)
	if err != nil {
		return nil, err
	}
	srcs, err := LoadKB(opt.KB)
	if err != nil {
		return nil, err
	}
	for _, t := range opt.Transcripts {
		ts, err := LoadTranscript(t)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, ts...)
	}
	for i := range srcs {
		srcs[i].ID = fmt.Sprintf("s%d", i+1)
	}
	led := &Ledger{Version: 1, Draft: DraftFile, Topic: opt.Topic, Generator: "docsbot draft (extractive)"}
	if opt.Provider != nil {
		led.Generator = "docsbot draft (" + opt.Provider.Name() + ")"
		led.PromptVersion = PromptVersion
		led.PromptSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(promptV1)))[:16]
	}
	ix := newIndex(srcs)
	used := map[string]bool{}
	title := firstNonEmpty(opt.Title, opt.Topic)
	var body strings.Builder
	res := &Result{Dir: opt.Out}
	for _, sec := range sections {
		progress("section: " + sec.Heading)
		hits := ix.search(opt.Topic+" "+sec.Heading+" "+sec.Query, opt.PerSection, used)
		fmt.Fprintf(&body, "## %s\n\n", sec.Heading)
		if sec.Guidance != "" {
			fmt.Fprintf(&body, "<!-- %s -->\n\n", strings.ReplaceAll(sec.Guidance, "--", "—"))
		}
		var text string
		if len(hits) == 0 {
			text = ""
		} else if opt.Provider != nil {
			text, err = writeWithModel(ctx, opt.Provider, opt.Topic, sec, hits)
			if err != nil {
				return nil, fmt.Errorf("section %q: %w", sec.Heading, err)
			}
		} else {
			text = extractive(hits)
		}
		if strings.TrimSpace(text) == "" || strings.Contains(text, "[placeholder]") {
			res.Empty = append(res.Empty, sec.Heading)
			text = "> **[placeholder]** No source covers this section. Add sources and draft again, or write it yourself and cite what you used."
		}
		for _, h := range hits {
			used[h.ID] = true
		}
		body.WriteString(strings.TrimSpace(text) + "\n\n")
	}
	md := body.String()
	// keep only the sources the draft cites, in the order they appear
	cited := map[string]bool{}
	for _, m := range citeRe.FindAllStringSubmatch(md, -1) {
		cited[m[1]] = true
	}
	led.Sources = srcs
	led.Build(md)
	var notes []string
	for _, s := range srcs {
		if cited[s.ID] {
			notes = append(notes, fmt.Sprintf("[^%s]: %s: “%s”", s.ID, s.Location(), excerpt(s.Text, 200)))
		}
	}
	// mark uncited statements where a reader will see them
	for _, c := range led.Claims {
		if c.Uncited && !c.Code {
			md = markUncited(md, c.Text)
		}
	}
	var keep []Source
	for _, s := range srcs {
		if cited[s.ID] {
			keep = append(keep, s)
		}
	}
	led.Sources = keep
	var doc strings.Builder
	doc.WriteString("---\n")
	fmt.Fprintf(&doc, "title: %q\nstatus: draft\nreview: required\ntopic: %q\ngenerator: %q\n", title, opt.Topic, led.Generator)
	if led.PromptVersion != "" {
		fmt.Fprintf(&doc, "prompt_version: %s\n", led.PromptVersion)
	}
	if opt.Date != "" {
		fmt.Fprintf(&doc, "generated: %s\n", opt.Date)
	}
	fmt.Fprintf(&doc, "claims: %s\n---\n\n", LedgerFile)
	fmt.Fprintf(&doc, "<!-- Draft for review: every statement cites a source passage. Check each claim in %s and record it with `docsbot draft verify`; `docsbot check` fails while a page marked ready has unverified claims. -->\n\n", ReviewFile)
	if opt.Provider == nil {
		doc.WriteString("<!-- Extractive draft: sentences are taken from the sources as written. Rewrite them in your own words and keep the citations. -->\n\n")
	}
	doc.WriteString(md)
	if len(notes) > 0 {
		doc.WriteString(strings.Join(notes, "\n") + "\n")
	}
	final := strings.TrimRight(doc.String(), "\n") + "\n"
	led.Build(final) // claims from the file as written, so verify matches it
	if err := os.MkdirAll(opt.Out, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opt.Out, DraftFile), []byte(final), 0o644); err != nil {
		return nil, err
	}
	if err := led.Write(filepath.Join(opt.Out, LedgerFile)); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opt.Out, ReviewFile), []byte(Checklist(led)), 0o644); err != nil {
		return nil, err
	}
	res.Files = []string{DraftFile, LedgerFile, ReviewFile}
	res.Sources = len(keep)
	res.Counts = led.Counts()
	return res, nil
}

// markUncited appends **[uncited]** to the statement, after any citation
// markers that do not resolve.
func markUncited(md, text string) string {
	i := strings.Index(md, text)
	if i < 0 {
		return md
	}
	j := i + len(text)
	for {
		loc := citeRe.FindStringIndex(md[j:])
		if loc == nil || loc[0] != 0 {
			break
		}
		j += loc[1]
	}
	return md[:j] + " **[uncited]**" + md[j:]
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "”", "\"")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

var (
	fillers = regexp.MustCompile(`(?i)(?:^|\s)(?:um+|uh+|erm|you know,?|I mean,?|sort of|kind of)(?:[,.]|\s|$)`)
)

var (
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	leadWords = regexp.MustCompile(`(?i)^(?:(?:so|and|well|okay|ok|right|yeah)[,]?\s+)+`)
)

// extractive writes a section from passages as they are: up to three cited
// sentences per passage, prose first, then at most two code blocks.
func extractive(hits []Source) string {
	var prose, code []string
	n := 0
	for _, h := range hits {
		if h.Code {
			if len(code) < 2 {
				code = append(code, fmt.Sprintf("Source:[^%s]\n\n```\n%s\n```", h.ID, h.Text))
			}
			continue
		}
		t := mdLink.ReplaceAllString(h.Text, "$1") // links would point nowhere from the draft folder
		if h.Kind == "transcript" {
			t = dedupeWords(fillers.ReplaceAllString(t, " "))
		}
		var picked []string
		for _, st := range sentences("", t, 0) {
			if len(picked) == 3 || n+len(picked) >= 8 {
				break
			}
			s := strings.TrimSpace(st.Text)
			if h.Kind == "transcript" {
				s = leadWords.ReplaceAllString(s, "")
				if r := []rune(s); len(r) > 0 {
					s = strings.ToUpper(string(r[0])) + string(r[1:])
				}
			}
			// fragments, questions and lead-ins to a block that isn't here
			if len(strings.Fields(s)) < 4 || strings.HasSuffix(s, "?") || strings.HasSuffix(s, ":") || !startsUpper(s) {
				continue
			}
			if !strings.ContainsAny(s[len(s)-1:], ".!") {
				s += "."
			}
			picked = append(picked, s+"[^"+h.ID+"]")
		}
		if len(picked) > 0 {
			prose = append(prose, strings.Join(picked, " "))
			n += len(picked)
		}
	}
	return strings.Join(append(prose, code...), "\n\n")
}

func startsUpper(s string) bool {
	for _, r := range s {
		return unicode.IsUpper(r) || unicode.IsDigit(r) || r == '`'
	}
	return false
}

// dedupeWords drops a word repeated straight after itself ("the the").
func dedupeWords(s string) string {
	var out []string
	for _, w := range strings.Fields(s) {
		if len(out) > 0 && strings.EqualFold(out[len(out)-1], w) {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

func writeWithModel(ctx context.Context, p Provider, topic string, sec Section, hits []Source) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\nSection: %s\n", topic, sec.Heading)
	if sec.Guidance != "" {
		fmt.Fprintf(&b, "What belongs in this section: %s\n", sec.Guidance)
	}
	b.WriteString("\nPassages:\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "\n[%s] (%s)\n%s\n", h.ID, h.Location(), h.Text)
	}
	out, err := p.Complete(ctx, promptV1, b.String())
	if err != nil {
		return "", err
	}
	// drop a heading the model added despite the prompt
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#") {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// index is BM25 over source passages; the section heading counts twice.
type index struct {
	srcs []Source
	tf   []map[string]float64
	df   map[string]int
	len  []float64
	avg  float64
}

func newIndex(srcs []Source) *index {
	ix := &index{srcs: srcs, df: map[string]int{}}
	total := 0.0
	for _, s := range srcs {
		tf := map[string]float64{}
		for _, t := range search.Tokens(s.Text) {
			tf[t]++
		}
		for _, t := range search.Tokens(s.Heading) {
			tf[t] += 2
		}
		l := 0.0
		for t, n := range tf {
			ix.df[t]++
			l += n
		}
		ix.tf = append(ix.tf, tf)
		ix.len = append(ix.len, l)
		total += l
	}
	if len(srcs) > 0 {
		ix.avg = total / float64(len(srcs))
	}
	return ix
}

// search returns up to n unused passages, best first, scoring at least a
// quarter of the best match.
func (ix *index) search(q string, n int, used map[string]bool) []Source {
	terms := map[string]bool{}
	for _, t := range search.Tokens(q) {
		terms[t] = true
	}
	type hit struct {
		i     int
		score float64
	}
	var hits []hit
	N := float64(len(ix.srcs))
	for i, tf := range ix.tf {
		if used[ix.srcs[i].ID] {
			continue
		}
		s := 0.0
		for t := range terms {
			f := tf[t]
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (N-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
			s += idf * f * 2.2 / (f + 1.2*(0.25+0.75*ix.len[i]/ix.avg))
		}
		if s > 0 {
			hits = append(hits, hit{i, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	var out []Source
	for _, h := range hits {
		if len(out) == n || h.score < hits[0].score/4 {
			break
		}
		out = append(out, ix.srcs[h.i])
	}
	return out
}
