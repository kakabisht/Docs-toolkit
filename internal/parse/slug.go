package parse

import (
	"strconv"
	"strings"
	"unicode"
)

// Slugify turns heading text into a GitHub/Hugo style anchor:
// lowercase, punctuation dropped, spaces to hyphens.
func Slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	return b.String()
}

// slugger hands out unique anchors the way GitHub and Hugo do: foo, foo-1, foo-2.
type slugger map[string]int

func (s slugger) unique(base string) string {
	n, seen := s[base]
	s[base] = n + 1
	if !seen {
		return base
	}
	for {
		cand := base + "-" + strconv.Itoa(n)
		if _, taken := s[cand]; !taken {
			s[cand] = 1
			return cand
		}
		n++
	}
}
