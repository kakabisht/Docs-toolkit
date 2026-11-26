package crawl

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitDates returns the last commit time (unix seconds) for each file in the
// repository at root, from one `git log` pass. Empty when root is not a git
// checkout or git is missing.
func gitDates(root string) map[string]int64 {
	out := map[string]int64{}
	cmd := exec.Command("git", "-C", root, "log", "--format=%x00%ct", "--name-only", "--no-renames", "--relative")
	b, err := cmd.Output()
	if err != nil {
		return out
	}
	var cur int64
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\x00") {
			cur, _ = strconv.ParseInt(strings.TrimPrefix(line, "\x00"), 10, 64)
			continue
		}
		if line == "" {
			continue
		}
		if _, seen := out[line]; !seen { // log is newest first
			out[line] = cur
		}
	}
	return out
}

// ChangedFiles lists files changed between base and the working tree,
// relative to root (for --changed-only).
func ChangedFiles(root, base string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, args := range [][]string{
		{"diff", "--name-only", "--relative", base + "...HEAD"},
		{"diff", "--name-only", "--relative", "HEAD"},
		{"ls-files", "--others", "--exclude-standard"},
	} {
		b, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			if args[0] == "diff" && strings.Contains(args[3], "...") {
				return nil, err
			}
			continue
		}
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "" {
				out[filepath.ToSlash(l)] = true
			}
		}
	}
	return out, nil
}

type codeowners []struct {
	pattern string
	owners  []string
}

// loadCodeowners reads CODEOWNERS from the usual locations.
func loadCodeowners(root string) codeowners {
	for _, p := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS", ".gitlab/CODEOWNERS"} {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			continue
		}
		var co codeowners
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
				continue
			}
			f := strings.Fields(line)
			co = append(co, struct {
				pattern string
				owners  []string
			}{f[0], f[1:]})
		}
		return co
	}
	return nil
}

// owner returns the owners of the last matching CODEOWNERS rule.
func (co codeowners) owner(rel string) string {
	res := ""
	for _, r := range co {
		if codeownersMatch(r.pattern, rel) {
			res = strings.Join(r.owners, " ")
		}
	}
	return res
}

func codeownersMatch(pat, rel string) bool {
	anchored := strings.HasPrefix(pat, "/")
	pat = strings.TrimPrefix(pat, "/")
	if strings.HasSuffix(pat, "/") {
		pat += "**"
	}
	if pat == "*" {
		return true
	}
	if !strings.Contains(pat, "/") || (!anchored && !strings.HasPrefix(pat, "**")) {
		if !anchored {
			pat = "**/" + pat
		}
	}
	if Match(pat, rel) {
		return true
	}
	// a directory pattern without a trailing slash matches everything under it
	return Match(strings.TrimSuffix(pat, "/**")+"/**", rel)
}
