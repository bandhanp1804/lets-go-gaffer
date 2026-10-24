# The assistant

`gaffer ask "…"` (or the Ask page, or `chat` for a conversation) answers in plain English by letting Claude call read-only tools. Haiku is the default model; set `ANTHROPIC_MODEL=claude-sonnet-5-5` for richer prose. `-v` shows which tools were called and the tokens used.

## Tools

| Tool | For |
|---|---|
| `get_my_team` | Your squad, bank, free transfers, chips, and the points scored so far in the gameweek being played. |
| `best_lineup` | The best XI, captain, bench and close calls. |
| `suggest_transfers` | Ranked moves with their hit, including Wildcard and "what if I had two free transfers". |
| `evaluate_transfers` | "What if I swap Hughes for Gomez?" The same rule-checked what-if as the web page, with players given by name. |
| `find_players` | Structured filters: position, club, price, availability. |
| `search_news` | News and injury notes by meaning. A player named in the question narrows it to stories about him. |
| `fixture_outlook` | A player's next gameweeks week by week: fixtures with difficulty (doubles and blanks), expected points, what he would score as captain and with Triple Captain, and his best week. |
| `chip_planner` | When to play each chip, with a verdict and a figure per gameweek. |

## The model narrates; Go computes and checks

The model is told never to calculate, and is not given the chance to.

- **Numbers arrive precomputed and formatted** (`"£6.1m"`, `"xp": 5.1`). After each answer, every number in it must appear in a tool result (an error message counts, since Go writes those too) or in your question. Anything else, such as a sum or a gap the model worked out itself, is flagged under "Checks".
- **Every expected-points figure says what it averages over** (`xp_basis`): the next gameweek only for your team and lineup, several gameweeks for player lists and transfers. So two figures that differ for the same player are explained, not "unexplained".
- **Citations are forced by construction.** News reaches the model as reference numbers with no URL. Go builds the Sources list from its own record, and a cited number that does not exist, or news used without citing it, is flagged. FPL's own injury notes are shown as "official FPL player note", not as a link.
- **Names are matched in Go, not by the model.** `evaluate_transfers` and `fixture_outlook` take names as you said them. Go matches them (ignoring case, accents and punctuation), narrows a name two players share by the sold player's position and an optional club, and says plainly when it is still ambiguous, so the model asks instead of guessing.
- **Clubs come from a list, not from memory.** The model is given this season's club codes and names, and an answer that names a club the data and your question never mentioned is flagged, because it once called BHA "Bournemouth".
- **News text is untrusted.** It is delivered with a warning, and every tool is read-only, so a poisoned headline cannot change anything: the assistant cannot make transfers or alter your FPL team.
- **Freshness is stated by the tools.** Every squad answer carries `squad_as_of` ("now (live from FPL)" or "the GW8 deadline") and notes about an expired session or stored history that lags a finished gameweek.
- **The upcoming gameweek comes from FPL's schedule**, not from how far the stored data goes.
- A failed turn is not committed to the conversation, so one API error cannot poison the next question.

**Known limits.** The number check can be satisfied by coincidence (a small number that happens to appear elsewhere in a tool result), and it cannot judge whether a sentence is true. The club check cannot catch a wrong club that happens to appear elsewhere in the data. Your question and the tool results, including your squad, are sent to Anthropic's API. News search needs `VOYAGE_API_KEY`; without it that one tool reports itself unavailable.

## Evaluating it

`gaffer eval [-run NAME] [-n 3] [-workers 2] [-min 0.9]` runs 19 fixed scenarios against the real model and exits non-zero when the pass rate falls below `-min`. Each scenario is run `-n` times because model output varies. It needs only `ANTHROPIC_API_KEY` and spends tokens.

- **Fixed data, not live data.** The backend is a stub with a known squad, lineup, transfers, market, fixtures and news (`internal/eval/fixture.go`), so a change in score means the model or prompt changed, not the FPL season.
- **Plain Go checks, no model as judge.** A scenario passes when the right tools were called ("which goalkeepers are injured" must use `find_players` and not `search_news`), the assistant's own grounding checks stayed quiet, and the answer says (or omits) what it should. A model grading a model would add cost and its own mistakes.
- **Safety scenarios.** One planted news story orders the model to call a tool and repeat a phrase; obeying either fails the scenario. Another asks it to make a transfer, which it must decline. Another asks it to weigh an illegal swap, which it must report as illegal.
- **A failing run prints the answer**, so the cause can be read rather than guessed. That is how the eval found the model subtracting two prices ("£1.8m less"), naming clubs from memory, and counting a squad itself; each led to a data fix or a check.

The harness itself is tested offline with a scripted model, so `go test` costs nothing. Recent runs pass 98–100%; the occasional miss is the model computing a gap itself or citing a reference that does not exist, which the checks catch. Known limit: the content checks look for words, so they catch a wrong or missing name, not a subtly misleading sentence.
