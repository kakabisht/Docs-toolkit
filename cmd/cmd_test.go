package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, as := range []string{"mermaid", "dot", "json", "html", "llms-txt"} {
		if code, out := run(t, "graph", "export", "--db", db, "--as", as); code != ExitOK || len(out) == 0 {
			t.Errorf("export %s: %d %s", as, code, out)
		}
	}
	if code, _ := run(t, "graph", "stats", "--db", filepath.Join(t.TempDir(), "missing.db")); code != ExitError {
		t.Errorf("missing db exit %d", code)
	}
}
