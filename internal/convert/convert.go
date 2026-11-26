// Package convert turns AsciiDoc into Markdown natively, without Pandoc, and
// reports every construct it approximated, dropped or could not resolve,
// with the file and line, so nothing is lost silently.
package convert

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/docsbot/docsbot/internal/parse"
)

// Actions recorded in the report.
const (
	Approximated = "approximated" // converted, but the Markdown differs in look or behaviour
	Dropped      = "dropped"      // not in the output
	Review       = "review"       // needs a person: unresolved, ambiguous, or raw markup kept
)

// Target Markdown flavours. They decide admonition and definition-list syntax.
const (
	TargetGFM        = "gfm"        // > [!NOTE] (GitHub, many renderers)
	TargetMkDocs     = "mkdocs"     // !!! note
	TargetDocusaurus = "docusaurus" // :::note
	TargetHugo       = "hugo"       // > [!NOTE] with Hugo's blockquote alerts
	TargetPlain      = "commonmark" // > **Note:** …, bold-term lists
)

// Options configures conversion.
type Options struct {
	Attributes   map[string]string // attribute values, as with asciidoctor -a (override the documents)
	Target       string            // gfm (default), mkdocs, docusaurus, hugo, commonmark
	AnchorStyle  string            // heading slug style of the target site (default github)
	H1           bool              // also write the document title as a level-1 heading
	KeepComments bool              // keep AsciiDoc comments as HTML comments (default true via New)
	Unknown      string            // conditionals that can't be evaluated: keep (default) or drop
}

// Item is one report entry.
type Item struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Construct string `json:"construct"`
	Action    string `json:"action"`
	Message   string `json:"message"`
}

// FileResult is one converted file.
type FileResult struct {
	Source  string `json:"source"`
	Output  string `json:"output"`
	Skipped string `json:"skipped,omitempty"` // why it was not converted on its own
	Status  string `json:"status,omitempty"`  // new, updated or unchanged (empty on a dry run)
}

// Report covers a whole run.
type Report struct {
	Files   []FileResult   `json:"files"`
	Items   []Item         `json:"items"`
	Summary map[string]int `json:"summary"` // "construct/action" -> count
	Assets  []string       `json:"assets_copied,omitempty"`
}

func (r *Report) add(it Item) { r.Items = append(r.Items, it) }

func (r *Report) finish() {
	// the same message for the same line once is enough
	seen := map[Item]bool{}
	items := r.Items[:0]
	for _, it := range r.Items {
		if !seen[it] {
			seen[it] = true
			items = append(items, it)
		}
	}
	r.Items = items
	sort.SliceStable(r.Items, func(i, j int) bool {
		a, b := r.Items[i], r.Items[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Construct < b.Construct
	})
	r.Summary = map[string]int{}
	for _, it := range r.Items {
		r.Summary[it.Construct+"/"+it.Action]++
	}
}

// Counts returns how many items have each action.
func (r *Report) Counts() map[string]int {
	m := map[string]int{}
	for _, it := range r.Items {
		m[it.Action]++
	}
	return m
}

// Converter holds what one run knows across files: attribute values and the
// ids of every section, so cross-file references land on the right anchor.
type Converter struct {
	opt     Options
	root    string                       // source root (batch) or the file's folder
	ids     map[string]map[string]string // source file (rel) -> AsciiDoc id -> Markdown anchor
	titles  map[string]map[string]string // source file (rel) -> id -> section title (for <<id>> with no text)
	docT    map[string]string            // source file -> document title
	report  *Report
	antora  map[string]map[string]string // folder -> attributes from the antora.yml that governs it
	outPath func(rel string) string      // source rel path -> output rel path
}

// New makes a converter.
func New(opt Options) *Converter {
	if opt.Target == "" {
		opt.Target = TargetGFM
	}
	if opt.AnchorStyle == "" {
		opt.AnchorStyle = parse.StyleGitHub
	}
	if opt.Unknown == "" {
		opt.Unknown = "keep"
	}
	return &Converter{opt: opt, ids: map[string]map[string]string{}, antora: map[string]map[string]string{}, titles: map[string]map[string]string{}, docT: map[string]string{}, report: &Report{},
		outPath: func(rel string) string { return strings.TrimSuffix(rel, filepath.Ext(rel)) + ".md" }}
}

// ConvertString converts one document held in memory; name is used in the
// report and to resolve includes relative to dir.
func (c *Converter) ConvertString(dir, name, src string) (string, *Report) {
	c.root = dir
	doc := c.load(name, src)
	c.scan(doc)
	out := c.render(doc)
	c.report.finish()
	return out, c.report
}

// Batch converts every .adoc file under src into out, mirroring paths.
// Files that other files include are not converted on their own. With dryRun
// nothing is written. out must not be src: the source is never changed.
func (c *Converter) Batch(src, out string, dryRun bool) (*Report, error) {
	sa, _ := filepath.Abs(src)
	oa, _ := filepath.Abs(out)
	if sa == oa {
		return nil, fmt.Errorf("output folder is the source folder: choose another --out so the AsciiDoc stays as it is")
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	var files []string
	if !info.IsDir() {
		c.root = filepath.Dir(src)
		files = []string{filepath.Base(src)}
	} else {
		c.root = src
		filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != src && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
					return filepath.SkipDir
				}
				if pa, _ := filepath.Abs(p); pa == oa {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(p), ".adoc") || strings.EqualFold(filepath.Ext(p), ".asciidoc") || strings.EqualFold(filepath.Ext(p), ".asc") {
				rel, _ := filepath.Rel(src, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(files)
	docs := map[string]*document{}
	assets := map[string]bool{}
	included := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		d := c.load(f, string(b))
		docs[f] = d
		for _, inc := range d.includes {
			if _, ok := included[inc]; !ok {
				included[inc] = f
			}
		}
		c.scan(d)
	}
	for _, f := range files {
		if by, ok := included[f]; ok {
			c.report.Files = append(c.report.Files, FileResult{Source: f, Skipped: "included by " + by}) // its content is in the including page
			continue
		}
		md := c.render(docs[f])
		rel := c.outPath(f)
		fr := FileResult{Source: f, Output: rel}
		if dryRun {
			c.report.Files = append(c.report.Files, fr)
			continue
		}
		dst := filepath.Join(out, filepath.FromSlash(rel))
		fr.Status, err = writeIfChanged(dst, []byte(md))
		if err != nil {
			return nil, err
		}
		c.report.Files = append(c.report.Files, fr)
		for _, a := range docs[f].assets {
			from := filepath.Join(filepath.Dir(filepath.Join(c.root, filepath.FromSlash(f))), filepath.FromSlash(a))
			to := filepath.Join(filepath.Dir(dst), filepath.FromSlash(a))
			b, err := os.ReadFile(from)
			if err != nil {
				continue // a missing image is reported by --check
			}
			if _, err := writeIfChanged(to, b); err != nil {
				return nil, err
			}
			r, _ := filepath.Rel(out, to)
			assets[filepath.ToSlash(r)] = true
		}
	}
	for a := range assets {
		c.report.Assets = append(c.report.Assets, a)
	}
	sort.Strings(c.report.Assets)
	c.report.finish()
	return c.report, nil
}

// writeIfChanged writes b to path unless the file already holds exactly b,
// so a second run over the same input touches nothing.
func writeIfChanged(path string, b []byte) (string, error) {
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, b):
		return "unchanged", nil
	case err == nil:
		return "updated", os.WriteFile(path, b, 0o644)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return "new", os.WriteFile(path, b, 0o644)
}

// Markdown renders the report for convert-report.md. links are problems
// found in the output by --check, one line each.
func (r *Report) Markdown(links []string) string {
	var b strings.Builder
	b.WriteString("# Conversion report\n\n")
	converted, skipped := 0, 0
	status := map[string]int{}
	for _, fr := range r.Files {
		if fr.Skipped != "" {
			skipped++
			continue
		}
		converted++
		status[fr.Status]++
	}
	counts := r.Counts()
	fmt.Fprintf(&b, "%d files converted, %d included by others and not converted on their own. %d items approximated, %d dropped, %d to review.\n",
		converted, skipped, counts[Approximated], counts[Dropped], counts[Review])
	if status["updated"] > 0 {
		fmt.Fprintf(&b, "\n%d existing Markdown files in the output folder were replaced. Any hand edits to them are gone; keep edits in the AsciiDoc or in a separate folder.\n", status["updated"])
	}
	b.WriteString("\nApproximated: converted, but the Markdown looks or behaves differently. Dropped: not in the output. Review: needs a person.\n")
	if len(r.Items) > 0 {
		b.WriteString("\n| File | Line | Construct | Action | Detail |\n| --- | ---: | --- | --- | --- |\n")
		for _, it := range r.Items {
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", it.File, it.Line, it.Construct, it.Action, strings.ReplaceAll(it.Message, "|", `\|`))
		}
	}
	b.WriteString("\n## Files\n\n")
	for _, fr := range r.Files {
		if fr.Skipped != "" {
			fmt.Fprintf(&b, "- %s: not converted on its own (%s)\n", fr.Source, fr.Skipped)
			continue
		}
		fmt.Fprintf(&b, "- %s → %s\n", fr.Source, fr.Output)
	}
	if len(r.Assets) > 0 {
		b.WriteString("\n## Assets copied\n\n")
		for _, a := range r.Assets {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	if links != nil {
		b.WriteString("\n## Links and images in the output\n\n")
		if len(links) == 0 {
			b.WriteString("No problems found.\n")
		}
		for _, l := range links {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	return b.String()
}

// idByTitle finds a section in file whose title is title, for <<Title>>.
func (c *Converter) idByTitle(file, title string) (string, bool) {
	for id, t := range c.titles[file] {
		if strings.EqualFold(stripInline(t), strings.TrimSpace(title)) {
			return id, true
		}
	}
	return "", false
}

// componentAttrs returns the asciidoc.attributes of the antora.yml that
// governs file: the nearest one in its folder or above, up to the source
// root. A value of false unsets; a trailing @ (soft set) is dropped, since the
// document may override any of them here.
func (c *Converter) componentAttrs(file string) map[string]string {
	dir := path.Dir(file)
	for {
		if a, ok := c.antora[dir]; ok {
			return a
		}
		b, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(dir), "antora.yml"))
		if err == nil {
			var desc struct {
				Asciidoc struct {
					Attributes map[string]any `yaml:"attributes"`
				} `yaml:"asciidoc"`
			}
			attrs := map[string]string{}
			if yaml.Unmarshal(b, &desc) != nil {
				c.report.add(Item{File: path.Join(dir, "antora.yml"), Line: 1, Construct: "antora.yml", Action: Review, Message: "could not read antora.yml; its attributes are not applied"})
			}
			for k, v := range desc.Asciidoc.Attributes {
				switch x := v.(type) {
				case bool:
					if x {
						attrs[k] = ""
					}
				case nil:
					attrs[k] = ""
				default:
					attrs[k] = strings.TrimSuffix(fmt.Sprint(x), "@")
				}
			}
			c.antora[dir] = attrs
			return attrs
		}
		if dir == "." || dir == "/" || dir == "" {
			c.antora[dir] = nil
			return nil
		}
		dir = path.Dir(dir)
	}
}
