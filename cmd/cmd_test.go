package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	gf = globalFlags{}
	root := newRoot()
	var out bytes.Buffer
	tmp := filepath.Join(t.TempDir(), "out")
	root.SetArgs(append(args, "-q", "-o", tmp))
	root.SetOut(&out)
	err := root.Execute()
	b, _ := os.ReadFile(tmp)
	if err == nil {
		return ExitOK, string(b)
	}
	if ee, ok := err.(*exitError); ok {
		return ee.code, string(b)
	}
	return ExitError, err.Error()
}

const site = "../testdata/site"

func TestCheckExitCodes(t *testing.T) {
	broken := "../testdata/fixtures/broken"
	if code, out := run(t, "check", broken, "--offline"); code != ExitFindings {
		t.Errorf("check exit %d: %s", code, out)
	}
	if code, out := run(t, "check", broken, "--offline", "--fail-level", "off"); code != ExitOK {
		t.Errorf("--fail-level off exit %d: %s", code, out)
	}
	if code, out := run(t, "check", "../testdata/fixtures/clean", "--offline"); code != ExitOK {
		t.Errorf("clean exit %d: %s", code, out)
	}
	if code, out := run(t, "links", site, "--offline"); code != ExitFindings {
		t.Errorf("links exit %d: %s", code, out)
	}
	if code, _ := run(t, "check", broken, "--only", "nonsense"); code != ExitError {
		t.Errorf("bad --only exit %d", code)
	}
	code, out := run(t, "check", broken, "--offline", "-f", "json", "--fail-level", "off")
	var rep struct {
		Summary  map[string]int    `json:"summary"`
		Findings []json.RawMessage `json:"findings"`
	}
	if code != ExitOK || json.Unmarshal([]byte(out), &rep) != nil || rep.Summary["error"] == 0 || len(rep.Findings) != rep.Summary["total"] {
		t.Errorf("json: %d %s", code, out)
	}
	for _, f := range []string{"github", "sarif", "junit"} {
		if code, out := run(t, "check", broken, "--offline", "-f", f); code != ExitFindings || len(out) == 0 {
			t.Errorf("%s: exit %d, %d bytes", f, code, len(out))
		}
	}
	// only warnings: fail at warning level, pass at error level
	if code, _ := run(t, "check", broken, "--offline", "--only", "structure", "--rule", "heading-empty=warning", "--rule", "include-missing=warning"); code != ExitOK {
		t.Errorf("warnings-only exit %d", code)
	}
}

func TestAuditFailUnder(t *testing.T) {
	if code, out := run(t, "audit", site, "--afdocs", "off", "--fail-under", "99"); code != ExitFindings {
		t.Errorf("exit %d: %s", code, out)
	}
	if code, out := run(t, "audit", site, "--afdocs", "off"); code != ExitOK {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestGraphBuildStatsExport(t *testing.T) {
	db := filepath.Join(t.TempDir(), "g.db")
	if code, out := run(t, "graph", "build", site, "--db", db); code != ExitOK {
		t.Fatalf("build: %d %s", code, out)
	}
	if code, out := run(t, "graph", "stats", "--db", db, "-f", "json"); code != ExitOK || !json.Valid([]byte(out)) {
		t.Errorf("stats: %d %s", code, out)
	}
	for _, f := range []string{"json", "graphml", "jsonld", "cypher", "mermaid", "dot"} {
		if code, out := run(t, "graph", "export", "--db", db, "-f", f); code != ExitOK || len(out) == 0 {
			t.Errorf("export %s: %d %s", f, code, out)
		}
	}
	if code, out := run(t, "graph", "view", "--db", db); code != ExitOK || !strings.Contains(out, "<canvas") {
		t.Errorf("view: %d", code)
	}
	if code, _ := run(t, "graph", "view", "--db", db, "-f", "mindmap"); code != ExitError {
		t.Errorf("mindmap without --root: exit %d, want %d", code, ExitError)
	}
	if code, _ := run(t, "graph", "export", "--db", db, "-f", "png"); code != ExitError {
		t.Errorf("unknown export format: exit %d", code)
	}
	if code, _ := run(t, "graph", "stats", "--db", filepath.Join(t.TempDir(), "missing.db")); code != ExitError {
		t.Errorf("missing db exit %d", code)
	}
}

func TestGraphExplainAndMindmap(t *testing.T) {
	db := filepath.Join(t.TempDir(), "g.db")
	if code, out := run(t, "graph", "build", "../testdata/fixtures/graph", "--db", db); code != ExitOK {
		t.Fatalf("build: %d %s", code, out)
	}
	code, out := run(t, "graph", "explain", "config file", "--db", db)
	if code != ExitOK || !strings.Contains(out, "docs/glossary.md:11") {
		t.Errorf("explain: %d %s", code, out)
	}
	code, out = run(t, "graph", "view", "--db", db, "--root", "lumen deploy", "-f", "mindmap")
	if code != ExitOK || !strings.HasPrefix(out, "mindmap") || !strings.Contains(out, "documented by") {
		t.Errorf("mindmap: %d %s", code, out)
	}
	if code, _ := run(t, "graph", "explain", "nothing like this", "--db", db); code != ExitError {
		t.Errorf("unknown entity: exit %d", code)
	}
}
