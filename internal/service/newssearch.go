package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

const (
	// staleNews is how old the newest stored story can be before the result says so.
	staleNews = 36 * time.Hour
	// looseMatch is the similarity below which the closest stories are only loosely related to the
	// question: with a player named, good matches scored 0.49 to 0.54, and unrelated ones 0.34 to 0.38.
	looseMatch = 0.40
)

// NewsPlayerView is the player a search was narrowed to, with what FPL says of him now.
type NewsPlayerView struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Team     string `json:"team"`
	Position string `json:"position"`
	Price    string `json:"price"`
	Status   string `json:"status"`
	News     string `json:"news,omitempty"` // FPL's own note about him, if it has one
}

// NewsSearchView is the answer to a news search: the stories, and what the search did.
type NewsSearchView struct {
	Stories []NewsView `json:"stories"`
	// Player is set when the search was narrowed to one player. Detected says he was found in the
	// question rather than given, so the page can offer to search everything instead.
	Player   *NewsPlayerView `json:"player"`
	Detected bool            `json:"player_detected"`
	// Notes say what the search did and what to make of it: a player was detected, there is nothing
	// about him, the matches are loose, the stored news is old.
	Notes []string `json:"notes"`
	// NewestAge is how old the newest stored story is ("3d"), empty when there are none.
	NewestAge string `json:"newest_story_age"`
}

// Search finds the stories closest in meaning to a question. If the question names one player
// (accents and case ignored, so "joao pedro" finds João Pedro) it is narrowed to stories about him,
// because otherwise the word "injured" matches injury notes for anyone. It says plainly when there
// is nothing stored about him, and how old the stored news is.
func (s *Service) Search(ctx context.Context, q NewsQuery) (NewsSearchView, error) {
	if s.deps.Embedder == nil {
		return NewsSearchView{}, ErrNewsUnavailable
	}
	query := strings.TrimSpace(q.Query)
	switch {
	case query == "":
		return NewsSearchView{}, badInput("a news query is required")
	case q.Days < 0:
		return NewsSearchView{}, badInput("days cannot be negative")
	}
	limit := q.Limit
	if limit < 1 {
		limit = defaultNews
	}
	limit = min(limit, maxNews)

	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return NewsSearchView{}, err
	}
	v := NewsSearchView{Stories: []NewsView{}, Notes: []string{}}
	filter := db.SearchFilter{Limit: limit}

	var who *fpl.Player
	switch name := strings.TrimSpace(q.Player); {
	case name != "":
		p, err := resolveAny(base, name, nil)
		if err != nil {
			return NewsSearchView{}, err
		}
		who = &p
	case !q.NoDetect:
		var note string
		who, note = detectPlayer(query, base.players, base.short)
		v.Detected = who != nil
		if note != "" {
			v.Notes = append(v.Notes, note)
		}
	}
	if who != nil {
		filter.PlayerIDs = []int{who.ID}
		pv := base.view(*who, 0)
		v.Player = &NewsPlayerView{ID: who.ID, Name: who.WebName, Team: pv.Team, Position: pv.Position, Price: pv.Price, Status: pv.Status, News: who.News}
	}

	now := s.deps.Now()
	if q.Days > 0 {
		filter.Since = now.Add(-time.Duration(q.Days) * 24 * time.Hour)
	}
	// A question is embedded as a Query, not a Document: Voyage embeds them differently.
	vecs, err := s.deps.Embedder.Embed(ctx, []string{query}, embed.Query)
	if err != nil {
		return NewsSearchView{}, fmt.Errorf("embed question: %w", err)
	}
	hits, err := s.deps.Store.SearchNews(ctx, vecs[0], filter)
	if err != nil {
		return NewsSearchView{}, err
	}
	best := 0.0
	for _, h := range hits {
		if news.IsBoilerplate(h.Title) { // old rows from before such link text was filtered out at ingestion
			continue
		}
		nv := NewsView{
			Title: h.Title, Content: h.Content, Source: h.Source, URL: h.URL,
			Published: h.PublishedAt.UTC().Format(time.RFC3339), Age: age(now, h.PublishedAt), Similarity: round2(h.Similarity),
		}
		for _, id := range h.PlayerIDs {
			if n := nameOf(base.players, id); n != "" {
				nv.Players = append(nv.Players, n)
			}
		}
		best = max(best, nv.Similarity)
		v.Stories = append(v.Stories, nv)
	}

	switch {
	case who != nil && len(v.Stories) == 0:
		v.Notes = append(v.Notes, nothingAbout(who, v.Player))
	case who != nil && v.Detected:
		v.Notes = append(v.Notes, fmt.Sprintf("Showing stories about %s, who is named in your question.", who.WebName))
	case who == nil && len(v.Stories) > 0 && best < looseMatch:
		v.Notes = append(v.Notes, fmt.Sprintf("These are loose matches (the best scores %.2f): there may be nothing stored about this. Name a player to narrow the search.", best))
	}
	if c, err := s.deps.Store.CountNews(ctx); err == nil && !c.Newest.IsZero() {
		v.NewestAge = age(now, c.Newest)
		if now.Sub(c.Newest) > staleNews {
			v.Notes = append(v.Notes, fmt.Sprintf("The newest stored story is %s old. Run `gaffer news` to fetch the latest.", v.NewestAge))
		}
	}
	return v, nil
}

// nothingAbout says there is no stored news about a player and gives FPL's own word on him.
func nothingAbout(p *fpl.Player, pv *NewsPlayerView) string {
	m := fmt.Sprintf("No stored story mentions %s.", p.WebName)
	if pv.News != "" {
		return m + fmt.Sprintf(" FPL lists him as %s: %s", pv.Status, pv.News)
	}
	return m + fmt.Sprintf(" FPL lists him as %s, with no injury or availability note.", pv.Status)
}

// SearchNews is Search for callers that want only the stories, such as the assistant.
func (s *Service) SearchNews(ctx context.Context, q NewsQuery) ([]NewsView, error) {
	v, err := s.Search(ctx, q)
	return v.Stories, err
}

// detectPlayer finds the one player a question names. It reads whole words and ignores accents and
// case. Of the names found, one inside a longer name is dropped ("pedro" inside "joao pedro"); if
// that leaves several different players the question is about none of them in particular, and if two
// players share the name it cannot tell which is meant. Either way nothing is detected and the note
// says so.
func detectPlayer(query string, players []fpl.Player, short map[int]string) (*fpl.Player, string) {
	// "Pedro's knee" names Pedro: the possessive is not part of the name.
	q := " " + fold(strings.NewReplacer("'s ", " ", "’s ", " ", "'S ", " ", "’S ", " ").Replace(query+" ")) + " "
	type found struct {
		p      fpl.Player
		phrase string
	}
	var hits []found
	for _, p := range players {
		seen := map[string]bool{}
		for _, name := range []string{p.WebName, p.FirstName + " " + p.LastName} {
			f := fold(name)
			if len(f) < 3 || seen[f] || !strings.Contains(q, " "+f+" ") {
				continue
			}
			seen[f] = true
			hits = append(hits, found{p, f})
		}
	}
	if len(hits) == 0 {
		return nil, ""
	}
	// A name inside a longer name that was also found is part of it, not another player.
	var kept []found
	for _, h := range hits {
		inside := false
		for _, o := range hits {
			if o.phrase != h.phrase && strings.Contains(" "+o.phrase+" ", " "+h.phrase+" ") {
				inside = true
			}
		}
		if !inside {
			kept = append(kept, h)
		}
	}
	ids := map[int]fpl.Player{}
	phrases := map[string]bool{}
	for _, h := range kept {
		ids[h.p.ID] = h.p
		phrases[h.phrase] = true
	}
	switch {
	case len(ids) == 1:
		for _, p := range ids {
			return &p, ""
		}
	case len(phrases) == 1: // one name, several players
		var list []string
		for _, h := range kept {
			list = append(list, fmt.Sprintf("%s (%s)", h.p.WebName, short[h.p.TeamID]))
		}
		sort.Strings(list)
		return nil, fmt.Sprintf("More than one player fits that name (%s): put his full name in the Player box to narrow the search.", strings.Join(list, ", "))
	}
	return nil, "Several players are named in your question, so all stored news is searched. Use the Player box to look at one."
}
