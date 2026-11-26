package kg

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/kakabisht/Docs-toolkit/internal/model"
)

// occurrence is an entity found on a page.
type occurrence struct {
	kind, name string
	rel        string // EDefines, EMentions or EDocuments
	line       int
	span       string
}

var shellLangs = map[string]bool{"bash": true, "sh": true, "shell": true, "console": true, "zsh": true, "terminal": true, "powershell": true, "ps1": true, "pwsh": true, "cmd": true, "fish": true, "shell-session": true, "shellsession": true}

var configLangs = map[string]bool{"yaml": true, "yml": true, "toml": true, "json": true, "jsonc": true}

// shell built-ins and general-purpose tools: not entities of the product
var commonCommands = map[string]bool{
	"cd": true, "ls": true, "echo": true, "export": true, "cat": true, "mkdir": true, "cp": true, "mv": true, "rm": true,
	"curl": true, "wget": true, "grep": true, "sed": true, "awk": true, "sudo": true, "chmod": true, "chown": true, "source": true,
	"tar": true, "unzip": true, "touch": true, "set": true, "unset": true, "exit": true, "pwd": true, "which": true, "env": true,
	"apt": true, "apt-get": true, "yum": true, "dnf": true, "brew": true, "snap": true, "choco": true, "winget": true, "scoop": true,
	"head": true, "tail": true, "less": true, "more": true, "vi": true, "vim": true, "nano": true, "open": true, "code": true,
	"python": true, "python3": true, "pip": true, "pip3": true, "node": true, "npx": true, "sleep": true, "true": true, "false": true,
	"if": true, "then": true, "fi": true, "for": true, "do": true, "done": true, "while": true, "else": true, "xargs": true, "tee": true,
	"jq": true, "find": true, "sort": true, "uniq": true, "wc": true, "diff": true, "ssh": true, "scp": true, "base64": true, "date": true,
	"printf": true, "read": true, "test": true, "kill": true, "ps": true, "ln": true, "dirname": true, "basename": true, "eval": true,
	"command": true, "trap": true, "type": true, "hash": true, "alias": true, "local": true, "return": true, "shift": true, "wait": true,
	"case": true, "esac": true, "function": true, "declare": true, "readonly": true, "doas": true, "pkg": true, "pkg_add": true,
	"apk": true, "pacman": true, "zypper": true, "port": true, "nix-env": true, "main": true, "exec": true, "time": true, "nohup": true,
}

var (
	promptRe   = regexp.MustCompile(`^(?:\$|>|%|PS [^>]*>|[\w.@-]+[@:][^$#]*[$#])\s+`)
	envAssign  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=\S*\s+`)
	cmdWord    = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	flagRe     = regexp.MustCompile(`^(--[a-zA-Z][\w-]*)`)
	endpointRe = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(/[\w/{}:.<>$-]*)`)
	errorRe    = regexp.MustCompile(`(?m)^\s*(?:Error|ERROR|error|fatal|FATAL|panic|Exception|ERR)(?:\[[^\]]*\])?[:!]\s*(\S.{6,118})`)
	identRe    = regexp.MustCompile(`^[A-Za-z_][\w.-]*$`)
)

// extractPage finds the entities on one page with deterministic rules.
func extractPage(p *model.Page, commands map[string]bool, glossaryPage bool) []occurrence {
	var occ []occurrence
	add := func(o occurrence) { occ = append(occ, o) }
	for _, cb := range p.CodeBlocks {
		lang := strings.ToLower(cb.Lang)
		switch {
		case shellLangs[lang]:
			for i, line := range strings.Split(cb.Content, "\n") {
				for _, c := range commandsIn(line, lang, commands) {
					add(occurrence{KCommand, c.cmd, EMentions, cb.Line + 1 + i, strings.TrimSpace(line)})
					for _, f := range c.flags {
						add(occurrence{KFlag, c.base + " " + f, EMentions, cb.Line + 1 + i, strings.TrimSpace(line)})
					}
				}
			}
		case configLangs[lang]:
			for _, k := range configKeys(cb.Content, lang) {
				add(occurrence{KConfig, k, EMentions, cb.Line, k})
			}
		}
		for _, m := range endpointRe.FindAllStringSubmatch(cb.Content, -1) {
			add(occurrence{KAPI, m[1] + " " + m[2], EMentions, cb.Line, m[0]})
		}
		for _, m := range errorRe.FindAllStringSubmatch(cb.Content, -1) {
			add(occurrence{KError, normError(m[1]), EMentions, cb.Line, strings.TrimSpace(m[0])})
		}
	}
	for _, s := range p.InlineCode {
		t := strings.TrimSpace(s.Text)
		if m := endpointRe.FindStringSubmatch(t); m != nil {
			add(occurrence{KAPI, m[1] + " " + m[2], EMentions, s.Line, t})
			continue
		}
		if m := errorRe.FindStringSubmatch(t); m != nil {
			add(occurrence{KError, normError(m[1]), EMentions, s.Line, t})
			continue
		}
		if cs := commandsIn(t, "inline", commands); len(cs) > 0 {
			for _, c := range cs {
				add(occurrence{KCommand, c.cmd, EMentions, s.Line, t})
				for _, f := range c.flags {
					add(occurrence{KFlag, c.base + " " + f, EMentions, s.Line, t})
				}
			}
			continue
		}
		// bare identifiers are matched later against entities defined elsewhere
		add(occurrence{"", t, EMentions, s.Line, t})
	}
	for _, s := range p.TableCells {
		t := strings.Trim(strings.TrimSpace(s.Text), "`")
		if m := endpointRe.FindStringSubmatch(t); m != nil {
			add(occurrence{KAPI, m[1] + " " + m[2], EDefines, s.Line, s.Text})
		} else if identRe.MatchString(t) && looksLikeKey(t) && referenceTable(p) {
			add(occurrence{KConfig, t, EDefines, s.Line, s.Text})
		}
	}
	for _, s := range p.DefTerms {
		t := strings.TrimSpace(s.Text)
		switch {
		case glossaryPage:
			add(occurrence{KGlossary, t, EDefines, s.Line, t})
		case strings.HasPrefix(t, "-"):
			// on a page about a command, a flag belongs to that command
			name := strings.Fields(t)[0]
			if f := strings.Fields(strings.ToLower(strings.Trim(p.Title, "` "))); len(f) > 0 && commands[f[0]] {
				name = f[0] + " " + name
			}
			add(occurrence{KFlag, name, EDefines, s.Line, t})
		case identRe.MatchString(t) && looksLikeKey(t):
			add(occurrence{KConfig, t, EDefines, s.Line, t})
		default:
			// free-text terms ("Step 1", "Example") are not entities on their
			// own; they link only when they name a known entity
			add(occurrence{"", t, EMentions, s.Line, t})
		}
	}
	if glossaryPage {
		for _, h := range p.Headings {
			if h.Level >= 2 && h.Level <= 3 && h.Text != "" {
				add(occurrence{KGlossary, h.Text, EDefines, h.Line, h.Text})
			}
		}
	}
	return occ
}

type cmdUse struct {
	base, cmd string
	flags     []string
}

// commandsIn parses shell lines into command names (with subcommands) and flags.
func commandsIn(line, lang string, known map[string]bool) []cmdUse {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
		return nil
	}
	prompted := promptRe.MatchString(line)
	if lang == "console" || lang == "shell-session" || lang == "shellsession" || lang == "terminal" {
		if !prompted {
			return nil // output, not a command
		}
	}
	line = promptRe.ReplaceAllString(line, "")
	var out []cmdUse
	for _, part := range regexp.MustCompile(`&&|\|\||[|;]`).Split(line, -1) {
		part = strings.TrimSpace(part)
		for envAssign.MatchString(part) {
			part = envAssign.ReplaceAllString(part, "")
		}
		toks := strings.Fields(part)
		for len(toks) > 0 && (toks[0] == "sudo" || toks[0] == "doas" || toks[0] == "time" || toks[0] == "exec" || toks[0] == "nohup") {
			toks = toks[1:]
		}
		if len(toks) == 0 || !cmdWord.MatchString(toks[0]) || commonCommands[toks[0]] {
			continue
		}
		if lang == "inline" && !known[toks[0]] {
			continue // inline code only counts for commands seen in code blocks
		}
		cmd := []string{toks[0]}
		i := 1
		for ; i < len(toks) && len(cmd) < 3; i++ {
			if !cmdWord.MatchString(toks[i]) || len(toks[i]) > 24 {
				break
			}
			cmd = append(cmd, toks[i])
		}
		// stop subcommands at the first argument-looking word: keep at most two words
		// unless the third is also a plain word followed by flags or nothing
		if len(cmd) == 3 && i < len(toks) && !strings.HasPrefix(toks[i], "-") {
			cmd = cmd[:2]
		}
		u := cmdUse{base: toks[0], cmd: strings.Join(cmd, " ")}
		for _, t := range toks[1:] {
			if m := flagRe.FindStringSubmatch(t); m != nil {
				u.flags = append(u.flags, m[1])
			}
		}
		out = append(out, u)
	}
	return out
}

// configKeys flattens a YAML, TOML or JSON example into dotted keys (depth ≤ 3).
func configKeys(src, lang string) []string {
	var doc any
	var err error
	switch lang {
	case "toml":
		var m map[string]any
		_, err = toml.Decode(src, &m)
		doc = m
	case "json", "jsonc":
		err = json.Unmarshal([]byte(src), &doc)
	default:
		err = yaml.Unmarshal([]byte(src), &doc)
	}
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(v any, prefix string, depth int)
	walk = func(v any, prefix string, depth int) {
		if depth > 3 {
			return
		}
		switch m := v.(type) {
		case map[string]any:
			for k, x := range m {
				if !identRe.MatchString(k) {
					continue
				}
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				seen[key] = true
				walk(x, key, depth+1)
			}
		case []any:
			for _, x := range m {
				walk(x, prefix, depth)
			}
		}
	}
	walk(doc, "", 1)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// looksLikeKey rejects plain English words in tables: keys have case changes,
// separators or dots.
func looksLikeKey(t string) bool {
	if len(t) < 2 || len(t) > 64 {
		return false
	}
	if strings.ContainsAny(t, "._-") {
		return true
	}
	for i, r := range t {
		if i > 0 && r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// referenceTable is true when the page's tables look like option references
// (first cells mostly identifiers).
func referenceTable(p *model.Page) bool {
	n, keys := 0, 0
	for _, s := range p.TableCells {
		n++
		t := strings.Trim(strings.TrimSpace(s.Text), "`")
		if identRe.MatchString(t) && looksLikeKey(t) {
			keys++
		}
	}
	return n > 0 && keys*2 >= n
}

var spaces = regexp.MustCompile(`\s+`)
var errVar = regexp.MustCompile(`"[^"]*"|'[^']*'|0x[0-9a-fA-F]+|\b\d+(?:[.:/-]\d+)*\b`)

// normError keeps the stable part of an error message.
func normError(s string) string {
	s = spaces.ReplaceAllString(strings.TrimSpace(s), " ")
	s = errVar.ReplaceAllString(s, "…")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}
