package kg

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/model"
)

var wordRe = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}_'-]*`)

// stopwords are left out of similarity; a short English list keeps it predictable.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a about above after again all also an and any are as at be because been
before being below between both but by can could did do does doing down during each few for from further
had has have having here how i if in into is it its itself just may more most must no nor not now of off
on once only or other our out over own same should so some such than that the their them then there these
they this those through to too under until up use used using very was we were what when where which while
who why will with would you your`) {
		stopwords[w] = true
	}
}

func tokens(s string) []string {
	var out []string
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if len(w) > 1 && !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

func pageText(p *model.Page) (weighted []string, prose string) {
	// title and headings count three times, as authors put the topic there
	head := p.Title
	for _, h := range p.Headings {
		head += " " + h.Text
	}
	var sb strings.Builder
	for _, para := range p.Paragraphs {
		sb.WriteString(para.Text)
		sb.WriteByte(' ')
	}
	prose = sb.String()
	t := tokens(head)
	weighted = append(append(append(weighted, t...), t...), t...)
	weighted = append(weighted, tokens(p.Description)...)
	weighted = append(weighted, tokens(prose)...)
	return weighted, prose
}

// similarity adds related-to edges (TF-IDF cosine over title, headings and
// prose) and overlaps-with edges (shared 5-word shingles, a sign of copied
// or duplicated content). Both are inferred and carry their score.
func (b *builder) similarity() {
	var pages []*model.Page
	for _, p := range b.s.Pages {
		if p.Err == "" && b.g.Node(PageID(p.Key)) != nil {
			pages = append(pages, p)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Key < pages[j].Key })
	n := len(pages)
	if n < 2 {
		return
	}
	tfs := make([]map[string]float64, n)
	shingles := make([]map[string]bool, n)
	df := map[string]int{}
	for i, p := range pages {
		w, prose := pageText(p)
		tf := map[string]float64{}
		for _, t := range w {
			tf[t]++
		}
		for t := range tf {
			df[t]++
		}
		tfs[i] = tf
		shingles[i] = shingleSet(prose, 5)
	}
	// TF-IDF vectors, unit length
	vecs := make([]map[string]float64, n)
	for i, tf := range tfs {
		v := map[string]float64{}
		var norm float64
		for t, c := range tf {
			idf := math.Log(float64(n+1)/float64(df[t]+1)) + 1
			x := (1 + math.Log(c)) * idf
			v[t] = x
			norm += x * x
		}
		norm = math.Sqrt(norm)
		for t := range v {
			v[t] /= norm
		}
		vecs[i] = v
	}
	// inverted index, so cost follows shared terms rather than n²
	post := map[string][]int{}
	for i, v := range vecs {
		for t := range v {
			if df[t] < n { // a term on every page says nothing
				post[t] = append(post[t], i)
			}
		}
	}
	for i := range pages {
		scores := map[int]float64{}
		for t, x := range vecs[i] {
			for _, j := range post[t] {
				if j != i {
					scores[j] += x * vecs[j][t]
				}
			}
		}
		type cand struct {
			j int
			s float64
		}
		var cs []cand
		for j, s := range scores {
			if s >= b.opt.RelatedThreshold {
				cs = append(cs, cand{j, s})
			}
		}
		sort.Slice(cs, func(a, c int) bool {
			if cs[a].s != cs[c].s {
				return cs[a].s > cs[c].s
			}
			return pages[cs[a].j].Key < pages[cs[c].j].Key
		})
		if len(cs) > b.opt.MaxRelated {
			cs = cs[:b.opt.MaxRelated]
		}
		for _, c := range cs {
			// related-to is symmetric: one edge per pair, from the page that sorts first
			a, z := i, c.j
			if z < a {
				a, z = z, a
			}
			conf := math.Round(c.s*100) / 100
			b.edge(Edge{From: PageID(pages[a].Key), To: PageID(pages[z].Key), Type: ERelated, Derivation: Inferred,
				File: fileOf(pages[a]), Confidence: conf,
				Span: fmt.Sprintf("cosine %.2f over title, headings and text; shared terms: %s", c.s, strings.Join(topShared(vecs[a], vecs[z], 5), ", "))})
		}
	}
	// overlaps-with: Jaccard over shingles, via an inverted index
	spost := map[string][]int{}
	for i, set := range shingles {
		for sh := range set {
			spost[sh] = append(spost[sh], i)
		}
	}
	for i := range pages {
		if len(shingles[i]) == 0 {
			continue
		}
		shared := map[int]int{}
		for sh := range shingles[i] {
			for _, j := range spost[sh] {
				if j > i {
					shared[j]++
				}
			}
		}
		js := make([]int, 0, len(shared))
		for j := range shared {
			js = append(js, j)
		}
		sort.Ints(js)
		for _, j := range js {
			inter := shared[j]
			// overlap relative to the smaller page, so a short page copied
			// into a long one still counts
			small := min(len(shingles[i]), len(shingles[j]))
			if small < 10 {
				continue
			}
			ov := float64(inter) / float64(small)
			if ov < b.opt.OverlapThreshold {
				continue
			}
			conf := math.Round(ov*100) / 100
			span := fmt.Sprintf("%d of %d five-word sequences shared (%.0f%%)", inter, small, ov*100)
			b.edge(Edge{From: PageID(pages[i].Key), To: PageID(pages[j].Key), Type: EOverlaps, Derivation: Inferred, File: fileOf(pages[i]), Confidence: conf, Span: span})
		}
	}
}

func shingleSet(text string, k int) map[string]bool {
	var w []string
	for _, t := range wordRe.FindAllString(strings.ToLower(text), -1) {
		w = append(w, t)
	}
	out := map[string]bool{}
	for i := 0; i+k <= len(w); i++ {
		out[strings.Join(w[i:i+k], " ")] = true
	}
	return out
}

func topShared(a, b map[string]float64, k int) []string {
	type tw struct {
		t string
		w float64
	}
	var ts []tw
	for t, x := range a {
		if y, ok := b[t]; ok {
			ts = append(ts, tw{t, x * y})
		}
	}
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].w != ts[j].w {
			return ts[i].w > ts[j].w
		}
		return ts[i].t < ts[j].t
	})
	var out []string
	for i := 0; i < len(ts) && i < k; i++ {
		out = append(out, ts[i].t)
	}
	return out
}
