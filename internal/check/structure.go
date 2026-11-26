package check

import (
	"bytes"
	"encoding/json"
	"os"

	"errors"
	"fmt"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/docsbot/docsbot/internal/model"
)

func (c *checker) structure() {
	titles := map[string][]*model.Page{}
	for _, p := range c.s.Pages {
		if p.Err != "" {
			continue
		}
		f := fileOf(p)
		// headings
		prev := 0
		if p.Title != "" {
			prev = 1 // the front matter title renders as the H1
		}
		h1 := 0
		for _, h := range p.Headings {
			if strings.TrimSpace(h.Text) == "" {
				c.e.Add("heading-empty", f, h.Line, 0, "", "Heading has no text", "Remove it or give it a title.")
				continue
			}
			if h.Level == 1 {
				h1++
				if h1 > 1 {
					c.e.Add("heading-multiple-h1", f, h.Line, 0, "", fmt.Sprintf("Second H1 %q", h.Text), "Use one H1 per page; make the others H2.")
				}
			}
			if prev > 0 && h.Level > prev+1 {
				c.e.Add("heading-skipped-level", f, h.Line, 0, "", fmt.Sprintf("H%d %q follows H%d", h.Level, h.Text, prev), fmt.Sprintf("Make it an H%d, or add the missing level.", prev+1))
			}
			prev = h.Level
		}
		// code blocks
		for _, cb := range p.CodeBlocks {
			if cb.Fenced && cb.Lang == "" {
				c.e.Add("code-missing-language", f, cb.Line, 0, "", "Code block has no language", "Add one (```bash, ```yaml, ```text) so readers and agents can tell commands from output.")
			}
		}
		// leftover markers in prose and headings (code is excluded)
		pats := c.opt.TodoPatterns
		if pats == nil {
			pats = DefaultTodo
		}
		scan := func(text string, line int) {
			for _, re := range pats {
				if m := re.FindString(text); m != "" {
					c.e.Add("content-todo", f, line, 0, m, fmt.Sprintf("Leftover marker %q", m), "Finish or remove it before publishing.")
					return
				}
			}
		}
		for _, para := range p.Paragraphs {
			scan(para.Text, para.Line)
		}
		for _, h := range p.Headings {
			scan(h.Text, h.Line)
		}
		if t := strings.ToLower(strings.TrimSpace(p.Title)); t != "" && !p.Draft {
			titles[t] = append(titles[t], p)
		}
	}
	keys := make([]string, 0, len(titles))
	for k := range titles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ps := titles[k]
		if len(ps) < 2 {
			continue
		}
		for _, p := range ps {
			var others []string
			for _, o := range ps {
				if o != p {
					others = append(others, fileOf(o))
				}
			}
			c.e.Add("title-duplicate", fileOf(p), 1, 0, "", fmt.Sprintf("Title %q is also used by %s", p.Title, strings.Join(others, ", ")), "Make titles specific so readers, search and agents can tell the pages apart.")
		}
	}
}

func (c *checker) metadata() {
	for _, p := range c.s.Pages {
		if p.Err != "" || p.RepoPath == "" {
			continue
		}
		f := fileOf(p)
		if p.FrontMatterErr != "" {
			c.e.Add("frontmatter-invalid", f, 1, 0, "", "Front matter does not parse: "+firstLine(p.FrontMatterErr), "")
			continue
		}
		if strings.TrimSpace(p.Title) == "" {
			c.e.Add("frontmatter-missing-title", f, 1, 0, "", "Page has no title", "Add `title:` to the front matter or an H1.")
		}
		if c.opt.Schema != nil && p.Format == "markdown" {
			c.validate(p)
		}
		if !c.opt.Vocab.Empty() {
			for _, tag := range p.Tags {
				name, known, alias := c.opt.Vocab.Canonical(tag)
				line := keyLine(p.Raw, "tags", "keywords")
				switch {
				case !known:
					c.e.Add("tag-not-in-vocabulary", f, line, 0, tag, fmt.Sprintf("Tag %q is not in the vocabulary", tag), "Use an existing term, or add it to the vocabulary file after review.")
				case alias:
					c.e.Add("tag-alias", f, line, 0, tag, fmt.Sprintf("Tag %q is an alias of %q", tag, name), fmt.Sprintf("Use %q.", name))
				}
			}
		}
	}
}

func (c *checker) validate(p *model.Page) {
	// round-trip through JSON so YAML dates and TOML values become JSON types
	b, err := json.Marshal(p.FrontMatter)
	if err != nil {
		return
	}
	var inst any
	if err := json.Unmarshal(b, &inst); err != nil {
		return
	}
	err = c.opt.Schema.Validate(inst)
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return
	}
	for _, leaf := range leaves(ve) {
		key := ""
		if len(leaf.InstanceLocation) > 0 {
			key = leaf.InstanceLocation[0]
		}
		loc := "/" + strings.Join(leaf.InstanceLocation, "/")
		msg := leaf.ErrorKind.LocalizedString(message.NewPrinter(language.English))
		line := 1
		if key != "" {
			line = keyLine(p.Raw, key)
		}
		if missing := missingProps.FindStringSubmatch(msg); missing != nil {
			key = strings.Trim(missing[1], "'")
		}
		c.e.Add("frontmatter-schema", fileOf(p), line, 0, key, fmt.Sprintf("Front matter %s: %s", loc, msg), "")
	}
}

var missingProps = regexp.MustCompile(`missing propert(?:y|ies) (.+)$`)

func leaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// keyLine finds the line of a top-level front matter key, or 1.
func keyLine(raw []byte, keys ...string) int {
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if i > 0 && (strings.TrimSpace(l) == "---" || strings.TrimSpace(l) == "+++") {
			break
		}
		for _, k := range keys {
			if strings.HasPrefix(l, k+":") || strings.HasPrefix(l, k+" =") || strings.HasPrefix(l, k+"=") {
				return i + 1
			}
		}
	}
	return 1
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// CompileSchema loads a JSON Schema file, written in JSON or YAML.
func CompileSchema(file string) (*jsonschema.Schema, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var doc any
	if strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml") {
		var y any
		if err := yaml.Unmarshal(b, &y); err != nil {
			return nil, err
		}
		j, err := json.Marshal(y)
		if err != nil {
			return nil, err
		}
		b = j
	}
	if doc, err = jsonschema.UnmarshalJSON(bytes.NewReader(b)); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("file:///frontmatter.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("file:///frontmatter.schema.json")
}
