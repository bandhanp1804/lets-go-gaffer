package eval

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// PassRate is the share of runs, over all cases, that passed every check.
func PassRate(results []CaseResult) float64 {
	pass, total := 0, 0
	for _, c := range results {
		pass += c.Passes()
		total += len(c.Runs)
	}
	if total == 0 {
		return 0
	}
	return float64(pass) / float64(total)
}

// Write prints a per-case summary, the reason for each distinct failure, and the
// overall pass rate and token spend.
func Write(w io.Writer, results []CaseResult) {
	var tokens, cached int
	for _, c := range results {
		mark := "ok  "
		if c.Passes() < len(c.Runs) {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "%s %-28s %d/%d\n", mark, c.Case, c.Passes(), len(c.Runs))

		sample := ""                   // the answer of the first failing run
		reasons := map[string]string{} // check -> first failure text
		count := map[string]int{}
		for _, r := range c.Runs {
			tokens += r.Tokens.InputTokens + r.Tokens.OutputTokens
			cached += r.Tokens.CacheReadTokens
			if sample == "" && !r.Passed() {
				sample = r.Text
			}
			if r.Err != nil {
				count["error"]++
				reasons["error"] = r.Err.Error()
			}
			for name, err := range r.Failures {
				count[name]++
				if reasons[name] == "" {
					reasons[name] = err.Error()
				}
			}
		}
		names := make([]string, 0, len(count))
		for n := range count {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(w, "       x%d %s: %s\n", count[n], n, reasons[n])
		}
		if sample != "" {
			fmt.Fprintf(w, "       first failing answer:\n%s\n", indent(sample))
		}
	}
	fmt.Fprintf(w, "\nPass rate %.1f%%   tokens %d (%d read from cache)\n", PassRate(results)*100, tokens, cached)
}

// indent shows an answer under its case, shortened so one long reply cannot bury the report.
func indent(s string) string {
	const limit = 900
	if len(s) > limit {
		s = s[:limit] + " ..."
	}
	return "         | " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n         | ")
}
