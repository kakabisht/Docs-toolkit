package convert

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixture = "../../testdata/fixtures/convert"

// golden compares got with the file at path, or rewrites it with -update.
func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/convert -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs; run go test ./internal/convert -update and review the diff\n--- got ---\n%s", path, got)
	}
}

// tree returns path -> sha256 for every file under dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		m[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	return m
}

func TestBatchGolden(t *testing.T) {
	src := filepath.Join(fixture, "src")
	before := tree(t, src)
	for _, target := range []string{TargetGFM, TargetMkDocs, TargetDocusaurus, TargetHugo, TargetPlain} {
		t.Run(target, func(t *testing.T) {
			out := t.TempDir()
			rep, err := New(Options{Target: target}).Batch(src, out, false)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(fixture, "expected", target)
			got := tree(t, out)
			var names []string
			for n := range got {
				if strings.HasSuffix(n, ".md") {
					names = append(names, n)
				}
			}
			sort.Strings(names)
			if len(names) != 2 {
				t.Fatalf("want 2 Markdown files, got %v", names)
			}
			for _, n := range names {
				b, _ := os.ReadFile(filepath.Join(out, n))
				golden(t, filepath.Join(want, n), string(b))
			}
			if _, ok := got["guide/images/diagram.png"]; !ok {
				t.Error("referenced image not copied")
			}
			if _, ok := got["partials/_snippet.md"]; ok {
				t.Error("an included file was converted on its own")
			}
			golden(t, filepath.Join(want, "convert-report.md"), rep.Markdown(nil))
		})
	}
	if after := tree(t, src); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Error("the source folder changed")
	}
}

func TestIdempotent(t *testing.T) {
	src, out := filepath.Join(fixture, "src"), t.TempDir()
	if _, err := New(Options{}).Batch(src, out, false); err != nil {
		t.Fatal(err)
	}
	first := tree(t, out)
	rep, err := New(Options{}).Batch(src, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(tree(t, out)) != fmt.Sprint(first) {
		t.Error("second run changed the output")
	}
	for _, f := range rep.Files {
		if f.Skipped == "" && f.Status != "unchanged" {
			t.Errorf("%s: status %q on the second run, want unchanged", f.Source, f.Status)
		}
	}
	if len(rep.Assets) != 1 {
		t.Errorf("assets on the second run: %v", rep.Assets)
	}
}

func TestReplacedFilesAreReported(t *testing.T) {
	src, out := filepath.Join(fixture, "src"), t.TempDir()
	os.MkdirAll(filepath.Join(out, "guide"), 0o755)
	os.WriteFile(filepath.Join(out, "guide", "install.md"), []byte("hand edit\n"), 0o644)
	rep, err := New(Options{}).Batch(src, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.Markdown(nil), "1 existing Markdown files in the output folder were replaced") {
		t.Error("replacing an existing file is not reported")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "md")
	rep, err := New(Options{}).Batch(filepath.Join(fixture, "src"), out, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry run created the output folder")
	}
	if len(rep.Items) == 0 || len(rep.Files) != 3 {
		t.Errorf("dry run should still report: %d items, %d files", len(rep.Items), len(rep.Files))
	}
}

func TestRefusesSourceAsOutput(t *testing.T) {
	src := filepath.Join(fixture, "src")
	if _, err := New(Options{}).Batch(src, src+"/", false); err == nil {
		t.Fatal("converting into the source folder should fail")
	}
}

func convertOne(t *testing.T, opt Options, src string) (string, *Report) {
	t.Helper()
	return New(opt).ConvertString(t.TempDir(), "page.adoc", src)
}

func actions(r *Report, construct string) []string {
	var out []string
	for _, it := range r.Items {
		if it.Construct == construct {
			out = append(out, fmt.Sprintf("%d:%s", it.Line, it.Action))
		}
	}
	return out
}

func TestConditionals(t *testing.T) {
	src := "= T\n\nifeval::[\"{edition}\" == \"enterprise\"]\nEnterprise.\nendif::[]\n\nifdef::beta[Beta only.]\n"
	cases := []struct {
		name    string
		opt     Options
		has     []string
		hasNot  []string
		actions string
	}{
		{"unknown kept", Options{}, []string{"Enterprise."}, []string{"Beta only."}, "[3:review 7:dropped]"},
		{"unknown dropped", Options{Unknown: "drop"}, nil, []string{"Enterprise.", "Beta only."}, "[3:review 7:dropped]"},
		{"attributes given", Options{Attributes: map[string]string{"edition": "enterprise", "beta": ""}}, []string{"Enterprise.", "Beta only."}, nil, "[3:approximated 7:approximated]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, rep := convertOne(t, c.opt, src)
			for _, s := range c.has {
				if !strings.Contains(md, s) {
					t.Errorf("missing %q:\n%s", s, md)
				}
			}
			for _, s := range c.hasNot {
				if strings.Contains(md, s) {
					t.Errorf("unexpected %q:\n%s", s, md)
				}
			}
			if got := fmt.Sprint(actions(rep, "conditional")); got != c.actions {
				t.Errorf("conditional report %s, want %s", got, c.actions)
			}
		})
	}
}

func TestCallouts(t *testing.T) {
	for lang, want := range map[string]string{
		"go":   "x := 1 // (1)",
		"sql":  "SELECT 1 -- (1)",
		"xml":  "<a/> <!-- (1) -->",
		"yaml": "a: 1 # (1)",
	} {
		line := strings.TrimSuffix(strings.SplitN(want, " (1)", 2)[0], " //")
		line = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(line, " --"), " <!--"), " #")
		md, _ := convertOne(t, Options{}, fmt.Sprintf("[source,%s]\n----\n%s <1>\n----\n<1> One.\n", lang, line))
		if !strings.Contains(md, want) || !strings.Contains(md, "- (1) One.") {
			t.Errorf("%s: want %q in\n%s", lang, want, md)
		}
	}
	_, rep := convertOne(t, Options{}, "[source,text]\n----\nx <1>\n----\n<1> One.\n")
	if got := fmt.Sprint(actions(rep, "callouts")); got != "[2:approximated 2:review]" {
		t.Errorf("callouts without comment syntax: %s", got)
	}
}

func TestTables(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"header and alignment", "[cols=\"1,>1\",options=header]\n|===\n|A |B\n|1 |2\n|===\n", "| A | B |\n| --- | ---: |\n| 1 | 2 |"},
		{"implicit header", "|===\n|A |B\n\n|1 |2\n|===\n", "| A | B |\n| --- | --- |\n| 1 | 2 |"},
		{"no header", "[cols=2]\n|===\n|1 |2\n|===\n", "|  |  |\n| --- | --- |\n| 1 | 2 |"},
		{"colspan", "[cols=2]\n|===\n2+|wide\n|a |b\n|===\n", "<td colspan=\"2\">wide</td>\n</tr>\n<tr>\n<td>a</td>\n<td>b</td>"},
		{"rowspan", "[cols=2]\n|===\n.2+|tall |c\n|d\n|===\n", "<td rowspan=\"2\">tall</td>\n<td>c</td>\n</tr>\n<tr>\n<td>d</td>\n</tr>"},
		{"escaped pipe", "[cols=2]\n|===\n|a \\| b |c\n|===\n", "| a \\| b | c |"},
		{"csv", "[format=csv,options=header]\n|===\nA,B\n1,\"x, y\"\n|===\n", "| A | B |\n| --- | --- |\n| 1 | x, y |"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, rep := convertOne(t, Options{}, c.src)
			if !strings.Contains(md, c.want) {
				t.Errorf("want\n%s\nin\n%s", c.want, md)
			}
			for _, it := range rep.Items {
				if strings.Contains(it.Message, "fewer cells") {
					t.Errorf("unexpected: %s", it.Message)
				}
			}
		})
	}
}

func TestInline(t *testing.T) {
	cases := map[string]string{
		"E=mc^2^ and H~2~O":           "E=mc<sup>2</sup> and H<sub>2</sub>O",
		"\"`quoted`\" and '`single`'": "“quoted” and ‘single’",
		"use `a<b` here":              "use `a<b` here",
		"a <b> tag":                   "a &lt;b> tag",
		"kbd:[Ctrl+Alt+Del]":          "<kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>Del</kbd>",
		"https://example.test[site]":  "[site](https://example.test)",
		"*bold* and _it_ and #mark#":  "**bold** and _it_ and <mark>mark</mark>",
	}
	for in, want := range cases {
		md, _ := convertOne(t, Options{}, in+"\n")
		if strings.TrimSpace(md) != want {
			t.Errorf("%q: got %q, want %q", in, strings.TrimSpace(md), want)
		}
	}
}

// Cases found by converting the public Asciidoctor documentation.
func TestDogfoodRegressions(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"block id", "See <<ex>>.\n\n[#ex,yaml]\n.Example config\n----\na: 1\n----\n", "[Example config](#ex)"},
		{"block id anchor", "[#ex,yaml]\n----\na: 1\n----\n", "<a id=\"ex\"></a>"},
		{"title reference", "See <<Second part>>.\n\n== Second part\n\nText.\n", "[Second part](#second-part)"},
		{"code in xref text", "See <<a,the `x` option>>.\n\n[[a]]\n== A\n", "[the `x` option](#a)"},
		{"literal after list item", ". Run:\n\n $ tool run\n+\nor\n\n $ tool go\n\nAfter.\n", "1. Run:\n\n   ```\n   $ tool run\n   ```\n\n   or\n\n   ```\n   $ tool go\n   ```\n\nAfter."},
		{"title on literal", ".Usage\n $ tool\n", "**Usage**\n\n```\n$ tool\n```"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, _ := convertOne(t, Options{}, c.src)
			if !strings.Contains(md, c.want) {
				t.Errorf("want\n%s\nin\n%s", c.want, md)
			}
		})
	}
}

func TestAntoraComponentAttributes(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "modules", "ROOT", "pages"), 0o755)
	os.WriteFile(filepath.Join(src, "antora.yml"), []byte("name: x\nversion: '1'\nasciidoc:\n  attributes:\n    product: Lumen@\n    hidden: false\n"), 0o644)
	os.WriteFile(filepath.Join(src, "modules", "ROOT", "nav.adoc"), []byte("* xref:index.adoc[]\n"), 0o644)
	os.WriteFile(filepath.Join(src, "modules", "ROOT", "pages", "index.adoc"), []byte("= Home\n\n{product} here.\n\nifdef::hidden[Hidden.]\n"), 0o644)
	out := t.TempDir()
	if _, err := New(Options{}).Batch(src, out, false); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(filepath.Join(out, "modules", "ROOT", "pages", "index.md"))
	if !strings.Contains(string(page), "Lumen here.") || strings.Contains(string(page), "Hidden.") {
		t.Errorf("component attributes not applied:\n%s", page)
	}
	nav, _ := os.ReadFile(filepath.Join(out, "modules", "ROOT", "nav.md"))
	if !strings.Contains(string(nav), "[Home](pages/index.md)") {
		t.Errorf("nav xref not resolved against the module's pages:\n%s", nav)
	}
}
