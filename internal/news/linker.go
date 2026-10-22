package news

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

const minNameLen = 3

// initialPrefix matches the "B." in FPL web names like "B.Fernandes", which FPL
// adds only when a surname is shared.
var initialPrefix = regexp.MustCompile(`^\p{L}\.`)

type candidate struct{ playerID, teamID int }

// Linker finds which FPL players a piece of text is about.
//
// This is deliberately conservative: a missed link costs a little recall, but a
// wrong link puts a story on the wrong player. Rules:
//   - a name must start with a capital letter in the text, so "rice" (food) is not
//     the Arsenal midfielder Rice;
//   - names shorter than three letters are ignored;
//   - a surname shared by several players only links when the text also names
//     one of their clubs;
//   - accents are not folded yet ("Joao" does not match "João"); a proper alias
//     table is planned.
type Linker struct {
	names    map[string][]candidate // lower-case name phrase -> players with it
	maxWords int
	teams    map[int]string // team id -> lower-case club name, padded for phrase search
}

// NewLinker indexes the players' names and their clubs.
func NewLinker(players []fpl.Player, teams []fpl.Team) *Linker {
	l := &Linker{names: map[string][]candidate{}, maxWords: 1, teams: map[int]string{}}
	for _, t := range teams {
		l.teams[t.ID] = " " + strings.Join(lowerAll(words(t.Name)), " ") + " "
	}
	for _, p := range players {
		toks := words(initialPrefix.ReplaceAllString(p.WebName, ""))
		phrase := strings.Join(lowerAll(toks), " ")
		if len(phrase) < minNameLen {
			continue
		}
		l.names[phrase] = append(l.names[phrase], candidate{p.ID, p.TeamID})
		l.maxWords = max(l.maxWords, len(toks))
	}
	return l
}

// Link returns the ascending, de-duplicated ids of the players text mentions.
func (l *Linker) Link(text string) []int {
	toks := words(text)
	lower := lowerAll(toks)
	haystack := " " + strings.Join(lower, " ") + " "

	found := map[int]bool{}
	for i := 0; i < len(toks); {
		matched := 0
		// Prefer the longest phrase starting here.
		for n := min(l.maxWords, len(toks)-i); n >= 1; n-- {
			cands, ok := l.names[strings.Join(lower[i:i+n], " ")]
			if !ok || !startsUpper(toks[i]) {
				continue
			}
			for _, c := range l.resolve(cands, haystack) {
				found[c.playerID] = true
			}
			matched = n
			break
		}
		if matched > 0 {
			i += matched
		} else {
			i++
		}
	}

	ids := make([]int, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// resolve returns the candidates a name refers to. A lone candidate is accepted
// as is; several need their club to appear in the text.
func (l *Linker) resolve(cands []candidate, haystack string) []candidate {
	if len(cands) == 1 {
		return cands
	}
	var out []candidate
	for _, c := range cands {
		if team, ok := l.teams[c.teamID]; ok && strings.Contains(haystack, team) {
			out = append(out, c)
		}
	}
	return out
}

// words splits on anything that is not a letter or digit, so "Saka's" gives
// "Saka" and "s".
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func lowerAll(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = strings.ToLower(w)
	}
	return out
}

func startsUpper(w string) bool {
	for _, r := range w {
		return unicode.IsUpper(r)
	}
	return false
}

// PlayerNewsItems turns FPL's official per-player notes into news items. Notes
// without text or without a timestamp are skipped. Each note gets a URL that
// includes its timestamp, so a player's later updates are stored as new items
// instead of overwriting history.
func PlayerNewsItems(players []fpl.Player, teams []fpl.Team) []Item {
	club := make(map[int]string, len(teams))
	for _, t := range teams {
		club[t.ID] = t.Name
	}
	var items []Item
	for _, p := range players {
		if p.News == "" || p.NewsAdded == nil {
			continue
		}
		added := p.NewsAdded.UTC()
		items = append(items, Item{
			Source:      "fpl",
			Title:       fmt.Sprintf("%s (%s): %s", p.WebName, club[p.TeamID], p.News),
			URL:         fmt.Sprintf("fpl://player/%d/%s", p.ID, added.Format("2006-01-02T15:04:05Z")),
			PublishedAt: added,
			PlayerIDs:   []int{p.ID},
		})
	}
	return items
}

// PlayerNewsSource adapts a fixed slice of items (such as PlayerNewsItems) to a Source.
type PlayerNewsSource struct{ items []Item }

func NewPlayerNewsSource(items []Item) *PlayerNewsSource { return &PlayerNewsSource{items: items} }
