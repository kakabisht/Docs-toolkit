package agentready

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docsbot/docsbot/internal/crawl"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixture = "../../testdata/fixtures/graph"

func load(t *testing.T) *crawl.Site {
	t.Helper()
	s, err := crawl.LoadLocal(fixture, crawl.LocalOptions{NoGit: true, BaseURL: "https://docs.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func treeHash(t *testing.T, dir string) string {
	h := sha256.New()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		fmt.Fprintf(h, "%s\n%x\n", p, sha256.Sum256(b))
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestGenerate(t *testing.T) {
	before := treeHash(t, fixture)
	out := t.TempDir()
	res, err := Generate(load(t), Options{Out: out, Directive: true})
	if err != nil {
		t.Fatal(err)
	}
	if treeHash(t, fixture) != before {
		t.Fatal("generate changed the docs source")
	}
	llms, _ := os.ReadFile(filepath.Join(out, "llms.txt"))
	golden := filepath.Join(fixture, "expected-llms.txt")
	if *update {
		os.WriteFile(golden, llms, 0o644)
	}
	want, _ := os.ReadFile(golden)
	if string(llms) != string(want) {
		t.Errorf("llms.txt differs from %s:\n%s", golden, llms)
	}
	install, _ := os.ReadFile(filepath.Join(out, "install.md"))
	for _, s := range []string{"> For AI agents", "# Install lumen", "Every lumen command reads", "(https://docs.example.test/configure.md)"} {
		if !strings.Contains(string(install), s) {
			t.Errorf("install.md lacks %q:\n%s", s, install)
		}
	}
	if strings.Contains(string(install), "supersedes:") || strings.Contains(string(install), "--8<--") {
		t.Errorf("front matter or include line left in:\n%s", install)
	}
	full, _ := os.ReadFile(filepath.Join(out, "llms-full.txt"))
	if strings.Count(string(full), "For AI agents") != 0 || strings.Count(string(full), "<!-- source:") != res.Pages {
		t.Error("llms-full.txt should hold each page once, without the per-page directive")
	}
	if res.Inlined != 1 || res.Rewritten == 0 || len(res.Optional) != 1 || len(res.NoDesc) != 7 {
		t.Errorf("result %+v", res)
	}
	// idempotent: a second run writes the same bytes
	out2 := t.TempDir()
	Generate(load(t), Options{Out: out2, Directive: true})
	if hashFiles(t, out) != hashFiles(t, out2) {
		t.Error("two runs differ")
	}
}

func hashFiles(t *testing.T, dir string) string {
	var sb strings.Builder
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(&sb, "%s %x\n", rel, sha256.Sum256(b))
		return nil
	})
	return sb.String()
}

func TestGenerateRefusesContentRoot(t *testing.T) {
	s := load(t)
	if _, err := Generate(s, Options{Out: filepath.Join(s.Content, "agent")}); err == nil {
		t.Error("an output folder inside the content root should be refused")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x")
	res, err := Generate(load(t), Options{Out: out, DryRun: true})
	if err != nil || len(res.Files) == 0 {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry run wrote files")
	}
}

func TestMarkdownPath(t *testing.T) {
	for in, want := range map[string]string{"/": "index.md", "/guide/install": "guide/install.md", "/a/": "a.md"} {
		if got := MarkdownPath(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}
