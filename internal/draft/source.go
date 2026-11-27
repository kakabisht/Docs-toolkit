// Package draft writes a best-practice draft from a knowledge base and
// transcripts, with a citation for every statement and a ledger of the facts a
// reviewer must check. It never publishes: it writes to an output folder that
// is not part of the docs.
package draft

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Source is one citable passage.
type Source struct {
	ID      string `json:"id"`   // s1, s2 …
	Kind    string `json:"kind"` // kb or transcript
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`     // first line (kb, text transcripts)
	EndLine int    `json:"end_line,omitempty"` // last line
	Time    string `json:"time,omitempty"`     // transcript timestamp hh:mm:ss
	Speaker string `json:"speaker,omitempty"`
	Heading string `json:"heading,omitempty"` // kb: the section it sits in
	Code    bool   `json:"code,omitempty"`    // a code block
	Text    string `json:"text"`
}

// Location is how the source is named in a footnote.
func (s Source) Location() string {
	switch {
	case s.Kind == "mcp":
		return s.File // the server and the attribution it gave
	case s.Kind == "transcript" && s.Time != "":
		loc := fmt.Sprintf("%s at %s", s.File, s.Time)
		if s.Speaker != "" {
			loc += ", " + s.Speaker
		}
		return loc
	case s.EndLine > s.Line:
		return fmt.Sprintf("%s, lines %d–%d", s.File, s.Line, s.EndLine)
	default:
		return fmt.Sprintf("%s, line %d", s.File, s.Line)
	}
}

var kbExt = map[string]bool{".md": true, ".mdx": true, ".markdown": true, ".txt": true, ".adoc": true, ".asciidoc": true, ".rst": true}

// LoadKB reads passages from files or folders of Markdown, AsciiDoc, reST or
// text. Paths in the result are relative to each argument's folder.
func LoadKB(paths []string) ([]Source, error) {
	var out []Source
	for _, p := range paths {
		if isURL(p) {
			return nil, fmt.Errorf("%s: the knowledge base must be local files", p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		base := p
		var files []string
		if info.IsDir() {
			filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && f != p && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
					return filepath.SkipDir
				}
				if !d.IsDir() && kbExt[strings.ToLower(filepath.Ext(f))] {
					files = append(files, f)
				}
				return nil
			})
		} else {
			base = filepath.Dir(p)
			files = []string{p}
		}
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			rel, _ := filepath.Rel(filepath.Dir(base), f)
			out = append(out, splitDoc(filepath.ToSlash(rel), string(b))...)
		}
	}
	return out, nil
}

func isURL(s string) bool { return strings.Contains(s, "://") }

var (
	mdHeading   = regexp.MustCompile(`^(#{1,6}|={1,6})\s+(.+?)\s*#*$`)
	fenceOpen   = regexp.MustCompile("^\\s*(```+|~~~+|-{4,}|\\.{4,})")
	listStart   = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)]|\.+|\*+)\s+`)
	commentLine = regexp.MustCompile(`^\s*(?://|<!--.*-->\s*$|:[\w-]+:|\[[^\]]*\]\s*$)`)
)

// SplitPage cuts one page of the repository into passages; file is its path
// in the repository.
func SplitPage(file, text string) []Source { return splitDoc(file, text) }

// splitDoc cuts a document into paragraphs, list items and code blocks, each
// with its line range and section heading.
func splitDoc(file, text string) []Source {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []Source
	heading := ""
	var cur []string
	start := 0
	flush := func(end int) {
		t := strings.TrimSpace(strings.Join(cur, " "))
		if t != "" {
			out = append(out, Source{Kind: "kb", File: file, Line: start, EndLine: end, Heading: heading, Text: t})
		}
		cur = nil
	}
	i := 0
	// front matter
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				i = j + 1
				break
			}
		}
	}
	for ; i < len(lines); i++ {
		l := lines[i]
		n := i + 1
		if m := fenceOpen.FindStringSubmatch(l); m != nil {
			flush(n - 1)
			fence := strings.TrimSpace(m[1])
			var code []string
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), fence) {
				code = append(code, lines[j])
				j++
			}
			if c := strings.TrimRight(strings.Join(code, "\n"), "\n "); strings.TrimSpace(c) != "" {
				out = append(out, Source{Kind: "kb", File: file, Line: n + 1, EndLine: j, Heading: heading, Code: true, Text: c})
			}
			i = j
			continue
		}
		if m := mdHeading.FindStringSubmatch(l); m != nil {
			flush(n - 1)
			heading = m[2]
			continue
		}
		if strings.TrimSpace(l) == "" || commentLine.MatchString(l) {
			flush(n - 1)
			continue
		}
		if t := strings.TrimSpace(l); strings.HasPrefix(t, ": ") && len(cur) > 0 {
			// definition list: "term" then ":   definition"
			cur[len(cur)-1] += ":"
			l = strings.TrimSpace(t[1:])
		} else if listStart.MatchString(l) {
			flush(n - 1)
			l = listStart.ReplaceAllString(l, "")
		}
		if len(cur) == 0 {
			start = n
		}
		cur = append(cur, strings.TrimSpace(l))
	}
	flush(len(lines))
	return out
}

var (
	vttTime  = regexp.MustCompile(`^(\d{1,2}:)?(\d{2}):(\d{2})[.,]\d{3}\s+-->`)
	vttVoice = regexp.MustCompile(`^<v(?:\.[\w.-]+)?\s+([^>]+)>`)
	tagRe    = regexp.MustCompile(`<[^>]+>`)
	txtStamp = regexp.MustCompile(`^\[?(\d{1,2}:\d{2}(?::\d{2})?)\]?\s+`)
	speaker  = regexp.MustCompile(`^([A-Z][\w .'-]{0,40}?):\s+(.*)$`)
)

// LoadTranscript reads a WebVTT, SRT or plain-text transcript into passages:
// consecutive cues by the same speaker, up to about 80 words each.
func LoadTranscript(path string) ([]Source, error) {
	if isURL(path) {
		return nil, fmt.Errorf("%s: transcripts must be local files", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := filepath.Base(path)
	type cue struct {
		time, who, text string
		line            int
	}
	var cues []cue
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	timed := strings.EqualFold(filepath.Ext(path), ".vtt") || strings.EqualFold(filepath.Ext(path), ".srt")
	n := 0
	curTime, curLine := "", 0
	var curText []string
	end := func() {
		if len(curText) == 0 {
			return
		}
		t := strings.Join(curText, " ")
		who := ""
		if m := vttVoice.FindStringSubmatch(t); m != nil {
			who = strings.TrimSpace(m[1])
		}
		t = strings.TrimSpace(tagRe.ReplaceAllString(t, ""))
		if m := speaker.FindStringSubmatch(t); m != nil && who == "" {
			who, t = m[1], m[2]
		}
		cues = append(cues, cue{curTime, who, t, curLine})
		curText = nil
	}
	for sc.Scan() {
		n++
		l := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if timed {
			switch {
			case l == "":
				end()
			case n == 1 && strings.HasPrefix(l, "WEBVTT"), strings.HasPrefix(l, "NOTE"):
			case vttTime.MatchString(l):
				end()
				m := vttTime.FindStringSubmatch(l)
				h := strings.TrimSuffix(m[1], ":")
				if h == "" {
					h = "0"
				}
				curTime, curLine = fmt.Sprintf("%02s:%s:%s", h, m[2], m[3]), n
			case isDigits(l) && len(curText) == 0:
				// SRT cue number
			default:
				if curLine == 0 {
					curLine = n
				}
				curText = append(curText, l)
			}
			continue
		}
		if l == "" {
			end()
			continue
		}
		if m := txtStamp.FindStringSubmatch(l); m != nil {
			end()
			curTime = m[1]
			l = l[len(m[0]):]
		} else if len(curText) == 0 {
			curTime = ""
		}
		if speaker.MatchString(l) && len(curText) > 0 {
			end()
		}
		if len(curText) == 0 {
			curLine = n
		}
		curText = append(curText, l)
	}
	end()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// merge consecutive cues by the same speaker
	var out []Source
	for _, c := range cues {
		if c.text == "" {
			continue
		}
		if k := len(out) - 1; k >= 0 && out[k].Speaker == c.who && len(strings.Fields(out[k].Text)) < 80 {
			out[k].Text += " " + c.text
			out[k].EndLine = c.line
			continue
		}
		out = append(out, Source{Kind: "transcript", File: name, Line: c.line, EndLine: c.line, Time: c.time, Speaker: c.who, Text: c.text})
	}
	return out, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
