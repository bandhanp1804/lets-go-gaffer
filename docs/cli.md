# Command-line reference

All commands read `.env` (see [configuration.md](configuration.md)). Run them as `go run ./cmd/gaffer <command>`, or build once with `go build -o gaffer ./cmd/gaffer`.

| Command | What it does |
|---|---|
| `sync` | Applies migrations, then pulls all teams, players and per-gameweek history from FPL into Postgres. About a minute: 8 workers fetch concurrently, paced to 10 requests a second. Ctrl-C cancels cleanly. |
| `news` | Pulls BBC Sport, Sky Sports, TalkSport and Guardian football RSS plus FPL's official per-player injury notes, links stories to players, embeds anything new with Voyage AI and stores it in pgvector. Stored stories are never embedded twice and progress is saved in chunks, so an interrupted or rate-limited run resumes where it stopped. Needs `VOYAGE_API_KEY`. |
| `relink` | Works out again which players each stored story mentions, without fetching or embedding anything. Stories are linked once, when stored, so run this after the player matching improves (it was run when accents began to be ignored, so "Joao Pedro" links to João Pedro). FPL's own notes are left as they are. |
| `search [-player NAME] [-days N] [-k N] QUERY` | Ranks stored stories by meaning. Useful for judging retrieval quality before any model is involved. |
| `team [ID]` | Your squad, checked against the FPL rules, with injury and availability news flagged. |
| `lineup [ID]` | The best XI for the upcoming gameweek: captain, vice-captain, bench order, each player's opponent and difficulty, the cost of every other formation, and the changes from your set lineup. |
| `transfers [-wildcard] [-free N] [-horizon N] [-max N] [-n N] [-prices] [-swap "Out>In"]` | This week's best moves for your real squad under FPL's transfer rules. `-horizon` is how many gameweeks a gain is counted over (default 4), `-max` the most players to change (default 2), `-n` how many suggestions to show (default 5). |
| `squad` | The best legal squad from scratch under £100.0m. |
| `backtest` | How well the scoring model predicted past gameweeks ([model.md](model.md)). |
| `ask [-v] "QUESTION"` | One answer from the assistant. `-v` shows the tools it called and the tokens used. |
| `chat [-v]` | A conversation: follow-ups see earlier answers and keep their citations. |
| `eval [-run NAME] [-n 3] [-workers 2] [-min 0.9]` | Runs the assistant's evaluation scenarios ([assistant.md](assistant.md#evaluating-it)). Spends API tokens. |
| `serve [-addr 127.0.0.1:8080] [-origins ...]` | The HTTP API ([api.md](api.md)). |
| `worker` | Runs the background workflows on Temporal and keeps their schedules up to date ([temporal.md](temporal.md)). |
| `workflow [-detach] NAME` | Starts one workflow by hand: `news`, `sync [-force]`, `backtest`, `eval`, `relink`, `reembed`, `briefing`. Waits and prints the result unless `-detach`. |

## Details worth knowing

**Stating your moves.** `-swap Out>In` (repeatable; accepted by `team`, `lineup` and `transfers`) applies transfers you made since the last deadline when there is no live session, for example `lineup -swap "Havertz>Calvert-Lewin"`. The new player takes the old one's place, the result is checked against the squad rules (2/5/5/3 split, at most three per club), and `transfers` also adjusts your bank, uses up free transfers and counts any hit already taken. Without a session and without `-swap`, every run warns that the squad is as of the last deadline.

**Lineup.** It picks by expected points in the upcoming gameweek alone (a lineup is chosen for one match week, unlike a transfer). It is exact, proven against enumeration of every legal XI. It reports **close calls**: a bench player 0.1 expected points behind a starter is a toss-up and is shown as one, and a near tie for the captain's armband (whose points count twice) is called out.

**Transfers.** One free transfer a gameweek (unused ones bank, up to five), each extra costs 4 points, and the money available is what your players *sell* for, not their market price. Free transfers are replayed from your season history and cross-checked against every hit FPL applied; selling prices are computed from FPL's rule using what you actually paid (15 of 15 matched FPL's own on a real squad). `-wildcard` plans a Wildcard or Free Hit with no limit and no hit and solves the rebuild exactly; `-free N` overrides the free-transfer count; `-prices` prints each computed selling price so you can compare it with the FPL site.

**Squad.** Built with the exact optimiser (dynamic programming over budget, with branch-and-bound on the club limit), and validated in plain Go.
