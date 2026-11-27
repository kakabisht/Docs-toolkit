package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/kakabisht/Docs-toolkit/internal/gaps"
	"github.com/kakabisht/Docs-toolkit/internal/mcpctx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	fixture = "../../testdata/fixtures/agent/repo"
	kb      = "../../testdata/fixtures/agent/product-kb"
)

// repoOptions reads a repository the way the agent command does.
func repoOptions(t *testing.T, root string) Options {
	t.Helper()
	s, err := crawl.LoadLocal(root, crawl.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Root: s.Root, Content: s.Content, Date: "2026-11-27", Out: filepath.Join(t.TempDir(), "agent")}
	for _, p := range s.Pages {
		opt.Pages = append(opt.Pages, gaps.Page{File: p.RepoPath, Text: string(p.Raw)})
		opt.Docs = append(opt.Docs, draft.SplitPage(p.RepoPath, string(p.Raw))...)
	}
	if opt.Evidence, err = draft.LoadEvidence(s.Root); err != nil {
		t.Fatal(err)
	}
	return opt
}

// kbContext serves the product knowledge base over in-memory MCP.
func kbContext(t *testing.T) *mcpctx.Context {
	t.Helper()
	srcs, err := draft.LoadKB([]string{kb})
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := mcpctx.NewServer(srcs, "kb", "test").Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	x, err := mcpctx.ConnectTransport(context.Background(), mcpctx.ServerConfig{Name: "product-kb"}, ct, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func byTopic(t *testing.T, run *Run, topic string) Result {
	t.Helper()
	for _, d := range run.Drafts {
		if d.Group.Topic == topic {
			return d
		}
	}
	t.Fatalf("no draft for %q", topic)
	return Result{}
}

func TestRunWithMCPContext(t *testing.T) {
	opt := repoOptions(t, fixture)
	opt.Contexts = []*mcpctx.Context{kbContext(t)}
	opt.Max = 10
	run, err := Execute(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Drafts) != 4 {
		t.Fatalf("drafts: %d", len(run.Drafts))
	}
	for _, d := range run.Drafts {
		if d.Error != "" || !d.Eval.Gate.Passed {
			t.Errorf("%s: %s %+v", d.Group.Topic, d.Error, d.Eval)
		}
		for _, f := range []string{draft.DraftFile, draft.LedgerFile, draft.ReviewFile, MetaFile, EvalFile, EvalReport, ChangesFile} {
			if _, err := os.Stat(filepath.Join(d.Dir, f)); err != nil {
				t.Errorf("%s: %v", d.Dir, err)
			}
		}
	}
	flags := byTopic(t, run, "Flags of the upgrade command")
	md := read(t, filepath.Join(flags.Dir, draft.DraftFile))
	for _, want := range []string{
		"The `--keep-days` flag is defined in cmd/upgrade.go with the default `14`.", // from the code
		"Set it to 0 to keep backups forever.",                                       // from the knowledge base, over MCP
		"lumen upgrade --backup --keep-days 30",                                      // the example, kept with its passage
		"mcp:product-kb/search_docs — product-kb/release-notes.md",                   // attribution in the footnote
	} {
		if !strings.Contains(md, want) {
			t.Errorf("flags draft lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Lumen keeps your workspaces up to date") || strings.Contains(md, "add `--force-upgrade`") {
		t.Errorf("flags draft cites passages about other things:\n%s", md)
	}
	if !strings.Contains(md, "## Examples\n\n<!-- A working example that uses them. -->\n\nSource:") {
		t.Errorf("the example should be in Examples, not Reference:\n%s", md)
	}
	api := read(t, filepath.Join(byTopic(t, run, "HTTP API endpoints").Dir, draft.DraftFile))
	if !strings.Contains(api, `{"workspace": "ws-123", "backup": true}`) || !strings.Contains(api, "returns 404 when the workspace does not exist") {
		t.Errorf("api draft:\n%s", api)
	}
	if len(run.Calls) != 8 { // two queries per group
		t.Errorf("context calls: %+v", run.Calls)
	}
	rep := read(t, filepath.Join(opt.Out, RunReport))
	if !strings.Contains(rep, "| G2 | Flags of the upgrade command | update | docs/reference/flags.md | `--keep-days` |") || !strings.Contains(rep, "docsbot agent approve") {
		t.Errorf("report:\n%s", rep)
	}
	if inside(opt.Out, filepath.Join(opt.Root, opt.Content)) {
		t.Error("drafts went into the docs")
	}
	if _, err := Execute(context.Background(), Options{Root: fixture, Content: "docs", Out: filepath.Join(fixture, "docs", "x")}); err == nil {
		t.Error("an output folder inside the docs was accepted")
	}
}

func TestGateBlocksInventedFlagsAndBrokenLinks(t *testing.T) {
	opt := repoOptions(t, fixture)
	opt.Only = []string{"G2"}
	run, err := Execute(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	dir := run.Drafts[0].Dir
	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, draft.DraftFile)
	md := read(t, p)
	bad := strings.Replace(md, "## Examples", "Add `--turbo` for speed, see [the list](missing.md).[^s1]\n\n```yaml\nkeep: [1, 2\n```\n\n```sh\nlumen upgrade --backup --fast\n```\n\n## Examples", 1)
	os.WriteFile(p, []byte(bad), 0o644)
	e, err := Evaluate(context.Background(), EvalOptions{Dir: dir, Meta: meta, Evidence: opt.Evidence})
	if err != nil {
		t.Fatal(err)
	}
	if e.Gate.Passed || e.Metrics.UnknownRefs != 1 || e.Metrics.BrokenLinks != 1 || e.Metrics.BadExamples != 2 {
		t.Fatalf("gate: %+v\nmetrics %+v\nexamples %+v", e.Gate, e.Metrics, e.Examples)
	}
	joined := strings.Join(e.Gate.Blocking, "\n")
	for _, want := range []string{"--turbo", "broken link", "2 example(s) fail validation"} {
		if !strings.Contains(joined, want) {
			t.Errorf("blocking lacks %q: %s", want, joined)
		}
	}
	if _, err := Approve(dir, "Reviewer", "2026-11-27"); err == nil || !strings.Contains(err.Error(), "gate is blocked") {
		t.Errorf("approve on a blocked gate: %v", err)
	}
	// a draft edited after evaluation can't be approved on the old evaluation
	os.WriteFile(p, []byte(md), 0o644)
	if _, err := Approve(dir, "Reviewer", "2026-11-27"); err == nil || !strings.Contains(err.Error(), "changed since it was evaluated") {
		t.Errorf("approve on a stale evaluation: %v", err)
	}
}

// verifyAll marks every claim verified, as draft verify would.
func verifyAll(t *testing.T, dir string) {
	t.Helper()
	lp := filepath.Join(dir, draft.LedgerFile)
	led, err := draft.ReadLedger(lp)
	if err != nil {
		t.Fatal(err)
	}
	for i := range led.Claims {
		led.Claims[i].Status, led.Claims[i].Reviewer = draft.Verified, "Reviewer"
	}
	if err := led.Write(lp); err != nil {
		t.Fatal(err)
	}
}

func TestApproveAndPublishNewPage(t *testing.T) {
	// a copy of the fixture whose docs name no environment variable, so the
	// group is a new page
	root := t.TempDir()
	if err := exec.Command("cp", "-R", fixture+"/.", root).Run(); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(root, "docs", "index.md")
	os.WriteFile(idx, []byte(strings.Replace(read(t, idx), "Lumen reads its data from the folder in `LUMEN_HOME`. ", "", 1)), 0o644)
	opt := repoOptions(t, root)
	opt.Contexts = []*mcpctx.Context{kbContext(t)}
	opt.Max = 10
	run, err := Execute(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	env := byTopic(t, run, "Environment variables")
	if env.Group.Mode != "new" || env.Group.Target != "docs/reference/environment-variables.md" || !env.Eval.Gate.Passed {
		t.Fatalf("env group: %+v %+v", env.Group, env.Eval.Gate)
	}
	dir := env.Dir
	if _, err := Approve(dir, "Reviewer", "2026-11-27"); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("approve before review: %v", err)
	}
	if _, err := Publish(dir, false); err == nil {
		t.Fatal("published without approval")
	}
	verifyAll(t, dir)
	a, err := Approve(dir, "Reviewer", "2026-11-27")
	if err != nil {
		t.Fatal(err)
	}
	if a.Claims == 0 || a.By != "Reviewer" {
		t.Errorf("approval: %+v", a)
	}
	md := read(t, filepath.Join(dir, draft.DraftFile))
	if !strings.Contains(md, `status: "approved"`) || !strings.Contains(md, `approved_by: "Reviewer"`) {
		t.Errorf("front matter:\n%s", md)
	}
	if again, err := Approve(dir, "Someone else", "2026-11-28"); err != nil || again.By != "Reviewer" {
		t.Errorf("approving again should keep the first approval: %+v %v", again, err)
	}
	target, err := Publish(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	page := read(t, target)
	for _, gone := range []string{"[^s", "status:", "review:", "claims:", "<!--", "Source:", "mcp:"} {
		if strings.Contains(page, gone) {
			t.Errorf("published page still has %q:\n%s", gone, page)
		}
	}
	if !strings.HasPrefix(page, "---\ntitle: \"Environment variables\"\n---\n\n## Reference") || !strings.Contains(page, "LUMEN_LOG_LEVEL") {
		t.Errorf("published page:\n%s", page)
	}
	if _, err := Publish(dir, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("publishing over an existing page: %v", err)
	}
	// editing after approval needs a new evaluation and approval
	os.WriteFile(filepath.Join(dir, draft.DraftFile), []byte(md+"\nOne more line.\n"), 0o644)
	if _, err := Publish(dir, true); err == nil || !strings.Contains(err.Error(), "changed after approval") {
		t.Errorf("publish after an edit: %v", err)
	}
	// an update draft is never published over its page
	flags := byTopic(t, run, "Flags of the upgrade command")
	verifyAll(t, flags.Dir)
	if _, err := Approve(flags.Dir, "Reviewer", "2026-11-27"); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(flags.Dir, true); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Errorf("publish of an update: %v", err)
	}
}

func TestSinceDraftsUseContextNotStaleDocs(t *testing.T) {
	root := changedRepo(t)
	opt := repoOptions(t, root)
	opt.Since = "HEAD~1"
	opt.Contexts = []*mcpctx.Context{kbContext(t)}
	opt.Max = 2
	run, err := Execute(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	stale := run.Drafts[0]
	if stale.Group.Target != "docs/upgrade.md" || stale.Group.Gaps[0].Type != "stale" {
		t.Fatalf("first group: %+v", stale.Group)
	}
	md := read(t, filepath.Join(stale.Dir, draft.DraftFile))
	if strings.Contains(md, "add `--force-upgrade`") {
		t.Errorf("the draft repeats the out-of-date docs:\n%s", md)
	}
	if !strings.Contains(md, "The `--force-upgrade` flag was removed in 2.1.") {
		t.Errorf("the draft lacks the knowledge base's account of the change:\n%s", md)
	}
	changes := read(t, filepath.Join(stale.Dir, ChangesFile))
	if !strings.Contains(changes, "## docs/upgrade.md:15 — `--force-upgrade` no longer exists") {
		t.Errorf("proposed changes:\n%s", changes)
	}
	var cfg Result
	for _, d := range run.Gaps.Groups {
		if d.Target == "docs/configure.md" && d.Mode == "update" && d.Gaps[0].Type == "outdated" {
			cfg.Group = d
		}
	}
	got := ProposedChanges(cfg.Group, "HEAD~1")
	if !strings.Contains(got, "- | `retries` | `3` | How often a failed request is retried |\n+ | `retries` | `5` | How often a failed request is retried |") {
		t.Errorf("proposed change for retries:\n%s", got)
	}
}

func changedRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if err := exec.Command("cp", "-R", fixture+"/.", dir).Run(); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	up := filepath.Join(dir, "cmd", "upgrade.go")
	b := read(t, up)
	os.WriteFile(up, []byte(strings.Replace(b, "\tkeepDays", "\tforce    = flag.Bool(\"force-upgrade\", false, \"skip the version check\")\n\tkeepDays", 1)), 0o644)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "base")
	os.WriteFile(up, []byte(b), 0o644)
	cfg := filepath.Join(dir, "config", "lumen.yaml")
	os.WriteFile(cfg, []byte(strings.Replace(read(t, cfg), "retries: 3", "retries: 5", 1)), 0o644)
	git("commit", "-qam", "change")
	return dir
}

type fakeJudge struct{ reply string }

func (f fakeJudge) Name() string { return "fake judge" }
func (f fakeJudge) Complete(_ context.Context, system, prompt string) (string, error) {
	if !strings.Contains(system, "faithfulness") || !strings.Contains(prompt, "Items the page must document:\n- flag --keep-days (default 14)") {
		return "", os.ErrInvalid
	}
	return f.reply, nil
}

func TestJudge(t *testing.T) {
	opt := repoOptions(t, fixture)
	opt.Only = []string{"G2"}
	run, err := Execute(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	dir := run.Drafts[0].Dir
	meta, _ := LoadMeta(dir)
	eval := func(reply string) *Eval {
		e, err := Evaluate(context.Background(), EvalOptions{Dir: dir, Meta: meta, Evidence: opt.Evidence, Judge: true, Provider: fakeJudge{reply}})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := eval("Here are the scores:\n{\"faithfulness\": 5, \"completeness\": 4, \"clarity\": 4, \"examples\": 3, \"problems\": []}")
	if !e.Gate.Passed || e.Judge.Faithfulness != 5 || e.Judge.Model != "fake judge" || e.Judge.PromptVersion != JudgePromptVersion {
		t.Errorf("good judgement: %+v %+v", e.Judge, e.Gate)
	}
	e = eval(`{"faithfulness": 2, "completeness": 4, "clarity": 4, "examples": 3, "problems": ["the Reference section repeats a sentence"]}`)
	if e.Gate.Passed || !strings.Contains(strings.Join(e.Gate.Blocking, " "), "judge scored faithfulness 2") {
		t.Errorf("low faithfulness: %+v", e.Gate)
	}
	if !strings.Contains(read(t, filepath.Join(dir, EvalReport)), "the Reference section repeats a sentence") {
		t.Error("eval.md lacks the judge's problems")
	}
	e = eval("I think it is fine.")
	if e.Gate.Passed || e.Judge.Error == "" {
		t.Errorf("unreadable judgement: %+v", e.Judge)
	}
	var saved Eval
	json.Unmarshal([]byte(read(t, filepath.Join(dir, EvalFile))), &saved)
	if saved.Judge == nil || saved.Judge.Error == "" {
		t.Errorf("eval.json: %+v", saved.Judge)
	}
}

func TestClean(t *testing.T) {
	in := "---\ntitle: \"X\"\nstatus: \"approved\"\nreview: \"done\"\nclaims: claims.json\n---\n\n<!-- Draft for review: x -->\n\n## A\n\n<!-- guidance -->\n\nOne.[^s1] Two.[^s2] **[uncited]**\n\nSource:[^s3]\n\n```\nrun\n```\n\n[^s1]: a.md, line 1: “One.”\n"
	want := "---\ntitle: \"X\"\n---\n\n## A\n\nOne. Two.\n\n```\nrun\n```\n"
	if got := Clean(in); got != want {
		t.Errorf("Clean:\n%q\nwant\n%q", got, want)
	}
}
