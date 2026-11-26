package finding

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Formats lists the supported reporter names.
var Formats = []string{"text", "json", "github", "sarif", "junit"}

// Write prints findings in the named format. tool and version label SARIF output.
func Write(w io.Writer, format string, fs []Finding, tool, version string) error {
	switch format {
	case "text", "":
		return Text(w, fs)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		c := Counts(fs)
		return enc.Encode(map[string]any{
			"summary":  map[string]int{"error": c[Error], "warning": c[Warning], "info": c[Info], "total": len(fs)},
			"findings": nonNil(fs),
		})
	case "github":
		return GitHub(w, fs)
	case "sarif":
		return SARIF(w, fs, tool, version)
	case "junit":
		return JUnit(w, fs, tool)
	}
	return fmt.Errorf("unknown format %q (%s)", format, strings.Join(Formats, ", "))
}

func nonNil(fs []Finding) []Finding {
	if fs == nil {
		return []Finding{}
	}
	return fs
}

// Text groups findings by file for people reading a terminal.
func Text(w io.Writer, fs []Finding) error {
	cur := "\x00"
	for _, f := range fs {
		if f.File != cur {
			if cur != "\x00" {
				fmt.Fprintln(w)
			}
			cur = f.File
			name := f.File
			if name == "" {
				name = "(site)"
			}
			fmt.Fprintln(w, name)
		}
		loc := ""
		if f.Line > 0 {
			loc = fmt.Sprintf("%d", f.Line)
			if f.Column > 0 {
				loc += fmt.Sprintf(":%d", f.Column)
			}
		}
		fmt.Fprintf(w, "  %-7s %-8s %s  [%s]\n", loc, f.Severity, f.Message, f.Rule)
		if f.Hint != "" {
			fmt.Fprintf(w, "  %-7s %-8s hint: %s\n", "", "", f.Hint)
		}
	}
	c := Counts(fs)
	if len(fs) > 0 {
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "%d errors, %d warnings, %d info\n", c[Error], c[Warning], c[Info])
	return nil
}

// GitHub writes workflow commands that GitHub Actions shows as annotations.
func GitHub(w io.Writer, fs []Finding) error {
	for _, f := range fs {
		level := map[Severity]string{Error: "error", Warning: "warning", Info: "notice"}[f.Severity]
		var props []string
		if f.File != "" && !strings.Contains(f.File, "://") {
			props = append(props, "file="+ghEscapeProp(f.File))
		}
		if f.Line > 0 {
			props = append(props, fmt.Sprintf("line=%d", f.Line))
		}
		if f.Column > 0 {
			props = append(props, fmt.Sprintf("col=%d", f.Column))
		}
		props = append(props, "title="+ghEscapeProp(f.Rule))
		msg := f.Message
		if f.Hint != "" {
			msg += "\n" + f.Hint
		}
		fmt.Fprintf(w, "::%s %s::%s\n", level, strings.Join(props, ","), ghEscapeData(msg))
	}
	return nil
}

func ghEscapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func ghEscapeProp(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// SARIF writes a SARIF 2.1.0 log for code-scanning tools.
func SARIF(w io.Writer, fs []Finding, tool, version string) error {
	used := map[string]bool{}
	for _, f := range fs {
		used[f.Rule] = true
	}
	var ids []string
	for id := range used {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var rules []map[string]any
	for _, id := range ids {
		r := Rules[id]
		rules = append(rules, map[string]any{
			"id":                   id,
			"shortDescription":     map[string]string{"text": r.Summary},
			"defaultConfiguration": map[string]string{"level": sarifLevel(r.Severity)},
		})
	}
	var results []map[string]any
	for _, f := range fs {
		res := map[string]any{
			"ruleId":  f.Rule,
			"level":   sarifLevel(f.Severity),
			"message": map[string]string{"text": strings.TrimSpace(f.Message + " " + f.Hint)},
		}
		if f.File != "" {
			region := map[string]int{}
			if f.Line > 0 {
				region["startLine"] = f.Line
			}
			if f.Column > 0 {
				region["startColumn"] = f.Column
			}
			loc := map[string]any{"artifactLocation": map[string]string{"uri": f.File}}
			if len(region) > 0 {
				loc["region"] = region
			}
			res["locations"] = []map[string]any{{"physicalLocation": loc}}
		}
		results = append(results, res)
	}
	if results == nil {
		results = []map[string]any{}
	}
	if rules == nil {
		rules = []map[string]any{}
	}
	doc := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []map[string]any{{
			"tool":    map[string]any{"driver": map[string]any{"name": tool, "version": version, "rules": rules}},
			"results": results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func sarifLevel(s Severity) string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	}
	return "note"
}

// JUnit writes one test case per file; findings at warning or above fail it.
func JUnit(w io.Writer, fs []Finding, tool string) error {
	type failure struct {
		Message string `xml:"message,attr"`
		Type    string `xml:"type,attr"`
		Text    string `xml:",chardata"`
	}
	type tc struct {
		Name     string    `xml:"name,attr"`
		Class    string    `xml:"classname,attr"`
		Failures []failure `xml:"failure"`
	}
	type suite struct {
		XMLName  xml.Name `xml:"testsuite"`
		Name     string   `xml:"name,attr"`
		Tests    int      `xml:"tests,attr"`
		Failures int      `xml:"failures,attr"`
		Cases    []tc     `xml:"testcase"`
	}
	byFile := map[string][]Finding{}
	var files []string
	for _, f := range fs {
		if _, ok := byFile[f.File]; !ok {
			files = append(files, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	s := suite{Name: tool}
	for _, file := range files {
		c := tc{Name: file, Class: tool}
		for _, f := range byFile[file] {
			if f.Severity.Rank() < Warning.Rank() {
				continue
			}
			c.Failures = append(c.Failures, failure{Message: f.Message, Type: f.Rule, Text: fmt.Sprintf("%s:%d:%d %s %s", f.File, f.Line, f.Column, f.Message, f.Hint)})
		}
		if len(c.Failures) > 0 {
			s.Failures++
		}
		s.Cases = append(s.Cases, c)
	}
	s.Tests = len(s.Cases)
	io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(s); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
