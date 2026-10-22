package projection

import (
	"math"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

const (
	// minPool is the fewest players a gameweek needs to be worth judging.
	minPool = 30
	topN    = 20
	// lookback is how many earlier gameweeks decide whether a player is "in the pool".
	lookback = 3
)

// Metrics summarises how well a predictor ranked players for the gameweeks it was
// tested on, averaged over those gameweeks.
type Metrics struct {
	Spearman float64 // rank correlation between predicted and actual points; 1 is perfect, 0 is noise
	MAE      float64 // mean absolute error in points
	TopMean  float64 // mean actual points of the 20 players it ranked highest
	Weeks    int     // gameweeks judged
}

// Evaluate backtests a predictor. For each gameweek from..to it asks predictor
// (given that gameweek) to score players, and compares with what they actually
// scored. Only players who played in one of the previous three gameweeks are
// judged, since those are the ones a manager would consider. A gameweek with
// fewer than 30 such players is skipped.
func Evaluate(players []fpl.Player, history []fpl.PlayerHistory, from, to int, predictor func(next int) func(fpl.Player) float64) Metrics {
	type key struct{ player, gw int }
	points, minutes := map[key]float64{}, map[key]float64{}
	for _, h := range history {
		k := key{h.PlayerID, h.GameweekID}
		points[k] += float64(h.TotalPoints)
		minutes[k] += float64(h.Minutes)
	}

	var m Metrics
	for next := max(from, 2); next <= to; next++ {
		var pool []fpl.Player
		for _, p := range players {
			for g := max(1, next-lookback); g < next; g++ {
				if minutes[key{p.ID, g}] > 0 {
					pool = append(pool, p)
					break
				}
			}
		}
		if len(pool) < minPool {
			continue
		}

		score := predictor(next)
		predicted := make([]float64, len(pool))
		actual := make([]float64, len(pool))
		var absErr float64
		for i, p := range pool {
			predicted[i], actual[i] = score(p), points[key{p.ID, next}]
			absErr += math.Abs(predicted[i] - actual[i])
		}
		idx := make([]int, len(pool))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return predicted[idx[a]] > predicted[idx[b]] })
		var top float64
		n := min(topN, len(idx))
		for _, i := range idx[:n] {
			top += actual[i]
		}

		m.Spearman += Spearman(predicted, actual)
		m.MAE += absErr / float64(len(pool))
		m.TopMean += top / float64(n)
		m.Weeks++
	}
	if m.Weeks > 0 {
		w := float64(m.Weeks)
		m.Spearman, m.MAE, m.TopMean = m.Spearman/w, m.MAE/w, m.TopMean/w
	}
	return m
}

// Spearman is the rank correlation of two equal-length series: ties share the
// average of the ranks they span. It is 0 when either series is constant.
func Spearman(a, b []float64) float64 {
	if len(a) != len(b) || len(a) < 2 {
		return 0
	}
	ra, rb := ranks(a), ranks(b)
	var ma, mb float64
	for i := range ra {
		ma += ra[i]
		mb += rb[i]
	}
	ma /= float64(len(ra))
	mb /= float64(len(rb))
	var cov, va, vb float64
	for i := range ra {
		cov += (ra[i] - ma) * (rb[i] - mb)
		va += (ra[i] - ma) * (ra[i] - ma)
		vb += (rb[i] - mb) * (rb[i] - mb)
	}
	if va == 0 || vb == 0 {
		return 0
	}
	return cov / math.Sqrt(va*vb)
}

func ranks(v []float64) []float64 {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
	out := make([]float64, len(v))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && v[idx[j+1]] == v[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[idx[k]] = avg
		}
		i = j + 1
	}
	return out
}

// FormBaseline predicts like the old scorer: the average points per appearance
// over the last three gameweeks, ignoring minutes, opponents, and the rest of
// the season. It is the bar the model has to clear.
func FormBaseline(history []fpl.PlayerHistory, next int) func(fpl.Player) float64 {
	pts, apps := map[int]float64{}, map[int]float64{}
	for _, h := range history {
		if h.GameweekID >= max(1, next-lookback) && h.GameweekID < next && h.Minutes > 0 {
			pts[h.PlayerID] += float64(h.TotalPoints)
			apps[h.PlayerID]++
		}
	}
	return func(p fpl.Player) float64 {
		if apps[p.ID] == 0 {
			return 0
		}
		return pts[p.ID] / apps[p.ID]
	}
}

// SeasonBaseline predicts each player's season-to-date points per gameweek.
func SeasonBaseline(history []fpl.PlayerHistory, next int) func(fpl.Player) float64 {
	pts := map[int]float64{}
	for _, h := range history {
		if h.GameweekID < next {
			pts[h.PlayerID] += float64(h.TotalPoints)
		}
	}
	return func(p fpl.Player) float64 { return pts[p.ID] / float64(max(next-1, 1)) }
}
