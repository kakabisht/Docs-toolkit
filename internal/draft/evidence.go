package draft

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Evidence is the repository outside the prose: code, configuration,
// schemas and reference tables, read line by line so the facts in a draft can
// be checked against what the product actually does.
type Evidence struct {
	Root  string
	lines []evLine
}

type evLine struct {
	file string
	n    int
	text string
	kind string // code, config, schema, table
}

var evExt = map[string]string{
	".go": "code", ".py": "code", ".js": "code", ".mjs": "code", ".ts": "code", ".rs": "code", ".java": "code", ".kt": "code",
	".rb": "code", ".sh": "code", ".c": "code", ".h": "code", ".cpp": "code", ".cs": "code", ".php": "code", ".swift": "code",
	".yaml": "config", ".yml": "config", ".toml": "config", ".ini": "config", ".conf": "config", ".cfg": "config", ".properties": "config",
	".env": "config", ".json": "config", ".proto": "schema", ".xsd": "schema", ".graphql": "schema",
}

var skipDir = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"public": true, "site": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true, ".docusaurus": true, "drafts": true}

// LoadEvidence reads code, configuration and schema files under root, and the
// table rows of Markdown files, skipping build output and dependencies.
func LoadEvidence(root string) (*Evidence, error) {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	ev := &Evidence{Root: root}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (skipDir[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(d.Name())
		ext := filepath.Ext(name)
		kind := evExt[ext]
		switch {
		case strings.Contains(name, "schema") && (ext == ".json" || ext == ".yaml" || ext == ".yml"):
			kind = "schema"
		case name == "makefile" || name == "dockerfile":
			kind = "code"
		case ext == ".md" || ext == ".mdx" || ext == ".adoc":
			kind = "table"
		case strings.HasSuffix(name, ".lock") || name == "package-lock.json" || name == "go.sum":
			kind = ""
		}
		if kind == "" {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 512<<10 {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 256<<10), 256<<10)
		n := 0
		for sc.Scan() {
			n++
			t := sc.Text()
			if kind == "table" && !strings.HasPrefix(strings.TrimSpace(t), "|") {
				continue
			}
			if strings.TrimSpace(t) == "" {
				continue
			}
			ev.lines = append(ev.lines, evLine{rel, n, t, kind})
		}
		return nil
	})
	return ev, err
}

// Check is what the repository says about one fact.
type Check struct {
	Found    []string // file:line where it is confirmed
	Conflict string   // the repository gives another value
}

var valueAfter = regexp.MustCompile(`^\s*["']?\s*[:=]\s*["']?([^"',#\s}]+)`)

// leaf is the last part of a dotted or slashed name.
func leaf(name string) string {
	name = strings.Trim(name, "`\"' ")
	if i := strings.LastIndexAny(name, "./"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func keyRe(k string) *regexp.Regexp {
	q := regexp.QuoteMeta(k)
	// a key in YAML/TOML/JSON/INI, a struct tag, or a table cell
	return regexp.MustCompile(`(?i)(?:^|[\s"'{,|(])` + q + `["']?\s*[:=]|(?:yaml|json|toml|mapstructure|env):"` + q + `[",]|\|\s*` + "`?" + q + "`?" + `\s*\|`)
}

// Field confirms that a configuration key or parameter exists.
func (ev *Evidence) Field(name string) Check {
	var c Check
	k := leaf(name)
	if len(k) < 2 {
		return c
	}
	re := keyRe(k)
	for _, l := range ev.lines {
		if l.kind != "code" && re.MatchString(l.text) || l.kind == "code" && strings.Contains(l.text, `"`+k+`"`) {
			c.Found = append(c.Found, fmt.Sprintf("%s:%d", l.file, l.n))
		}
	}
	c.Found = top(c.Found)
	return c
}

// Flag confirms a command-line flag is defined in code or documented in a
// reference table.
func (ev *Evidence) Flag(flag string) Check {
	var c Check
	name := strings.TrimLeft(strings.SplitN(flag, "=", 2)[0], "-")
	if len(name) < 2 {
		return c
	}
	quoted := regexp.MustCompile(`["'` + "`" + `]` + regexp.QuoteMeta(name) + `["'` + "`" + `]`)
	dashed := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `\b`)
	for _, l := range ev.lines {
		lower := strings.ToLower(l.text)
		flagCtx := strings.Contains(lower, "flag") || strings.Contains(lower, "option") || strings.Contains(lower, "argument") || strings.Contains(lower, "arg(")
		if dashed.MatchString(l.text) && l.kind != "config" || quoted.MatchString(l.text) && flagCtx {
			c.Found = append(c.Found, fmt.Sprintf("%s:%d", l.file, l.n))
		}
	}
	c.Found = top(c.Found)
	return c
}

// Value checks the value a statement gives for field (a default or a number)
// against the repository: on the key's line, or as a "default" within the next
// few lines (JSON Schema, OpenAPI, struct comments).
func (ev *Evidence) Value(field, value string) Check {
	var c Check
	k := leaf(field)
	want := strings.Trim(strings.ToLower(value), "`\"' ")
	if len(k) < 2 || want == "" {
		return c
	}
	re := keyRe(k)
	defRe := regexp.MustCompile(`(?i)["']?default["']?\s*[:=]\s*["']?([^"',#\s}]+)`)
	var other []string
	for i, l := range ev.lines {
		if l.kind == "code" && !strings.Contains(l.text, `"`+k+`"`) || l.kind != "code" && !re.MatchString(l.text) {
			continue
		}
		var got string
		loc := re.FindStringIndex(l.text)
		if loc != nil {
			if m := valueAfter.FindStringSubmatch(l.text[loc[1]-1:]); m != nil {
				got = m[1]
			}
		}
		if l.kind == "table" {
			got = tableValue(l.text, want)
		}
		if got == "" || got == "{" || got == "[" || strings.EqualFold(got, "object") {
			for j := i + 1; j < len(ev.lines) && j <= i+6 && ev.lines[j].file == l.file; j++ {
				if m := defRe.FindStringSubmatch(ev.lines[j].text); m != nil {
					got, l = m[1], ev.lines[j]
					break
				}
			}
		}
		if got == "" {
			continue
		}
		loc2 := fmt.Sprintf("%s:%d", l.file, l.n)
		if sameValue(got, want) {
			c.Found = append(c.Found, loc2)
		} else {
			other = append(other, fmt.Sprintf("%s at %s", got, loc2))
		}
	}
	c.Found = top(c.Found)
	if len(c.Found) == 0 && len(other) > 0 {
		c.Conflict = strings.Join(top(other), "; ")
	}
	return c
}

// tableValue returns want if a cell of the row equals it, else the last cell.
func tableValue(row, want string) string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	for _, cell := range cells {
		if sameValue(strings.Trim(strings.TrimSpace(cell), "`"), want) {
			return want
		}
	}
	return ""
}

var unitless = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([a-z%]*)$`)

func sameValue(a, b string) bool {
	a, b = strings.ToLower(strings.Trim(a, "`\"' ")), strings.ToLower(strings.Trim(b, "`\"' "))
	if a == b {
		return true
	}
	ma, mb := unitless.FindStringSubmatch(a), unitless.FindStringSubmatch(b)
	// 30 and 30s, 2 and 2 GB: the number agrees and one side has no unit
	return ma != nil && mb != nil && ma[1] == mb[1] && (ma[2] == "" || mb[2] == "")
}

// Version confirms a version string appears in the repository.
func (ev *Evidence) Version(v string) Check {
	var c Check
	re := regexp.MustCompile(`(?:^|[^\w.])v?` + regexp.QuoteMeta(strings.TrimPrefix(v, "v")) + `(?:$|[^\w.])`)
	for _, l := range ev.lines {
		if re.MatchString(l.text) {
			c.Found = append(c.Found, fmt.Sprintf("%s:%d", l.file, l.n))
		}
	}
	c.Found = top(c.Found)
	return c
}

func top(s []string) []string {
	sort.Strings(s)
	if len(s) > 3 {
		return s[:3]
	}
	return s
}
