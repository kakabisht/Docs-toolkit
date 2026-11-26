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
	if code, out := run(t, "agent-ready", "check", site, "--afdocs", "off", "--fail-under", "99"); code != ExitFindings {
		t.Errorf("exit %d: %s", code, out)
	}
	if code, out := run(t, "agent-ready", "check", site, "--afdocs", "off"); code != ExitOK {
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

func TestAudit(t *testing.T) {
	fx := "../testdata/fixtures/graph"
	code, out := run(t, "audit", fx, "-f", "json", "--tasks", fx+"/tasks.yaml")
	if code != ExitOK || !json.Valid([]byte(out)) || !strings.Contains(out, `"fix_first"`) {
		t.Errorf("audit json: %d %.200s", code, out)
	}
	if code, out := run(t, "audit", "structure", fx); code != ExitOK || !strings.HasPrefix(out, "# Docs audit") || strings.Contains(out, "## Findability") {
		t.Errorf("audit structure: %d %.200s", code, out)
	}
	if code, _ := run(t, "audit", fx, "--fail-under", "99"); code != ExitFindings {
		t.Errorf("fail-under: exit %d", code)
	}
	if code, _ := run(t, "audit", "architecture"); code != ExitError {
		t.Errorf("architecture: exit %d", code)
	}
}

func TestAgentReadyCappedAndSampled(t *testing.T) {
	fx, _ := filepath.Abs("../testdata/fixtures/agent-ready")
	dir := t.TempDir()
	argsOut := filepath.Join(dir, "args.txt")
	cfg := filepath.Join(dir, "docsbot.yml")
	os.WriteFile(cfg, []byte("base_url: https://docs.example.test\naudit:\n  afdocs_cmd: sh "+fx+"/fake-afdocs.sh\n"), 0o644)
	t.Setenv("AFDOCS_FIXTURE", fx+"/capped.json")
	t.Setenv("AFDOCS_ARGS_OUT", argsOut)
	code, out := run(t, "agent-ready", "check", "../testdata/fixtures/graph", "--config", cfg, "--afdocs", "on", "--sample", "5")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Blocking issue  No llms.txt found") || !strings.Contains(out, "before the cap") {
		t.Errorf("capped score should lead with the blocker:\n%s", out)
	}
	if strings.Contains(out, "59 / 100") {
		t.Errorf("capped number headlined:\n%s", out)
	}
	if !strings.Contains(out, "indicative: 5 of 8") || !strings.Contains(out, "Small sample") {
		t.Errorf("sample label or warning missing:\n%s", out)
	}
	args, _ := os.ReadFile(argsOut)
	if !strings.Contains(string(args), "--urls\nhttps://docs.example.test/") {
		t.Errorf("afdocs did not get docsbot's sample:\n%s", args)
	}
}

func TestConvert(t *testing.T) {
	src := "../testdata/fixtures/convert/src"
	if code, _ := run(t, "convert", src, "--out", src); code != ExitError {
		t.Errorf("converting into the source folder: exit %d, want %d", code, ExitError)
	}
	if code, _ := run(t, "convert", src); code != ExitError {
		t.Errorf("missing --out: exit %d, want %d", code, ExitError)
	}
	dir := t.TempDir()
	code, out := run(t, "convert", src, "--out", dir, "--check", "-f", "json")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var res struct {
		Report struct {
			Items []struct{ Action string } `json:"items"`
		} `json:"report"`
		Links []struct{ Rule string } `json:"link_findings"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Report.Items) == 0 || len(res.Links) != 1 || res.Links[0].Rule != "link-missing-anchor" {
		t.Errorf("unexpected result: %+v", res)
	}
	for _, f := range []string{"convert-report.md", "convert-report.json", "guide/install.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
}
