// Package finding defines the result every check produces, the rule
// registry, and the reporters that print findings for people and CI.
package finding

import (
	"fmt"
	"sort"
	"strings"
)

// Severity of a finding.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
	Off     Severity = "off" // disabled through configuration
)

// Rank orders severities: error > warning > info > off.
func (s Severity) Rank() int {
	switch s {
	case Error:
		return 3
	case Warning:
		return 2
	case Info:
		return 1
	}
	return 0
}

// ParseSeverity accepts error, warning (or warn), info and off.
func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return Error, nil
	case "warning", "warn":
		return Warning, nil
	case "info":
		return Info, nil
	case "off", "none", "ignore":
		return Off, nil
	}
	return "", fmt.Errorf("unknown severity %q (error, warning, info, off)", s)
}

// Finding is one problem, tied to a location.
type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"` // path relative to the repository root, or a URL
	Line     int      `json:"line,omitempty"`
	Column   int      `json:"column,omitempty"`
	Message  string   `json:"message"`
	Hint     string   `json:"hint,omitempty"`
	Target   string   `json:"target,omitempty"` // link target, when the finding is about a link
}

// Sort orders findings by file, line, column, rule and message, so output
// is identical across runs.
func Sort(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Message < b.Message
	})
}

// Dedupe removes identical findings (same rule, place and message).
func Dedupe(fs []Finding) []Finding {
	seen := map[string]bool{}
	out := fs[:0]
	for _, f := range fs {
		k := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s\x00%s", f.Rule, f.File, f.Line, f.Column, f.Target, f.Message)
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

// Counts tallies findings per severity.
func Counts(fs []Finding) map[Severity]int {
	c := map[Severity]int{}
	for _, f := range fs {
		c[f.Severity]++
	}
	return c
}

// Fails reports whether any finding is at or above level.
func Fails(fs []Finding, level Severity) bool {
	if level == Off {
		return false
	}
	for _, f := range fs {
		if f.Severity.Rank() >= level.Rank() {
			return true
		}
	}
	return false
}
