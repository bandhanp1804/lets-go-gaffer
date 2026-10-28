package telemetry

// Tokens is what one Claude request used, as its response reports.
type Tokens struct {
	Input      int // uncached input
	Output     int
	CacheRead  int // input served from the prompt cache
	CacheWrite int // input written to the prompt cache (5-minute lifetime)
}

// rate is a model's price in US dollars per million tokens.
type rate struct{ input, output, cacheRead, cacheWrite float64 }

// prices are Anthropic's list prices (2026-10). A 5-minute cache write costs 1.25x input; a cache
// read 0.1x, except on Sonnet 5.5 and Opus 5.5, where it is a flat $0.20.
var prices = map[string]rate{
	"claude-haiku-5-5":  {0.10, 0.50, 0.01, 0.125},
	"claude-sonnet-5-5": {2, 10, 0.20, 2.50},
	"claude-opus-5-5":   {4, 20, 0.20, 5},
}

// haikuLong is Haiku 5.5's second rate card, for prompts over haikuLongPrompt tokens.
var haikuLong = rate{0.50, 2.50, 0.05, 0.625}

const haikuLongPrompt = 100_000

// Cost estimates what a request cost in US dollars. ok is false for a model with no known price,
// so an unknown model is reported as unpriced rather than as free.
func Cost(model string, t Tokens) (usd float64, ok bool) {
	r, ok := prices[model]
	if !ok {
		return 0, false
	}
	if model == "claude-haiku-5-5" && t.Input+t.CacheRead+t.CacheWrite > haikuLongPrompt {
		r = haikuLong
	}
	const perMillion = 1e-6
	return perMillion * (float64(t.Input)*r.input + float64(t.Output)*r.output +
		float64(t.CacheRead)*r.cacheRead + float64(t.CacheWrite)*r.cacheWrite), true
}
