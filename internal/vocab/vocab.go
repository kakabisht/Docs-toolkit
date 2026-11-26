// Package vocab loads the controlled vocabulary and alias file shared by
// check (tags), graph (entity resolution) and audit (terminology).
//
// File format (YAML):
//
//	terms:
//	  OpenTelemetry:
//	    aliases: [OTel, otel]
//	    type: product            # optional entity type
//	    description: Observability framework
//	  Kubernetes:
//	    aliases: [k8s]
//	  tracing: {}                # a term with no aliases
package vocab

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Term is one preferred term.
type Term struct {
	Name        string   `yaml:"-" json:"name"`
	Aliases     []string `yaml:"aliases" json:"aliases,omitempty"`
	Type        string   `yaml:"type" json:"type,omitempty"`
	Description string   `yaml:"description" json:"description,omitempty"`
}

// Vocab maps every spelling to its preferred term.
type Vocab struct {
	Terms map[string]*Term // preferred name -> term
	index map[string]string
	alias map[string]bool
}

// Load reads a vocabulary file. An empty path gives an empty vocabulary.
func Load(path string) (*Vocab, error) {
	v := &Vocab{Terms: map[string]*Term{}, index: map[string]string{}, alias: map[string]bool{}}
	if path == "" {
		return v, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Terms map[string]*Term `yaml:"terms"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, t := range doc.Terms {
		if t == nil {
			t = &Term{}
		}
		v.Add(name, t)
	}
	return v, nil
}

// Add registers a term and its aliases.
func (v *Vocab) Add(name string, t *Term) {
	t.Name = name
	v.Terms[name] = t
	v.index[strings.ToLower(name)] = name
	for _, a := range t.Aliases {
		k := strings.ToLower(a)
		if _, taken := v.index[k]; !taken {
			v.index[k] = name
			v.alias[k] = true
		}
	}
}

// Empty reports whether no terms are defined.
func (v *Vocab) Empty() bool { return v == nil || len(v.Terms) == 0 }

// Canonical returns the preferred term for s, whether s is known, and
// whether s is an alias (rather than the preferred spelling).
func (v *Vocab) Canonical(s string) (name string, known, isAlias bool) {
	if v == nil {
		return s, false, false
	}
	k := strings.ToLower(strings.TrimSpace(s))
	name, known = v.index[k]
	if !known {
		return s, false, false
	}
	return name, true, v.alias[k] || name != strings.TrimSpace(s) && !strings.EqualFold(name, s)
}

// Spellings returns every known spelling (preferred names and aliases), longest first.
func (v *Vocab) Spellings() []string {
	var out []string
	for name, t := range v.Terms {
		out = append(out, name)
		out = append(out, t.Aliases...)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}
