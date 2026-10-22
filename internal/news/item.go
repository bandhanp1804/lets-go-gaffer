// Package news gathers football news, links it to players, embeds it, and hands
// it to storage for semantic search.
package news

import (
	"strings"
	"time"
)

// Item is one news story or note, from any source.
type Item struct {
	Source      string // "bbc", "sky", "fpl", ...
	Title       string
	Summary     string // may be empty (e.g. a live blog)
	URL         string // canonical; the identity used to avoid storing a story twice
	PublishedAt time.Time
	PlayerIDs   []int // FPL players the item is about; sources may pre-fill, else the linker does
}

// Text is what gets embedded: the title and, when there is one, the summary.
func (i Item) Text() string {
	if i.Summary == "" {
		return i.Title
	}
	sep := ". "
	if strings.HasSuffix(i.Title, ".") || strings.HasSuffix(i.Title, "?") || strings.HasSuffix(i.Title, "!") {
		sep = " "
	}
	return i.Title + sep + i.Summary
}

// Embedded is an item together with the vector of its Text.
type Embedded struct {
	Item
	Embedding []float32
}
