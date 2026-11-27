package gaps

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const fixture = "../../testdata/fixtures/agent/repo"

func names(items []Item, kind string) []string {
	var out []string
	for _, it := range items {
		if kind == "" || it.Kind == kind {
			out = append(out, it.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestFromCode(t *testing.T) {
	goSrc := `package cmd
var b strings.Builder
func init() {
	b.String()
	verbose := flag.Bool("verbose", false, "say more")
	c.Flags().StringVarP(&opt.Out, "out", "o", "build",
		"output folder")
	f.DurationVar(&timeout, "timeout", 5*time.Minute, "per request")
	pf.CountP("debug", "d", "more debug output")
	// flag.Bool("commented", false, "not a flag")
	key := os.Getenv("ACME_KEY")
	home := os.Getenv("HOME")
	mux.HandleFunc("GET /v1/items/{id}", get)
	r.POST("/v1/items", create)
}
type Config struct {
	Port int ` + "`yaml:\"port\"`" + ` // port to listen on
	Skip string ` + "`yaml:\"-\"`" + `
}`
	items := FromCode("cmd/root.go", goSrc)
	if got := strings.Join(names(items, "flag"), " "); got != "--debug --out --timeout --verbose" {
		t.Errorf("go flags: %s", got)
	}
	for _, it := range items {
		switch it.Name {
		case "--out":
			if it.Default != "build" || it.Help != "output folder" || it.Line != 6 {
				t.Errorf("--out: %+v", it)
			}
		case "--timeout":
			if it.Default != "5*time.Minute" {
				t.Errorf("--timeout: %+v", it)
			}
		case "port":
			if it.Kind != "config" || it.Help != "port to listen on" {
				t.Errorf("port: %+v", it)
			}
		}
	}
	if got := strings.Join(names(items, "env"), " "); got != "ACME_KEY" {
		t.Errorf("env (HOME is noise): %s", got)
	}
	if got := strings.Join(names(items, "endpoint"), " "); got != "GET /v1/items/{id} POST /v1/items" {
		t.Errorf("go endpoints: %s", got)
	}
	if got := strings.Join(names(items, "config"), " "); got != "port" {
		t.Errorf("struct tags: %s", got)
	}

	py := `parser.add_argument("-n", "--dry-run", action="store_true", help="show what would change")
parser.add_argument("--workers", default=4, help='parallel workers')
@click.option("--color/--no-color", default=True)
level = os.getenv("ACME_LEVEL", "info")
url = os.environ["ACME_URL"]
@app.get("/orders/{order_id}")
@app.route("/legacy/<int:id>", methods=["POST"])`
	items = FromCode("app/main.py", py)
	if got := strings.Join(names(items, "flag"), " "); got != "--color --dry-run --workers" {
		t.Errorf("py flags: %s", got)
	}
	for _, it := range items {
		if it.Name == "--workers" && (it.Default != "4" || it.Help != "parallel workers") {
			t.Errorf("--workers: %+v", it)
		}
		if it.Name == "ACME_LEVEL" && it.Default != "info" {
			t.Errorf("ACME_LEVEL: %+v", it)
		}
	}
	if got := strings.Join(names(items, "endpoint"), " "); got != "GET /orders/{order_id} POST /legacy/{id}" {
		t.Errorf("py endpoints: %s", got)
	}

	js := "program.option('-p, --port <n>', 'port to use', 3000)\napp.delete('/users/:id', h)\nconst t = process.env.ACME_TOKEN"
	items = FromCode("src/cli.js", js)
	if got := strings.Join(names(items, ""), " "); got != "--port ACME_TOKEN DELETE /users/{id}" {
		t.Errorf("js: %s", got)
	}
}

func TestSurfaceSkipsSecretsAndTests(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	write(".env", "ACME_SECRET=hunter2\n")
	write(".env.example", "ACME_REGION=eu-west-1\n")
	write("main_test.go", `var x = flag.Bool("test-only", false, "")`)
	write("testdata/x.go", `var x = flag.Bool("fixture", false, "")`)
	write("docker-compose.yml", "services:\n  app:\n    image: x\n")
	write("config/app.yaml", "server:\n  port: 8080 # listen port\n")
	items, err := Surface(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(items, ""), " "); got != "ACME_REGION server.port" {
		t.Errorf("surface: %s", got)
	}
	for _, it := range items {
		if it.Name == "server.port" && (it.Default != "8080" || it.Help != "listen port") {
			t.Errorf("server.port: %+v", it)
		}
	}
}

func pages(t *testing.T, root string) []Page {
	t.Helper()
	var out []Page
	filepath.Walk(filepath.Join(root, "docs"), func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".md") {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			out = append(out, Page{File: filepath.ToSlash(rel), Text: string(b)})
		}
		return nil
	})
	return out
}

func TestFindFixture(t *testing.T) {
	r, err := Find(Options{Root: fixture, Content: "docs", Pages: pages(t, fixture)})
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, g := range r.Gaps {
		if g.Type != "undocumented" {
			t.Errorf("without --since every gap is undocumented: %+v", g)
		}
		missing = append(missing, g.Item.Name)
	}
	sort.Strings(missing)
	want := "--keep-days GET /v1/workspaces/{id} LUMEN_LOG_LEVEL POST /v1/upgrades server.port"
	if got := strings.Join(missing, " "); got != want {
		t.Errorf("gaps:\n got %s\nwant %s", got, want)
	}
	if r.Items != 12 || r.Covered != 7 {
		t.Errorf("items %d, covered %d", r.Items, r.Covered)
	}
	targets := map[string]string{}
	for _, g := range r.Groups {
		targets[g.Topic] = g.Mode + " " + g.Target
	}
	if targets["Flags of the upgrade command"] != "update docs/reference/flags.md" {
		t.Errorf("flag group should update the page that has the other upgrade flags: %v", targets)
	}
	if r.Groups[0].Topic != "HTTP API endpoints" {
		t.Errorf("endpoints first by priority: %+v", r.Groups[0])
	}
}

func TestNewPageTarget(t *testing.T) {
	g := group([]Gap{{Type: "undocumented", Item: Item{Kind: "env", Name: "ACME_LEVEL", File: "main.go"}}},
		Options{Content: "docs", Surface: []Item{{Kind: "env", Name: "ACME_LEVEL", File: "main.go"}}})
	if len(g) != 1 || g[0].Mode != "new" || g[0].Target != "docs/reference/environment-variables.md" {
		t.Errorf("%+v", g)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// ChangedRepo copies the fixture into a git repository with two commits:
// the second removes --force-upgrade and changes the default of retries.
func ChangedRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if err := exec.Command("cp", "-R", fixture+"/.", dir).Run(); err != nil {
		t.Fatal(err)
	}
	up := filepath.Join(dir, "cmd", "upgrade.go")
	b, _ := os.ReadFile(up)
	withForce := strings.Replace(string(b), "\tkeepDays", "\tforce    = flag.Bool(\"force-upgrade\", false, \"skip the version check\")\n\tkeepDays", 1)
	os.WriteFile(up, []byte(withForce), 0o644)
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base")
	os.WriteFile(up, b, 0o644)
	cfg := filepath.Join(dir, "config", "lumen.yaml")
	c, _ := os.ReadFile(cfg)
	os.WriteFile(cfg, []byte(strings.Replace(string(c), "retries: 3", "retries: 5", 1)), 0o644)
	git(t, dir, "commit", "-qam", "change")
	return dir
}

func TestSince(t *testing.T) {
	dir := ChangedRepo(t)
	r, err := Find(Options{Root: dir, Content: "docs", Since: "HEAD~1", Pages: pages(t, dir)})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Gap{}
	for _, g := range r.Gaps {
		byName[g.Item.Name] = g
	}
	stale := byName["--force-upgrade"]
	if stale.Type != "stale" || stale.Change == nil || stale.Change.Status != "removed" || len(stale.Mentions) != 1 || stale.Mentions[0].File != "docs/upgrade.md" || stale.Mentions[0].Line != 15 {
		t.Errorf("stale: %+v", stale)
	}
	out := byName["retries"]
	if out.Type != "outdated" || out.Change.OldDefault != "3" || out.Item.Default != "5" || len(out.Mentions) != 1 || out.Mentions[0].File != "docs/configure.md" {
		t.Errorf("outdated: %+v", out)
	}
	if byName["--keep-days"].Type != "undocumented" || byName["--keep-days"].Change != nil {
		t.Errorf("--keep-days was in both commits: %+v", byName["--keep-days"])
	}
	if r.Groups[0].Mode != "update" || r.Groups[0].Target != "docs/upgrade.md" {
		t.Errorf("removed items come first: %+v", r.Groups[0])
	}
	if _, err := Find(Options{Root: dir, Since: "no-such-ref", Pages: pages(t, dir)}); err == nil {
		t.Error("a bad ref was accepted")
	}
}
