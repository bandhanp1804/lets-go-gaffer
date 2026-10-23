package assistant

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The prompt asks the model to cite real stories and to quote numbers rather than
// calculate them. These checks verify it, so a lapse is shown to the manager instead
// of trusted.

var (
	citationRE = regexp.MustCompile(`\[(\d+)\]`)
	listMarkRE = regexp.MustCompile(`(?m)^\s*\d+[.)]\s`)
	numberRE   = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// citations returns the distinct valid references the text cites, as sources, and the
// distinct references it cites that do not exist, both ascending.
func citations(text string, r *refs) (sources []Source, bad []int) {
	seen := map[int]bool{}
	var valid []int
	for _, m := range citationRE.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		if _, ok := r.source(n); ok {
			valid = append(valid, n)
		} else {
			bad = append(bad, n)
		}
	}
	sort.Ints(valid)
	sort.Ints(bad)
	for _, n := range valid {
		s, _ := r.source(n)
		sources = append(sources, s)
	}
	return sources, bad
}

// normalise puts a number in a canonical form so that "6.70" and "6.7" are the same
// figure: a tool prints 6 where the model may write 6.0.
func normalise(token string) string {
	f, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return token
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// numbersIn lists the numbers in text, as written, in order of appearance.
func numbersIn(text string) []string { return numberRE.FindAllString(text, -1) }

// ungrounded returns the numbers in an answer that appear in none of the tool results
// and not in anything the manager wrote, in order of appearance, each once. Citation
// markers and list numbering are not figures and are ignored.
func ungrounded(answer string, known map[string]bool) []string {
	text := citationRE.ReplaceAllString(answer, " ")
	text = listMarkRE.ReplaceAllString(text, "\n")
	var out []string
	seen := map[string]bool{}
	for _, tok := range numbersIn(text) {
		n := normalise(tok)
		if known[n] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, tok)
	}
	return out
}

// Warnings describes, in plain words, everything the checks found.
func (a Answer) Warnings() []string {
	var w []string
	if len(a.BadCitations) > 0 {
		refs := make([]string, len(a.BadCitations))
		for i, n := range a.BadCitations {
			refs[i] = fmt.Sprintf("[%d]", n)
		}
		w = append(w, fmt.Sprintf("Cited %s, which is not one of the stories retrieved.", strings.Join(refs, ", ")))
	}
	if a.UncitedNews {
		w = append(w, "News was searched but no story is cited, so the news claims here are not backed by a source.")
	}
	if len(a.Ungrounded) > 0 {
		w = append(w, fmt.Sprintf("These numbers appear in no tool result and may have been calculated or invented: %s.", strings.Join(a.Ungrounded, ", ")))
	}
	if a.Truncated {
		w = append(w, "The answer was cut off at the token limit.")
	}
	return w
}
