package parse

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// FrontMatter holds the parsed front matter: the raw map (for schema
// validation) plus the fields the tool itself uses.
type FrontMatter struct {
	Raw         map[string]any
	Err         error // parse error, if any
	Format      string
	Lines       int // lines taken by the front matter block, delimiters included
	Title       string
	LinkTitle   string
	Description string
	Aliases     []string
	Slug        string
	URL         string
	Draft       bool
	Tags        []string
	Type        string // content type: tutorial, how-to, reference, explanation ...
	Audience    []string
	Owner       string
	Version     string
	ID          string // Docusaurus id
	Status      string
}

// SplitFrontMatter separates YAML (---) or TOML (+++) front matter from the
// body. It returns the parsed fields, the body and the number of lines removed.
func SplitFrontMatter(src []byte) (FrontMatter, []byte, int) {
	fm := FrontMatter{Raw: map[string]any{}}
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	for _, delim := range []string{"---", "+++"} {
		if !bytes.HasPrefix(src, []byte(delim+"\n")) && !bytes.HasPrefix(src, []byte(delim+"\r\n")) {
			continue
		}
		rest := src[bytes.IndexByte(src, '\n')+1:]
		var head, body []byte
		if bytes.HasPrefix(rest, []byte(delim)) {
			head, body = nil, rest[len(delim):]
		} else {
			end := bytes.Index(rest, []byte("\n"+delim))
			if end < 0 {
				fm.Err = fmt.Errorf("front matter opened with %s but never closed", delim)
				return fm, src, 0
			}
			head = rest[:end]
			body = rest[end+1+len(delim):]
		}
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			body = body[i+1:]
		} else {
			body = nil
		}
		if delim == "---" {
			fm.Format = "yaml"
			if err := yaml.Unmarshal(head, &fm.Raw); err != nil {
				fm.Err = err
			}
		} else {
			fm.Format = "toml"
			if _, err := toml.Decode(string(head), &fm.Raw); err != nil {
				fm.Err = err
			}
		}
		if fm.Raw == nil {
			fm.Raw = map[string]any{}
		}
		fm.fill()
		fm.Lines = bytes.Count(src[:len(src)-len(body)], []byte("\n"))
		return fm, body, fm.Lines
	}
	return fm, src, 0
}

func (fm *FrontMatter) fill() {
	r := fm.Raw
	fm.Title = str(r["title"])
	fm.LinkTitle = str(first(r, "linkTitle", "sidebar_label", "nav_title"))
	fm.Description = str(first(r, "description", "summary"))
	fm.Aliases = strs(first(r, "aliases", "redirect_from"))
	fm.Slug = str(r["slug"])
	fm.URL = str(r["url"])
	if p := str(r["permalink"]); p != "" && fm.URL == "" {
		fm.URL = p
	}
	fm.Draft, _ = r["draft"].(bool)
	fm.Tags = strs(first(r, "tags", "keywords"))
	fm.Type = strings.ToLower(str(first(r, "content_type", "contentType", "doc_type", "diataxis")))
	if fm.Type == "" {
		// "type" is overloaded (Hugo layouts), so only accept known content types
		if t := strings.ToLower(str(r["type"])); KnownContentType(t) {
			fm.Type = t
		}
	}
	fm.Audience = strs(r["audience"])
	fm.Owner = str(first(r, "owner", "owners", "author", "maintainer"))
	fm.Version = str(first(r, "version", "versions"))
	fm.ID = str(r["id"])
	fm.Status = strings.ToLower(str(r["status"]))
	if params, ok := r["params"].(map[string]any); ok { // Hugo nests custom keys under params
		if fm.Type == "" {
			fm.Type = strings.ToLower(str(first(params, "content_type", "contentType", "doc_type")))
		}
		if fm.Owner == "" {
			fm.Owner = str(first(params, "owner", "owners"))
		}
		if len(fm.Audience) == 0 {
			fm.Audience = strs(params["audience"])
		}
	}
}

// KnownContentType reports whether t is a Diátaxis-style content type.
func KnownContentType(t string) bool {
	switch t {
	case "tutorial", "how-to", "howto", "how-to-guide", "guide", "reference", "explanation", "concept", "conceptual", "troubleshooting", "release-notes", "landing", "overview":
		return true
	}
	return false
}

func first(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func str(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			return str(v[0])
		}
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
	return ""
}

func strs(v any) []string {
	switch v := v.(type) {
	case string:
		if v == "" {
			return nil
		}
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []any:
		var out []string
		for _, x := range v {
			if s := str(x); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}
