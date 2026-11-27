package gaps

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Page is one documentation page as the gap finder needs it.
type Page struct {
	File  string // relative to the repository root
	Title string
	Text  string
}

// Loc is a place in a file.
type Loc struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text,omitempty"`
}

// Change is what a git diff did to an item.
type Change struct {
	Status     string `json:"status"` // added, changed, removed
	OldDefault string `json:"old_default,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line,omitempty"` // in the old file for removed items
}

// Gap is one item the docs miss or describe out of date.
type Gap struct {
	Type   string  `json:"type"` // undocumented, outdated (default changed), stale (removed from code)
	Item   Item    `json:"item"`
	Change *Change `json:"change,omitempty"`
	// Mentions are the doc lines that name the item: none for undocumented,
	// the lines to fix for outdated and stale.
	Mentions []Loc `json:"mentions,omitempty"`
}

// Group is a set of gaps one page can close: the unit the agent drafts.
type Group struct {
	ID     string `json:"id"`
	Topic  string `json:"topic"`
	Mode   string `json:"mode"`   // new (a new page) or update (an existing one)
	Target string `json:"target"` // page to create or update, relative to the repository root
	Reason string `json:"reason"`
	Gaps   []Gap  `json:"gaps"`
	Score  int    `json:"priority"`
}

// Report is everything Find returns.
type Report struct {
	Since    string         `json:"since,omitempty"`
	Items    int            `json:"surface_items"`
	ByKind   map[string]int `json:"surface_by_kind"`
	Covered  int            `json:"documented"`
	Gaps     []Gap          `json:"gaps"`
	Groups   []Group        `json:"groups"`
	Surface  []Item         `json:"-"`
	Warnings []string       `json:"warnings,omitempty"`
}

// Options configures Find.
type Options struct {
	Root    string // repository root
	Content string // content root relative to Root, for suggested new pages
	Since   string // git ref: also report what changed since it
	Pages   []Page
	Surface []Item // already read; Find reads Root when nil
}

// Find compares the code's surface with the pages.
func Find(opt Options) (*Report, error) {
	items := opt.Surface
	if items == nil {
		var err error
		if items, err = Surface(opt.Root); err != nil {
			return nil, err
		}
	}
	r := &Report{Since: opt.Since, Items: len(items), ByKind: map[string]int{}, Surface: items}
	var changes map[string]*Change
	var removed []Item
	if opt.Since != "" {
		var err error
		changes, removed, err = Diff(opt.Root, opt.Since, items)
		if err != nil {
			return nil, err
		}
	}
	for _, it := range items {
		r.ByKind[it.Kind]++
		mentions := Mentions(opt.Pages, it)
		ch := changes[key(it)]
		switch {
		case len(mentions) == 0:
			r.Gaps = append(r.Gaps, Gap{Type: "undocumented", Item: it, Change: ch})
		case ch != nil && ch.Status == "changed":
			// the lines that still give the old default, or all mentions when none does
			var old []Loc
			for _, m := range mentions {
				if ch.OldDefault != "" && containsWord(m.Text, ch.OldDefault) {
					old = append(old, m)
				}
			}
			if len(old) > 0 {
				r.Gaps = append(r.Gaps, Gap{Type: "outdated", Item: it, Change: ch, Mentions: old})
			} else {
				r.Covered++
			}
		default:
			r.Covered++
		}
	}
	for _, it := range removed {
		if m := Mentions(opt.Pages, it); len(m) > 0 {
			r.Gaps = append(r.Gaps, Gap{Type: "stale", Item: it, Change: changes[key(it)], Mentions: m})
		}
	}
	opt.Surface = items
	r.Groups = group(r.Gaps, opt)
	return r, nil
}

func key(it Item) string { return it.Kind + "\x00" + it.Name }

func containsWord(text, w string) bool {
	return regexp.MustCompile(`(?:^|[^\w.-])` + regexp.QuoteMeta(w) + `(?:[^\w-]|$)`).MatchString(text)
}

// pattern returns how an item is recognised on a page.
func pattern(it Item) (*regexp.Regexp, bool) {
	switch it.Kind {
	case "flag":
		return regexp.MustCompile(regexp.QuoteMeta(it.Name) + `(?:[^\w-]|$)`), false
	case "env":
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(it.Name) + `\b`), false
	case "endpoint":
		p := it.Name
		if i := strings.IndexByte(p, ' '); i >= 0 {
			p = p[i+1:]
		}
		if p == "/" || p == "" {
			return nil, false
		}
		var b strings.Builder
		for i, seg := range strings.Split(strings.Trim(p, "/"), "/") {
			if i > 0 || strings.HasPrefix(p, "/") {
				b.WriteString("/")
			}
			if strings.HasPrefix(seg, "{") {
				b.WriteString(`(?:\{[^}/]+\}|:\w+|<[^>/]+>|[\w.-]+)`)
			} else {
				b.WriteString(regexp.QuoteMeta(seg))
			}
		}
		return regexp.MustCompile(`(?:^|[^\w/])` + b.String() + `(?:[^\w/-]|$)`), false
	case "config":
		leaf := it.Name
		if i := strings.LastIndexByte(leaf, '.'); i >= 0 {
			leaf = leaf[i+1:]
		}
		// the full dotted path anywhere, or the key where docs show keys:
		// code spans, YAML lines and tables
		return regexp.MustCompile(`(?:^|[^\w.-])(?:` + regexp.QuoteMeta(it.Name) + `|` + regexp.QuoteMeta(leaf) + `)(?:[^\w-]|$)`), true
	}
	return nil, false
}

var keyContext = regexp.MustCompile("`|^\\s*[\\w.-]+\\s*:|\\|")

// Mentions returns the page lines that name the item.
func Mentions(pages []Page, it Item) []Loc {
	re, needContext := pattern(it)
	if re == nil {
		return nil
	}
	var out []Loc
	for _, p := range pages {
		sc := bufio.NewScanner(strings.NewReader(p.Text))
		sc.Buffer(make([]byte, 256<<10), 256<<10)
		n := 0
		for sc.Scan() {
			n++
			l := sc.Text()
			if !re.MatchString(l) {
				continue
			}
			if needContext && !strings.Contains(l, it.Name) && !keyContext.MatchString(l) {
				continue
			}
			out = append(out, Loc{File: p.File, Line: n, Text: strings.TrimSpace(l)})
		}
	}
	return out
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Diff reads git diff since ref and classifies items: added (new name),
// changed (same name, other default) and removed (the name is gone from the
// code). Removed items are returned with their old file and line.
func Diff(root, ref string, current []Item) (map[string]*Change, []Item, error) {
	cmd := exec.Command("git", "-C", root, "diff", "--unified=0", "--no-color", "--no-ext-diff", ref, "--")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("git diff %s: %v: %s", ref, err, strings.TrimSpace(stderr.String()))
	}
	// git paths are relative to the top level; root may be a subfolder
	top := root
	if b, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output(); err == nil {
		top = strings.TrimSpace(string(b))
	}
	absRoot, _ := filepath.Abs(root)
	if r, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = r
	}
	if r, err := filepath.EvalSymlinks(top); err == nil {
		top = r
	}
	prefix, _ := filepath.Rel(top, absRoot)
	prefix = filepath.ToSlash(prefix)
	rel := func(p string) (string, bool) {
		if prefix == "." || prefix == "" {
			return p, true
		}
		if strings.HasPrefix(p, prefix+"/") {
			return strings.TrimPrefix(p, prefix+"/"), true
		}
		return "", false
	}
	var added, gone []Item
	var oldFile, newFile string
	var oldLine, newLine int
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "--- "):
			oldFile = strings.TrimPrefix(strings.TrimPrefix(l[4:], "a/"), "/dev/null")
		case strings.HasPrefix(l, "+++ "):
			newFile = strings.TrimPrefix(strings.TrimPrefix(l[4:], "b/"), "/dev/null")
		case strings.HasPrefix(l, "@@"):
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				oldLine, _ = strconv.Atoi(m[1])
				newLine, _ = strconv.Atoi(m[2])
			}
		case strings.HasPrefix(l, "+"):
			if f, ok := rel(newFile); ok && surfaceFile(f) {
				for _, it := range lineItems(f, l[1:]) {
					it.Line = newLine
					added = append(added, it)
				}
			}
			newLine++
		case strings.HasPrefix(l, "-"):
			if f, ok := rel(oldFile); ok && surfaceFile(f) {
				for _, it := range lineItems(f, l[1:]) {
					it.Line = oldLine
					gone = append(gone, it)
				}
			}
			oldLine++
		}
	}
	now := map[string]bool{}
	for _, it := range current {
		now[key(it)] = true
	}
	was := map[string]Item{}
	for _, it := range gone {
		was[key(it)] = it
	}
	changes := map[string]*Change{}
	for _, it := range added {
		k := key(it)
		if old, ok := was[k]; ok {
			if old.Default != it.Default {
				changes[k] = &Change{Status: "changed", OldDefault: old.Default, File: it.File, Line: it.Line}
			}
			continue
		}
		changes[k] = &Change{Status: "added", File: it.File, Line: it.Line}
	}
	var removed []Item
	seen := map[string]bool{}
	for _, it := range gone {
		k := key(it)
		if now[k] || seen[k] {
			continue
		}
		seen[k] = true
		changes[k] = &Change{Status: "removed", File: it.File, Line: it.Line}
		removed = append(removed, it)
	}
	return changes, removed, nil
}

func surfaceFile(f string) bool {
	for _, d := range strings.Split(path.Dir(f), "/") {
		if skipDir[d] || strings.HasPrefix(d, ".") && d != "." {
			return false
		}
	}
	name := path.Base(f)
	return !isTest(name) && (codeExt[strings.ToLower(path.Ext(name))] || isConfigFile(f) || name == "Dockerfile" || name == ".env.example")
}

// lineItems extracts items from a single diff line. YAML keys are read
// without their parents, so only top-level and leaf names can be matched.
func lineItems(file, l string) []Item {
	if isConfigFile(file) {
		return fromYAML(file, []byte(strings.TrimSpace(l)))
	}
	if strings.HasPrefix(path.Base(file), ".env") {
		return fromEnvFile(file, l)
	}
	return FromCode(file, l)
}

// group turns gaps into draftable topics: undocumented items by kind and
// defining file, and outdated or stale items by the page that needs fixing.
func group(gs []Gap, opt Options) []Group {
	byKey := map[string]*Group{}
	var order []string
	add := func(k string, g Group, gap Gap) {
		if byKey[k] == nil {
			cp := g
			byKey[k] = &cp
			order = append(order, k)
		}
		byKey[k].Gaps = append(byKey[k].Gaps, gap)
	}
	for _, g := range gs {
		switch g.Type {
		case "undocumented":
			file := g.Item.File
			if g.Item.Kind == "env" || g.Item.Kind == "endpoint" {
				file = "" // one page per kind reads better than one per file
			}
			add("new\x00"+g.Item.Kind+"\x00"+file, Group{Mode: "new", Topic: topicFor(g.Item.Kind, file)}, g)
		default:
			page := g.Mentions[0].File
			add("update\x00"+page, Group{Mode: "update", Target: page, Topic: "Update " + page + " for code changes"}, g)
		}
	}
	var out []Group
	for _, k := range order {
		g := byKey[k]
		if g.Mode == "new" {
			g.Target, g.Reason = targetFor(*g, opt)
			if g.Target != "" && strings.HasPrefix(g.Reason, "existing") {
				g.Mode = "update"
			}
		} else {
			g.Reason = fmt.Sprintf("%d mention(s) of changed or removed items", countMentions(g.Gaps))
		}
		g.Score = priority(*g)
		out = append(out, *g)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	for i := range out {
		out[i].ID = fmt.Sprintf("G%d", i+1)
	}
	return out
}

func countMentions(gs []Gap) int {
	n := 0
	for _, g := range gs {
		n += len(g.Mentions)
	}
	return n
}

// priority: removed and changed behaviour first (the docs are wrong), then
// new items, then the rest; endpoints and flags before configuration keys.
func priority(g Group) int {
	weight := map[string]int{"endpoint": 4, "flag": 3, "env": 2, "config": 1}
	s := 0
	for _, gap := range g.Gaps {
		w := weight[gap.Item.Kind]
		switch {
		case gap.Type == "stale":
			s += 10 * w
		case gap.Type == "outdated":
			s += 8 * w
		case gap.Change != nil && gap.Change.Status == "added":
			s += 5 * w
		default:
			s += w
		}
	}
	return s
}

func topicFor(kind, file string) string {
	stem := strings.TrimSuffix(path.Base(file), path.Ext(file))
	switch kind {
	case "flag":
		if stem == "" || stem == "main" || stem == "root" || stem == "flags" {
			return "Command-line flags"
		}
		return "Flags of the " + stem + " command"
	case "env":
		return "Environment variables"
	case "endpoint":
		return "HTTP API endpoints"
	default:
		if file == "" {
			return "Configuration settings"
		}
		return "Configuration settings in " + file
	}
}

// targetFor picks the page to update: the one that already documents most of
// the items defined next to these (same kind, same file). Without one, it
// suggests a new reference page under the content root.
func targetFor(g Group, opt Options) (string, string) {
	it := g.Gaps[0].Item
	var siblings []Item
	missing := map[string]bool{}
	for _, gap := range g.Gaps {
		missing[gap.Item.Name] = true
	}
	for _, s := range opt.Surface {
		if s.Kind == it.Kind && (s.File == it.File || it.Kind == "env" || it.Kind == "endpoint") && !missing[s.Name] {
			siblings = append(siblings, s)
		}
	}
	count := map[string]int{}
	for _, s := range siblings {
		seen := map[string]bool{}
		for _, m := range Mentions(opt.Pages, s) {
			if !seen[m.File] {
				count[m.File]++
				seen[m.File] = true
			}
		}
	}
	best, n := "", 0
	for f, c := range count {
		if c > n || c == n && f < best {
			best, n = f, c
		}
	}
	if best != "" {
		return best, fmt.Sprintf("existing page documents %d of the other %s items defined alongside", n, it.Kind)
	}
	slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(g.Topic), "-"), "-")
	return path.Join(strings.Trim(filepath.ToSlash(opt.Content), "/"), "reference", slug+".md"), "no page documents these or the items defined alongside them"
}
