# lets-go-gaffer

Go + RAG assistant for Fantasy Premier League: pgvector news retrieval, concurrent ingestion, and a constraint-validated squad optimizer.

> **Status: early development.** Data ingestion and a greedy squad optimizer work end to end; the RAG and LLM layers are not built yet. Everything under "Planned" below is not built yet.

## Design principles

- **The LLM never does math or rule checking.** Squad validation and optimization are plain, tested Go. The LLM only narrates results and retrieves news.
- **Stdlib first.** Dependencies are added only when justified.
- **Small interfaces, defined where used.** `FPLClient`, and later `Embedder` and `LLMClient`, so each can be faked in tests.
- **Costs are integers.** Prices are stored in tenths of millions (`65` = £6.5m) as in the FPL API, so budget math never touches floats.

## Planned architecture

```text
FPL API ──► ingest ──► Postgres (stats + pgvector embeddings)
                            │
news feeds ──► embed ───────┘
                            │
                        retrieval ◄── query
                            │
                         router ──► LLM (tool calls) ──► HTTP / SSE
                            │
                        optimizer  (pure Go, no LLM)
```

## Layout

```text
cmd/gaffer/        entry point
deploy/docker/     local Postgres + pgvector via docker compose
migrations/        SQL schema migrations
internal/fpl/      FPL API client and types      (implemented)
internal/db/       Postgres + pgvector storage   (implemented)
internal/ingest/   concurrent worker pool + sync (implemented)
internal/optimizer/ squad rules, greedy baseline, exact optimizer (implemented)
internal/projection/ expected-points model + backtest   (implemented)
internal/lineup/   best XI, captain, bench, close calls (implemented)
internal/transfers/ transfer search with free-transfer and hit costs (implemented)
internal/myteam/   your squad, free transfers, selling prices (implemented)
internal/embed/    Voyage AI client + offline test embedder (implemented)
internal/httpx/    retry, rate limit, typed HTTP errors (implemented)
internal/llm/      Anthropic Messages API client: streaming, tool use (implemented)
internal/service/  read-only questions: my team, lineup, transfers, players, news (implemented)
internal/assistant/ tool-calling assistant with citation and number checks (implemented)
internal/news/     RSS + FPL news, player linking, embed pipeline (implemented)
(vector search lives in internal/db: SearchNews)
internal/router/   LLM tool-calling router       (planned)
internal/server/   HTTP API + SSE                (planned)
```

## Getting started

Requires Go 1.23+.

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --wait
cp .env.example .env   # then fill in API keys; .env is gitignored
go run ./cmd/gaffer sync
```

`sync` applies migrations, then pulls all teams, players, and per-gameweek history from the live FPL API into Postgres. A full run takes about a minute: 8 workers fetch concurrently, paced to 10 requests/second so the API isn't hammered. Ctrl-C cancels cleanly. Configuration is read from `.env` (see `.env.example`); real environment variables take precedence.

```bash
go run ./cmd/gaffer news    # needs VOYAGE_API_KEY in .env
go run ./cmd/gaffer search -player Saka "when will he be back"
go run ./cmd/gaffer team 12345      # your FPL team id, or set GAFFER_TEAM_ID in .env
go run ./cmd/gaffer lineup         # best XI, captain and bench for the next gameweek
go run ./cmd/gaffer transfers      # best moves for your real squad this week
go run ./cmd/gaffer transfers -wildcard
go run ./cmd/gaffer squad
go run ./cmd/gaffer ask "Who should I captain this week?"   # needs ANTHROPIC_API_KEY
go run ./cmd/gaffer chat                                     # a conversation with follow-ups
go run ./cmd/gaffer backtest        # how well does the scoring model predict past gameweeks?
```

`news` pulls BBC Sport and Sky Sports football RSS plus FPL's official per-player injury/availability notes, links stories to players, embeds anything new with Voyage AI, and stores it in pgvector. Stored stories are never embedded twice, and progress is saved in chunks, so an interrupted or rate-limited run resumes where it stopped. `team` pulls a manager's public squad (as of the last gameweek deadline; transfers made since need a login and are not read), checks it against the FPL rules, and flags players with injury or availability news. Find your team id in the address bar of the Points page on the FPL site (`.../entry/<TEAM_ID>/event/<N>`). **Your live squad.** FPL publishes a squad only as of the last deadline; transfers made since are visible only to the logged-in manager (the `my-team` endpoint returns 403 without a session). Two ways to work from the squad you actually have:

- **Live (exact).** Put the `x-api-authorization` value from a logged-in browser in `FPL_AUTH_HEADER` in `.env` (steps in `.env.example`). `team`, `lineup` and `transfers` then read your live squad, with FPL's own selling prices, bank, free transfers, hit already taken, and chips available. The token is a standard 60-minute token, so it must be refreshed about hourly; if it is missing, expired, or rejected, the tool says so and falls back to public data. It is sent only to `fantasy.premierleague.com`, over HTTPS, on a copy of the request (so a redirect cannot forward it), and is never logged or printed. Treat it as a password.
- **Stating your moves.** `-swap Out>In` (repeatable, accepted by `team`, `lineup` and `transfers`) applies transfers you tell it about, for example `go run ./cmd/gaffer lineup -swap "Havertz>Calvert-Lewin"`. The new player takes the old one's place, the result is checked against the squad rules (2/5/5/3 split, at most three per club), and `transfers` also adjusts your bank, uses up free transfers, and counts any hit already taken. Without a session and without `-swap`, every run warns that the squad is as of the last deadline.

Computed selling prices (used without a session) were checked against FPL's own for a real squad: 15 of 15 matched to the tenth.

`lineup` picks the best starting XI from your real 15 for the upcoming gameweek by expected points in that gameweek alone (a lineup is chosen for one match week, unlike a transfer), with captain, vice-captain, bench order, each player's opponent and difficulty, the cost of every other formation, and the changes from the lineup you set at the last deadline. It reports **close calls**: a bench player 0.1 xP behind a starter is a toss-up and is shown as one, and a near tie for the captain's armband (whose points count twice) is called out explicitly. It is exact, proven against enumeration of every legal XI. `transfers` suggests this week's best moves for your real squad under FPL's actual transfer rules: one free transfer per gameweek (unused ones bank, up to five), each extra transfer costs 4 points, and the money available is what your players *sell* for, not their market price. Free transfers are replayed from your season history (and cross-checked against every hit FPL applied); selling prices are computed from FPL's rule using what you actually paid. `-wildcard` plans a Wildcard or Free Hit with no limit and no hit. `-free N` overrides the free-transfer count, `-prices` prints each player's computed selling price so you can check it on the FPL site. Players are ranked by **expected points** (see below), shown as `xP` next to form so the reasoning is visible. `-wildcard` solves the rebuild exactly (dynamic programming over budget with branch-and-bound on the club limit), where a kept player costs his selling price. `search` ranks stored stories by semantic similarity (`-player NAME`, `-days N`, `-k N`), so retrieval quality can be judged directly before any LLM is involved. FPL's player notes are treated as state: only a player's current note is returned, so a recovered player is never reported as injured. Accounts without a payment method are limited to 3 requests/minute (`VOYAGE_RPM`), so a first run takes a few minutes.

`squad` builds the best legal squad from the stored players: it respects the £100.0m budget, the 3-per-club limit, and the position split, then picks the highest-scoring starting XI and a captain. Squads are validated in plain Go; the LLM never checks rules.

## Development

```bash
go vet ./... && go test -race ./...
```

Database tests need Postgres with pgvector and skip without `GAFFER_TEST_DSN`. The container listens on host port 5433 (a native Postgres often owns 5432):

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --wait
docker compose -f deploy/docker/docker-compose.yml exec postgres createdb -U gaffer gaffer_test
export GAFFER_TEST_DSN='postgres://gaffer:gaffer@localhost:5433/gaffer_test?sslmode=disable'
go test -race ./...
```

The tests truncate every table and refuse to run against a database whose name does not end in `_test`.

## Asking questions (`ask`, `chat`)

`gaffer ask "…"` answers in plain English by letting Claude (Haiku by default; set `ANTHROPIC_MODEL=claude-sonnet-5-5` for richer prose) call five read-only tools: `get_my_team`, `best_lineup`, `suggest_transfers`, `find_players` (structured filters: position, club, price, availability) and `search_news`. `-v` shows which tools it called and the tokens used. `chat` keeps the conversation, so follow-ups see earlier answers and keep their citations.

The model narrates; Go computes and checks. It is told never to calculate, and is not given the chance to:

- **Numbers arrive precomputed and formatted** (`"£6.1m"`, `"xp": 5.1`). After each answer, every number in it must appear in a tool result or in your question; anything else (a sum or a gap the model worked out itself) is flagged under "Checks".
- **Citations are forced by construction.** News reaches the model as reference numbers with no URL; Go builds the Sources list from its own record, and a cited number that does not exist, or news used without citing it, is flagged. FPL's own injury notes are shown as "official FPL player note", not as a link.
- **News text is untrusted.** It is delivered with a warning, and every tool is read-only, so a poisoned headline cannot change anything: the assistant cannot make transfers or alter your FPL team.
- **Freshness is stated by the tools.** Every squad answer carries `squad_as_of` ("now (live from FPL)" or "the GW8 deadline") and notes about an expired session or stored history that lags a finished gameweek.
- **The upcoming gameweek comes from FPL's schedule**, not from how far the stored data goes: once a deadline passes, that gameweek is locked and you are deciding for the next.
- A failed turn is not committed to the conversation, so one API error cannot poison the next question.

Known limits: the number check can be satisfied by coincidence (a small number that happens to appear elsewhere in a tool result), and it cannot judge whether a sentence is true. Your question and the tool results, including your squad, are sent to Anthropic's API. News search needs `VOYAGE_API_KEY`; without it that one tool reports itself unavailable.

## How players are scored

Raw form is a poor forecast, so players are ranked by a projection model (`internal/projection`) built from the gameweek-by-gameweek record: points per 90 minutes with long memory and shrinkage toward the position norm, minutes with short memory (who is starting, who was just dropped), FPL's fixture difficulty for each match (blank gameweeks are worth nothing, doubles both matches), and the chance of playing.

`gaffer backtest` measures it honestly: for each past gameweek it predicts using only earlier gameweeks and compares with what happened. On this season's data (GW3 to GW7):

| Scorer | Rank correlation | Mean abs. error | Avg. points of its top 20 |
|---|---|---|---|
| Last-three-games form | 0.368 | 2.23 | 3.89 |
| Season average | 0.413 | 2.08 | 4.10 |
| Projection model | **0.534** | **1.88** | **4.76** |

It beats form in every one of the five gameweeks. Settings were tuned and then re-checked walk-forward (tune on earlier gameweeks, score the next unseen one): about 0.52 out of sample against 0.38 for form. Five gameweeks is a small sample, so the backtest should be rerun as the season grows.

## Roadmap

Done:
- **Core data:** hardened FPL client, Postgres + pgvector, worker-pool ingestion, news ingestion and semantic search, your real squad, free-transfer and selling-price modelling.
- **Decisions:** backtested expected-points model, exact squad optimizer, transfer and Wildcard suggestions, and the **lineup advisor** (best XI, captain, bench order, close calls).
- **Ask:** a Claude assistant over read-only tools, with Go-enforced citation and number checks.

Planned, in order:
1. **Eval harness for the assistant:** a fixed set of questions with expected tool choices and checks (cites real stories, no ungrounded numbers, refuses to act), run against the real model, so changes to prompts and tools are measured.
2. **HTTP API and web UI:**
   - A JSON API in Go on the standard library (`net/http`), bound to localhost, with graceful shutdown, request logging, and short caches for expensive endpoints. The CLI commands and the API share one service layer instead of duplicating logic.
   - A **Next.js (React, TypeScript) UI** in `web/` that calls it through a Next.js rewrite, so there is no CORS and no keys in the browser. One frontend only: Next.js is React, so a second Angular app would mean building every screen twice.
   - Screens: dashboard (gameweek, data freshness); **my team** on a pitch with availability flags; **best XI for the next gameweek**; **transfers and Wildcard** with the hit and selling prices shown; squad builder; players table with xP; news search with filters; model page with the backtest.
   - Docker image, structured logging, metrics.
3. **Deploy:** Helm, Terraform on Azure AKS (student credit), CI, dashboards.
4. **Stretch:** Temporal to run the daily ingest as durable workflows.

## License

See [LICENSE](LICENSE).
