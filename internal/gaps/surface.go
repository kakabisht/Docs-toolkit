// Package gaps finds what a repository's code defines that its documentation
// does not cover: command-line flags, environment variables, HTTP endpoints
// and configuration keys, plus, against a git ref, what changed or was removed
// while the docs still describe the old behaviour.
//
// Everything is a text match with a file and line, like the rest of docsbot:
// "documented" means the name was found on a page, not that the page is right.
package gaps

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Item is one part of the product's surface, defined in code or configuration.
type Item struct {
	Kind    string `json:"kind"` // flag, env, endpoint, config
	Name    string `json:"name"` // --backup, LUMEN_HOME, GET /orders/{id}, server.port
	File    string `json:"file"` // relative to the repository root
	Line    int    `json:"line"`
	Default string `json:"default,omitempty"`
	Help    string `json:"help,omitempty"`
}

// Kinds in the order reports list them.
var Kinds = []string{"flag", "env", "endpoint", "config"}

var codeExt = map[string]bool{".go": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true,
	".sh": true, ".bash": true, ".rb": true, ".java": true, ".kt": true, ".rs": true}

var skipDir = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, "public": true, "site": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true,
	".docusaurus": true, "drafts": true, "testdata": true, "tests": true, "test": true, "fixtures": true}

// isTest reports test files, whose flags and variables are not product surface.
func isTest(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, "_test.go") || strings.HasPrefix(n, "test_") || strings.HasSuffix(n, "_test.py") ||
		strings.Contains(n, ".test.") || strings.Contains(n, ".spec.")
}

// isConfigFile picks YAML files that hold the product's own settings, not
// CI, compose or site-generator files.
func isConfigFile(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	ext := filepath.Ext(base)
	if ext != ".yaml" && ext != ".yml" {
		return false
	}
	for _, skip := range []string{"docker-compose", "compose.", "mkdocs", "docsbot.yml", "docsbot.yaml", "action.y", "netlify", "vercel", "sidebars", "_config", "antora", "readthedocs", "codecov", "dependabot"} {
		if strings.Contains(base, skip) {
			return false
		}
	}
	if strings.Contains(base, "config") || strings.Contains(base, "settings") || strings.Contains(base, "defaults") || strings.Contains(base, ".example.") {
		return true
	}
	for _, d := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
		if d == "config" || d == "configs" || d == "conf" || d == "settings" {
			return true
		}
	}
	return false
}

// Surface reads code and configuration under root. Test files, dependencies
// and build output are skipped, and so is .env, which may hold secrets.
func Surface(root string) ([]Item, error) {
	var items []Item
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
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if isTest(name) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 512<<10 {
			return nil
		}
		switch {
		case codeExt[strings.ToLower(filepath.Ext(name))] || name == "Dockerfile":
			b, err := os.ReadFile(p)
			if err == nil {
				items = append(items, FromCode(rel, string(b))...)
			}
		case name == ".env.example" || name == ".env.sample" || name == "env.example":
			b, err := os.ReadFile(p)
			if err == nil {
				items = append(items, fromEnvFile(rel, string(b))...)
			}
		case isConfigFile(rel):
			b, err := os.ReadFile(p)
			if err == nil {
				items = append(items, fromYAML(rel, b)...)
			}
		}
		return nil
	})
	return dedupe(items), err
}

// dedupe keeps the first definition of each kind and name, preferring one
// with a default or help text.
func dedupe(items []Item) []Item {
	seen := map[string]int{}
	var out []Item
	for _, it := range items {
		k := it.Kind + "\x00" + it.Name
		if i, ok := seen[k]; ok {
			if out[i].Default == "" && out[i].Help == "" && (it.Default != "" || it.Help != "") {
				out[i] = it
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, it)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].File != out[b].File {
			return out[a].File < out[b].File
		}
		return out[a].Line < out[b].Line
	})
	return out
}

var (
	goFlagCall = regexp.MustCompile(`\b(?:flag|[a-z]\w{0,2}|Flags\(\)|PersistentFlags\(\))\.(Bool|String|Int|Int64|Uint|Uint64|Float64|Duration|StringSlice|StringArray|IntSlice|Count)(Var)?(P)?\(`)
	pyArg      = regexp.MustCompile(`(?:add_argument|click\.option|typer\.Option)\(`)
	jsOption   = regexp.MustCompile(`\.(?:option|requiredOption)\(\s*['"]([^'"]+)['"]\s*(?:,\s*['"]([^'"]*)['"])?(?:\s*,\s*([^)]+))?\)`)
	longFlag   = regexp.MustCompile(`--[a-z0-9][\w-]*`)
	pyDefault  = regexp.MustCompile(`\bdefault\s*=\s*([^,)]+)`)
	pyHelp     = regexp.MustCompile(`\bhelp\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	envRes     = []*regexp.Regexp{
		regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\("([A-Z][A-Z0-9_]+)"\)`),
		regexp.MustCompile(`os\.(?:getenv|environ\.get)\(\s*["']([A-Z][A-Z0-9_]+)["'](?:\s*,\s*([^)]+))?\)`),
		regexp.MustCompile(`os\.environ\[\s*["']([A-Z][A-Z0-9_]+)["']\s*\]`),
		regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]+)`),
		regexp.MustCompile(`process\.env\[\s*["']([A-Z][A-Z0-9_]+)["']\s*\]`),
		regexp.MustCompile(`ENV\[\s*["']([A-Z][A-Z0-9_]+)["']\s*\]`),
		regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]+)(?::?-([^}]*))?\}`),
	}
	routeRes = []*regexp.Regexp{
		// FastAPI, Flask-style decorators: @app.get("/orders/{id}")
		regexp.MustCompile(`@\w+\.(get|post|put|patch|delete|head)\(\s*["'](/[^"']*)["']`),
		// Flask: @app.route("/x", methods=["POST"])
		regexp.MustCompile(`@\w+\.route\(\s*["'](/[^"']*)["'](?:[^)]*methods\s*=\s*\[\s*["'](\w+)["'])?`),
		// Go net/http, with optional method pattern: HandleFunc("GET /x", …)
		regexp.MustCompile(`\bHandle(?:Func)?\(\s*"(?:(GET|POST|PUT|PATCH|DELETE|HEAD) )?(/[^"]*)"`),
		// gin, echo, chi: r.GET("/x"), r.Get("/x")
		regexp.MustCompile(`\.(GET|POST|PUT|PATCH|DELETE|Get|Post|Put|Patch|Delete)\(\s*"(/[^"]*)"`),
		// Express: app.get('/x', …)
		regexp.MustCompile("\\b(?:app|router)\\.(get|post|put|patch|delete)\\(\\s*['\"`](/[^'\"`]*)['\"`]"),
	}
	structTag = regexp.MustCompile("`[^`]*\\b(?:yaml|toml):\"([A-Za-z][\\w-]*)[,\"]")
	lineCmt   = regexp.MustCompile(`//\s*(.+)$`)
	pathParam = regexp.MustCompile(`\{([^}/]+)\}|:(\w+)|<(?:\w+:)?(\w+)>`)
)

// envNoise are variables almost every program reads; they are not the product's.
var envNoise = map[string]bool{"HOME": true, "PATH": true, "USER": true, "PWD": true, "SHELL": true, "TERM": true,
	"TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true, "GOPATH": true, "GOOS": true, "GOARCH": true, "CI": true,
	"EDITOR": true, "PAGER": true, "HOSTNAME": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "PYTHONPATH": true, "NODE_ENV": true}

// FromCode extracts flags, environment variables, endpoints and struct-tag
// configuration keys from one source file.
func FromCode(file, src string) []Item {
	ext := strings.ToLower(filepath.Ext(file))
	isJS := ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".ts"
	isPy := ext == ".py"
	lines := strings.Split(src, "\n")
	var out []Item
	for i, l := range lines {
		n := i + 1
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "#") && !strings.HasPrefix(trim, "#!") {
			continue
		}
		// a call may continue on the next lines
		joined := strings.Join(lines[i:min(len(lines), i+4)], " ")
		for _, m := range goFlagCall.FindAllStringSubmatchIndex(l, -1) {
			if it, ok := goFlag(joined[m[1]:], l[m[2]:m[3]], m[4] >= 0, m[6] >= 0); ok {
				it.File, it.Line = file, n
				out = append(out, it)
			}
		}
		if loc := pyArg.FindStringIndex(l); isPy && loc != nil {
			args := callArgs(joined[loc[1]:])
			var names []string
			for _, a := range args {
				if s, ok := literal(a); ok && strings.HasPrefix(s, "--") {
					names = append(names, longFlag.FindString(s))
				}
			}
			rest := strings.Join(args, ",")
			for _, name := range names {
				it := Item{Kind: "flag", Name: name, File: file, Line: n}
				if m := pyDefault.FindStringSubmatch(rest); m != nil {
					it.Default = unquote(strings.TrimSpace(m[1]))
				}
				if m := pyHelp.FindStringSubmatch(rest); m != nil {
					it.Help = m[1] + m[2]
				}
				out = append(out, it)
			}
		}
		for _, m := range jsOption.FindAllStringSubmatch(l, -1) {
			if !isJS {
				break
			}
			for _, name := range longFlag.FindAllString(m[1], -1) {
				out = append(out, Item{Kind: "flag", Name: name, File: file, Line: n, Help: m[2], Default: unquote(strings.TrimSpace(m[3]))})
			}
		}
		for _, re := range envRes {
			for _, m := range re.FindAllStringSubmatch(l, -1) {
				if envNoise[m[1]] || strings.HasPrefix(m[1], "GITHUB_") || strings.HasPrefix(m[1], "RUNNER_") {
					continue
				}
				it := Item{Kind: "env", Name: m[1], File: file, Line: n}
				if len(m) > 2 {
					it.Default = unquote(strings.TrimSpace(m[2]))
				}
				out = append(out, it)
			}
		}
		for k, re := range routeRes {
			if (k <= 1) != isPy || k == 4 && !isJS {
				continue // decorators are Python's; app.get('/x') is Express
			}
			for _, m := range re.FindAllStringSubmatch(l, -1) {
				method, path := m[1], m[2]
				if k == 1 { // route(path, methods=[...])
					method, path = m[2], m[1]
				}
				if method == "" && k == 1 {
					method = "GET"
				}
				out = append(out, Item{Kind: "endpoint", Name: Endpoint(method, path), File: file, Line: n})
			}
		}
		if strings.HasSuffix(file, ".go") {
			for _, m := range structTag.FindAllStringSubmatch(l, -1) {
				it := Item{Kind: "config", Name: m[1], File: file, Line: n}
				if c := lineCmt.FindStringSubmatch(l); c != nil {
					it.Help = strings.TrimSpace(c[1])
				}
				out = append(out, it)
			}
		}
	}
	return out
}

// Endpoint names an endpoint as "METHOD /path", with path parameters written
// {name} whatever the framework's syntax.
func Endpoint(method, path string) string {
	path = pathParam.ReplaceAllStringFunc(path, func(s string) string {
		m := pathParam.FindStringSubmatch(s)
		return "{" + m[1] + m[2] + m[3] + "}"
	})
	if method == "" {
		return path
	}
	return strings.ToUpper(method) + " " + path
}

// goFlag reads the arguments of a flag or pflag call: (&v,) "name", (short,)
// default, "usage".
func goFlag(rest, typ string, isVar, short bool) (Item, bool) {
	args := callArgs(rest)
	if isVar {
		if len(args) == 0 {
			return Item{}, false
		}
		args = args[1:]
	}
	if len(args) < 2 {
		return Item{}, false
	}
	name, ok := literal(args[0])
	if !ok || name == "" || strings.ContainsAny(name, " /") {
		return Item{}, false
	}
	args = args[1:]
	if short && len(args) > 0 {
		args = args[1:]
	}
	it := Item{Kind: "flag", Name: "--" + strings.TrimLeft(name, "-")}
	if typ != "Count" && len(args) > 0 {
		it.Default = unquote(args[0])
		args = args[1:]
	}
	if len(args) > 0 {
		if u, ok := literal(args[0]); ok {
			it.Help = u
		}
	}
	return it, true
}

// callArgs splits the arguments of a call that starts just after "(", up to
// the matching ")", at top-level commas.
func callArgs(s string) []string {
	var out []string
	depth := 0
	var quote rune
	start := 0
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			if depth == 0 {
				if a := strings.TrimSpace(s[start:i]); a != "" {
					out = append(out, a)
				}
				return out
			}
			depth--
		case r == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return out
}

func literal(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return "", false
}

func unquote(s string) string {
	if v, ok := literal(s); ok {
		return v
	}
	return s
}

func fromEnvFile(file, src string) []Item {
	var out []Item
	re := regexp.MustCompile(`^\s*(?:export\s+)?([A-Z][A-Z0-9_]+)\s*=\s*(.*)$`)
	sc := bufio.NewScanner(strings.NewReader(src))
	n := 0
	for sc.Scan() {
		n++
		if m := re.FindStringSubmatch(sc.Text()); m != nil {
			out = append(out, Item{Kind: "env", Name: m[1], File: file, Line: n, Default: unquote(strings.TrimSpace(m[2]))})
		}
	}
	return out
}

// fromYAML lists every scalar key path in a configuration file.
func fromYAML(file string, b []byte) []Item {
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	var out []Item
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		if n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			path := k.Value
			if prefix != "" {
				path = prefix + "." + k.Value
			}
			switch v.Kind {
			case yaml.MappingNode:
				walk(v, path)
			default:
				it := Item{Kind: "config", Name: path, File: file, Line: k.Line, Help: strings.TrimSpace(strings.TrimLeft(k.HeadComment+" "+v.LineComment, "# "))}
				if v.Kind == yaml.ScalarNode {
					it.Default = v.Value
				}
				out = append(out, it)
			}
		}
	}
	walk(doc.Content[0], "")
	return out
}
