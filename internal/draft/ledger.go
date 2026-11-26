package draft

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Claim statuses. Only a reviewer sets verified, rejected or needs-change.
const (
	Unverified  = "unverified"
	Verified    = "verified"
	Rejected    = "rejected"
	NeedsChange = "needs-change"
)

// Fact is a checkable detail inside a statement.
type Fact struct {
	Kind     string   `json:"kind"` // number, version, flag, command, parameter, default, field
	Value    string   `json:"value"`
	Key      string   `json:"key,omitempty"`      // for a default: the setting it belongs to
	InSource bool     `json:"in_cited_source"`    // found word for word in a cited passage
	Repo     []string `json:"in_repo,omitempty"`  // file:line in code, config, schemas or tables that confirm it
	Conflict string   `json:"conflict,omitempty"` // the repository gives a different value
}

// Ref is a page the knowledge graph says defines or documents an entity.
type Ref struct {
	Entity   string `json:"entity"`
	Relation string `json:"relation"` // defines or documents
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
}

// Entity is a knowledge-graph entity and the pages that define or document it.
type Entity struct {
	Name string
	Refs []Ref
}

// Claim is one statement of the draft.
type Claim struct {
	ID        string   `json:"id"`
	Section   string   `json:"section"`
	Text      string   `json:"text"` // without citation markers
	Hash      string   `json:"hash"`
	Citations []string `json:"citations"`
	Invalid   []string `json:"invalid_citations,omitempty"` // cited ids that are not sources
	Uncited   bool     `json:"uncited,omitempty"`
	Code      bool     `json:"code,omitempty"`
	Facts     []Fact   `json:"facts,omitempty"`
	Related   []Ref    `json:"related,omitempty"` // pages that define or document what the claim names
	Status    string   `json:"status"`
	Reviewer  string   `json:"reviewer,omitempty"`
	Note      string   `json:"note,omitempty"`
	Reviewed  string   `json:"reviewed,omitempty"` // date
	Removed   bool     `json:"removed,omitempty"`  // no longer in draft.md
}

// Ledger is claims.json.
type Ledger struct {
	Version       int    `json:"version"`
	Draft         string `json:"draft"`
	Topic         string `json:"topic"`
	Generator     string `json:"generator"`
	PromptVersion string `json:"prompt_version,omitempty"`
	PromptSHA256  string `json:"prompt_sha256,omitempty"`
	Repo          string `json:"repo,omitempty"` // repository the facts were checked against

	ev      *Evidence
	ents    []Entity
	Sources []Source `json:"sources"`
	Claims  []Claim  `json:"claims"`
}

// ReadLedger loads claims.json.
func ReadLedger(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l Ledger
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &l, nil
}

// Write saves the ledger as indented JSON.
func (l *Ledger) Write(path string) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (l *Ledger) source(id string) *Source {
	for i := range l.Sources {
		if l.Sources[i].ID == id {
			return &l.Sources[i]
		}
	}
	return nil
}

// Find returns the claim with id.
func (l *Ledger) Find(id string) *Claim {
	for i := range l.Claims {
		if strings.EqualFold(l.Claims[i].ID, id) {
			return &l.Claims[i]
		}
	}
	return nil
}

var (
	citeRe    = regexp.MustCompile(`\[\^([\w-]+)\]`)
	flagMark  = regexp.MustCompile(`\s*\*\*\[uncited\]\*\*`)
	reVersion = regexp.MustCompile(`\bv?\d+\.\d+(?:\.\d+)*(?:-[\w.]+)?\b`)
	reNumber  = regexp.MustCompile(`(?:^|[^\w.])(\d+(?:[.,]\d+)?(?:%|\s?(?:ms|s|seconds?|minutes?|hours?|days?|weeks?|[KMGT]i?B|bytes?|requests?|connections?|retries|times|x)\b)?)`)
	reFlag    = regexp.MustCompile(`(?:^|[\s"'(` + "`" + `])(--?[A-Za-z][\w-]*(?:=[^\s` + "`" + `"')]+)?)`)
	reCode    = regexp.MustCompile("`([^`]+)`")
	reDefault = regexp.MustCompile("(?i)\\bdefaults?\\s+((?:[\\w.`-]+\\s+){0,3}?)" + `(?:is|to|of|value\s+is|=)\s+(` + "`[^`]+`" + `|"[^"]+"|[\w.%/-]+)`)
	reEnv     = regexp.MustCompile(`\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\b`)
	reField   = regexp.MustCompile(`^[\w-]+(?:\.[\w-]+)+$|^[a-z]+(?:_[a-z0-9]+)+$|^[a-z]+[A-Z]\w*$`)
)

var shellWords = map[string]bool{"sudo": true, "curl": true, "git": true, "docker": true, "kubectl": true, "helm": true, "npm": true, "npx": true, "pip": true, "go": true, "make": true, "apt": true, "brew": true, "systemctl": true, "juju": true, "snap": true, "terraform": true, "ssh": true, "cd": true, "export": true}

// ExtractFacts finds the details a reviewer must check: numbers, versions,
// flags, commands, parameters and fields, defaults. It is deterministic.
func ExtractFacts(text string) []Fact {
	var out []Fact
	seen := map[string]bool{}
	add := func(kind, v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[kind+"\x00"+v] {
			return
		}
		seen[kind+"\x00"+v] = true
		out = append(out, Fact{Kind: kind, Value: v})
	}
	for _, m := range reDefault.FindAllStringSubmatch(text, -1) {
		add("default", strings.TrimRight(strings.Trim(m[2], "`\""), ".,;:"))
		if w := strings.Fields(m[1]); len(w) > 0 && len(out) > 0 {
			k := strings.Trim(w[len(w)-1], "`")
			if k != "value" && k != "setting" {
				out[len(out)-1].Key = k
			}
		}
	}
	for _, m := range reCode.FindAllStringSubmatch(text, -1) {
		c := m[1]
		first := strings.Fields(c)
		switch {
		case len(first) > 1 || shellWords[strings.TrimPrefix(c, "$ ")]:
			add("command", strings.TrimPrefix(c, "$ "))
		case strings.HasPrefix(c, "-"):
			// flags are picked up below
		case reField.MatchString(c) || reEnv.MatchString(c):
			add("field", c)
		default:
			add("parameter", c)
		}
	}
	for _, m := range reFlag.FindAllStringSubmatch(text, -1) {
		if len(m[1]) > 2 {
			add("flag", m[1])
		}
	}
	for _, m := range reEnv.FindAllString(text, -1) {
		add("field", m)
	}
	versions := map[string]bool{}
	for _, v := range reVersion.FindAllString(text, -1) {
		versions[v] = true
		add("version", v)
	}
	for _, m := range reNumber.FindAllStringSubmatch(text, -1) {
		v := strings.TrimSpace(m[1])
		if versions[v] || inVersion(v, versions) || seen["default\x00"+v] {
			continue
		}
		add("number", v)
	}
	return out
}

func inVersion(n string, versions map[string]bool) bool {
	for v := range versions {
		if strings.Contains(v, n) {
			return true
		}
	}
	return false
}

func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func hashOf(s string) string {
	h := sha256.Sum256([]byte(norm(s)))
	return fmt.Sprintf("%x", h[:8])
}

// Statement is a sentence (or code block) found in draft.md.
type Statement struct {
	Section   string
	Text      string // citations removed
	Citations []string
	Code      bool
	Line      int
}

var abbrev = regexp.MustCompile(`(?i)(?:\b(?:e\.g|i\.e|etc|vs|cf|approx|no|fig)\.)$`)

// Statements reads the claims out of a draft: every sentence of prose and
// every code block, skipping front matter, headings, comments, footnote
// definitions and placeholders.
func Statements(md string) []Statement {
	lines := strings.Split(md, "\n")
	var out []Statement
	section := ""
	i := 0
	if len(lines) > 0 && lines[0] == "---" {
		for j := 1; j < len(lines); j++ {
			if lines[j] == "---" {
				i = j + 1
				break
			}
		}
	}
	var para []string
	paraLine := 0
	flush := func() {
		if len(para) > 0 {
			out = append(out, sentences(section, strings.Join(para, " "), paraLine)...)
		}
		para = nil
	}
	inComment := false
	for ; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if inComment {
			if strings.Contains(t, "-->") {
				inComment = false
			}
			continue
		}
		switch {
		case strings.HasPrefix(t, "<!--"):
			flush()
			inComment = !strings.Contains(t, "-->")
			continue
		case strings.HasPrefix(t, "```"):
			flush()
			start := i + 1
			var code []string
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
				code = append(code, lines[j])
				j++
			}
			// the citation for a code block is on the line before it
			var cites []string
			if k := len(out) - 1; k >= 0 && out[k].Line == start-2 && strings.HasPrefix(out[k].Text, "Source:") {
				cites = out[k].Citations
				out = out[:k]
			}
			out = append(out, Statement{Section: section, Text: strings.Join(code, "\n"), Citations: cites, Code: true, Line: start})
			i = j
			continue
		case strings.HasPrefix(t, "#"):
			flush()
			section = strings.TrimSpace(strings.TrimLeft(t, "#"))
			continue
		case t == "" || strings.HasPrefix(t, "[^") || strings.Contains(t, "[placeholder]"):
			flush()
			continue
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || regexp.MustCompile(`^\d+\.\s`).MatchString(t):
			flush()
			t = regexp.MustCompile(`^(?:[-*]|\d+\.)\s+`).ReplaceAllString(t, "")
		}
		t = strings.TrimPrefix(t, "> ")
		if len(para) == 0 {
			paraLine = i + 1
		}
		para = append(para, t)
	}
	flush()
	return out
}

func sentences(section, p string, line int) []Statement {
	var out []Statement
	r := []rune(p)
	start := 0
	emit := func(end int) {
		s := strings.TrimSpace(string(r[start:end]))
		start = end
		if s == "" {
			return
		}
		var cites []string
		for _, m := range citeRe.FindAllStringSubmatch(s, -1) {
			cites = append(cites, m[1])
		}
		text := strings.TrimSpace(flagMark.ReplaceAllString(citeRe.ReplaceAllString(s, ""), ""))
		if text == "" {
			if len(out) > 0 { // a stray marker belongs to the sentence before
				out[len(out)-1].Citations = append(out[len(out)-1].Citations, cites...)
			}
			return
		}
		out = append(out, Statement{Section: section, Text: text, Citations: cites, Line: line})
	}
	inCode := false
	for i := 0; i < len(r); i++ {
		if r[i] == '`' {
			inCode = !inCode
		}
		if inCode || (r[i] != '.' && r[i] != '!' && r[i] != '?') {
			continue
		}
		j := i + 1
		for j < len(r) && (r[j] == '.' || r[j] == '!' || r[j] == '?' || r[j] == '"' || r[j] == ')' || r[j] == '*') {
			j++
		}
		for {
			loc := citeRe.FindStringIndex(string(r[j:]))
			if loc == nil || loc[0] != 0 {
				break
			}
			j += len([]rune(string(r[j:])[:loc[1]]))
		}
		if j < len(r) && r[j] != ' ' {
			continue
		}
		if abbrev.MatchString(string(r[start : i+1])) {
			continue
		}
		emit(j)
		i = j
	}
	emit(len(r))
	return out
}

// Build turns the statements of a draft into claims, checking each citation
// and whether each fact is in a cited passage.
func (l *Ledger) Build(md string) {
	l.Claims = nil
	for _, st := range Statements(md) {
		l.Claims = append(l.Claims, l.claimOf(st, len(l.Claims)+1))
	}
}

func (l *Ledger) claimOf(st Statement, n int) Claim {
	c := Claim{ID: fmt.Sprintf("C%d", n), Section: st.Section, Text: st.Text, Hash: hashOf(st.Text), Code: st.Code, Status: Unverified, Citations: []string{}}
	var cited []string
	for _, id := range st.Citations {
		if s := l.source(id); s != nil {
			c.Citations = append(c.Citations, id)
			cited = append(cited, norm(s.Text))
		} else {
			c.Invalid = append(c.Invalid, id)
		}
	}
	c.Uncited = len(c.Citations) == 0
	if st.Code {
		c.Facts = []Fact{{Kind: "command", Value: strings.TrimSpace(st.Text)}}
	} else {
		c.Facts = ExtractFacts(st.Text)
	}
	for i := range c.Facts {
		v := norm(c.Facts[i].Value)
		for _, s := range cited {
			if strings.Contains(s, v) {
				c.Facts[i].InSource = true
			}
		}
	}
	if l.ev != nil && !st.Code {
		// the page a claim cites can't also be its confirmation
		self := map[string]bool{}
		for _, id := range c.Citations {
			if s := l.source(id); s != nil {
				self[s.File] = true
			}
		}
		l.ev.checkFacts(c.Facts, self)
	}
	c.Related = related(l.ents, st.Text)
	return c
}

// SetRepo makes claims check their facts against a repository and link the
// entities they name to the pages that define them.
func (l *Ledger) SetRepo(ev *Evidence, ents []Entity) {
	l.ev, l.ents = ev, ents
	if ev != nil {
		l.Repo = ev.Root
	}
}

// checkFacts fills Repo and Conflict for each fact the repository can speak to.
func (ev *Evidence) checkFacts(fs []Fact, self map[string]bool) {
	var fields []string
	for _, f := range fs {
		if f.Kind == "field" || f.Kind == "parameter" {
			fields = append(fields, f.Value)
		}
	}
	for i := range fs {
		f := &fs[i]
		var c Check
		switch f.Kind {
		case "flag":
			c = ev.Flag(f.Value)
		case "field", "parameter":
			c = ev.Field(f.Value)
		case "version":
			c = ev.Version(f.Value)
		case "default":
			key := f.Key
			if len(fields) == 1 {
				key = fields[0]
			}
			if key != "" {
				c = ev.Value(key, f.Value)
			}
		case "number":
			if len(fields) == 1 {
				c = ev.Value(fields[0], f.Value)
			} else if f.Key != "" {
				c = ev.Value(f.Key, f.Value)
			}
		}
		f.Repo, f.Conflict = nil, c.Conflict
		for _, loc := range c.Found {
			if !self[loc[:strings.LastIndex(loc, ":")]] {
				f.Repo = append(f.Repo, loc)
			}
		}
	}
	// a number that is also the default inherits the default's key
	for i := range fs {
		if fs[i].Kind == "number" && len(fs[i].Repo) == 0 && fs[i].Conflict == "" {
			for _, d := range fs {
				if d.Kind == "default" && sameValue(d.Value, fs[i].Value) {
					fs[i].Repo, fs[i].Conflict = d.Repo, d.Conflict
				}
			}
		}
	}
}

var wordChars = regexp.MustCompile(`[\w.-]`)

// related finds the entities named in text and the pages that define them.
func related(ents []Entity, text string) []Ref {
	var out []Ref
	lower := strings.ToLower(text)
	for _, e := range ents {
		n := strings.ToLower(e.Name)
		if len(n) < 3 {
			continue
		}
		i := strings.Index(lower, n)
		if i < 0 {
			continue
		}
		before, after := "", ""
		if i > 0 {
			before = lower[i-1 : i]
		}
		if j := i + len(n); j < len(lower) {
			after = lower[j : j+1]
		}
		if wordChars.MatchString(before) || wordChars.MatchString(after) && after != "." {
			continue
		}
		out = append(out, e.Refs...)
		if len(out) >= 6 {
			break
		}
	}
	return out
}

// SyncResult says what changed in draft.md since the ledger was written.
type SyncResult struct {
	Added, Changed, Removed []string
}

// Sync matches the ledger to the current draft.md. Unchanged statements keep
// their review; edited or new statements become unverified claims; claims
// whose text is gone are marked removed.
func (l *Ledger) Sync(md string) SyncResult {
	var res SyncResult
	byHash := map[string][]*Claim{} // the same sentence can appear more than once
	for i := range l.Claims {
		if !l.Claims[i].Removed {
			byHash[l.Claims[i].Hash] = append(byHash[l.Claims[i].Hash], &l.Claims[i])
		}
	}
	matched := map[string]bool{}
	next := 0
	for _, c := range l.Claims {
		var n int
		fmt.Sscanf(c.ID, "C%d", &n)
		next = max(next, n)
	}
	var fresh []Claim
	for _, st := range Statements(md) {
		h := hashOf(st.Text)
		if q := byHash[h]; len(q) > 0 {
			c := q[0]
			byHash[h] = q[1:]
			matched[c.ID] = true
			// citations may have been edited without the text changing
			nc := l.claimOf(st, 0)
			if strings.Join(nc.Citations, ",") != strings.Join(c.Citations, ",") {
				nc.ID = c.ID
				fresh = append(fresh, nc)
				res.Changed = append(res.Changed, c.ID)
				continue
			}
			fresh = append(fresh, *c)
			continue
		}
		next++
		nc := l.claimOf(st, next)
		fresh = append(fresh, nc)
		res.Added = append(res.Added, nc.ID)
	}
	for _, c := range l.Claims {
		if !matched[c.ID] && !c.Removed {
			c.Removed = true
			fresh = append(fresh, c)
			res.Removed = append(res.Removed, c.ID)
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Removed != fresh[j].Removed && !fresh[i].Removed })
	l.Claims = fresh
	return res
}

// Problems lists why the draft is not ready: claims not verified, rejected
// or needing change, and uncited statements. Removed claims don't count.
func (l *Ledger) Problems() []string {
	var out []string
	for _, c := range l.Claims {
		if c.Removed {
			continue
		}
		switch c.Status {
		case Verified:
		case Rejected:
			out = append(out, c.ID+" was rejected but is still in the draft")
		case NeedsChange:
			out = append(out, c.ID+" needs a change")
		default:
			out = append(out, c.ID+" is not verified")
		}
	}
	return out
}

// Counts returns claims per status, plus "uncited" and "removed".
func (l *Ledger) Counts() map[string]int {
	m := map[string]int{}
	for _, c := range l.Claims {
		if c.Removed {
			m["removed"]++
			continue
		}
		m[c.Status]++
		if c.Uncited {
			m["uncited"]++
		}
	}
	return m
}
