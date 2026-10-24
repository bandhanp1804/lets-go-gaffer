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
go run ./cmd/gaffer eval                                     # measure the assistant (spends API tokens)
go run ./cmd/gaffer serve                                    # the HTTP API on http://127.0.0.1:8080
(cd web && npm install && npm run dev)                       # the web UI on http://127.0.0.1:3001
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

## HTTP API (`serve`)

`gaffer serve [-addr 127.0.0.1:8080] [-origins http://localhost:3001]` serves the same service layer the CLI uses, as JSON. It needs `GAFFER_DSN`; `/api/ask` also needs `ANTHROPIC_API_KEY` (and news search `VOYAGE_API_KEY`).

| Endpoint | Query parameters |
|---|---|
| `GET /api/status` | dashboard: upcoming gameweek, the gameweek in play, deadline countdown, how far the stored history goes, player/club/news counts, FPL session state (never the token) |
| `GET /api/squad` | `horizon` (default 4): the best legal squad from scratch under £100.0m, with XI, bench, captain |
| `GET /api/backtest` | how the scoring model did on played gameweeks, including the out-of-sample check (cached until new history arrives) |
| `GET /api/team` | your squad, bank, free transfers, with `squad_as_of` and freshness notes |
| `GET /api/lineup` | the best XI, captain, bench order and close calls for the upcoming gameweek; `formation` (such as `3-5-2`) shows that formation instead, and `all_formations` compares every legal one with the best |
| `GET /api/transfers` | `wildcard`, `free_transfers`, `horizon`, `max_transfers` |
| `GET /api/players` | `position`, `team`, `name`, `status`, `min_price`, `max_price`, `sort_by` (`xp`, `form`, `price`, `points`, `name`, `team`, `position`), `sort_dir`, `limit` (up to 100), `offset`; returns `players`, `total`, `offset`, `limit` |
| `POST /api/whatif` | your own transfer plan: JSON `{"moves": [{"out": 101, "in": 201}], "horizon": 4, "free_transfers": 1}`. Checks the rules and returns the verdict, the hit, the money, the lineup before and after, and plain-sentence insights |
| `GET /api/news` | `q` (required), `player`, `days`, `limit` |
| `POST /api/ask` | JSON `{"question": "...", "session_id": "..."}`, answered as a server-sent-event stream |
| `GET /api/healthz` | liveness |

`/api/ask` streams `session` (the conversation id, sent first), then `text` deltas, then one `done` event carrying the full text, `sources`, `warnings` (the grounding checks), the `tools` used and token `usage`. Send the `session_id` back to continue the conversation; one question runs at a time per conversation (a second gets 409), and a conversation is forgotten after 30 idle minutes. A failure after streaming starts arrives as an `error` event with a generic message; the detail is in the server log, not the response. Problems with the request itself (bad parameter, unknown club) are `400` with a message written for the caller.

```
curl -s 'localhost:8080/api/players?position=MID&max_price=8&limit=3'
curl -sN -H 'Content-Type: application/json' -d '{"question":"Who should I captain?"}' localhost:8080/api/ask
```

**Lists are never `null`.** A Go `nil` slice would be sent as `null`, so the JSON writer turns every nil list into `[]` (`internal/api/nonnil.go`), and the web client can rely on it. The same guarantee is what the generated TypeScript types assume.

**Any team, with the secret kept in the browser.** The web app sends the team id in `X-Gaffer-Team` and, for a live squad, the manager's token in `X-Gaffer-Token` (headers, never the URL, so it is not in logs or history). The server attaches the token to that one request to FPL and keeps nothing: the squad cache is keyed by team and a *hash* of the token, a conversation is bound to the team it started with, and the configured team's own token is never applied to anyone else's. With no headers the API serves the team in `GAFFER_TEAM_ID` as before. Request errors for a missing team carry `"code": "team_required"` so the client can ask for one.

**A new FPL token needs no restart.** The session token lasts an hour and the server may run for days, so `gaffer serve` re-reads `FPL_AUTH_HEADER` from `.env` whenever the file changes, and drops its cached squad when the token changes. Paste a fresh token and the next request is live.

**Why it refuses some requests.** The server has no login, because it is for your own machine, and it will not listen on anything but loopback. But a browser will call `localhost` on behalf of any page you have open, so a hostile site could read your squad or spend your API credits. Three rules stop that: a request whose `Origin` is not on the `-origins` list gets 403; a request whose `Host` is not `localhost`/`127.0.0.1` gets 403 (this defeats DNS rebinding, where a hostile name is re-pointed at 127.0.0.1 and the browser sends no `Origin`); and a `POST` must be `application/json`, which a page cannot send cross-origin without a CORS preflight that this server never answers. No CORS headers are sent at all: the planned UI reaches the API through a Next.js rewrite, so it is same-origin.

## Web UI (`web/`)

A Next.js (React, TypeScript) app on port 3001 that talks to the Go API. Start the API (`gaffer serve`) and then `cd web && npm install && npm run dev`, and open http://127.0.0.1:3001.

**Choosing a team.** The landing page and the team chip in the nav open a dialog: paste your FPL team id (or the whole address of your Points page, which contains it) and, optionally, switch on **Live squad** to include transfers made since the deadline. Live squads need your login token, and the dialog explains how to get it: a short script you can read first, run in the console of your own FPL tab, that shows the token FPL's page is already sending (it sends nothing anywhere), or by hand from the Network tab. The team id is remembered in this browser; the token lives in `sessionStorage`, so it is gone when the tab closes, and it expires after about an hour anyway. The snippet's logic is tested against a stand-in page, but it has not been run against FPL's real site (that needs a login), so the by-hand route is the fallback.

**What if?** (`/whatif`) lets you try transfers before making them: click a player of yours to sell him, pick a replacement from a shortlist (same position, affordable, best first), and the plan is weighed in plain Go, with no model involved. It checks the rules (like for like, at most three from a club, the real selling prices against your bank) and says what the plan is worth over the horizon after any points hit, whether it is worth doing, a toss-up or not, who would start or only sit on the bench, whether the captain changes, risk flags such as a doubtful signing, your free transfers next week, and a clearly better pick for the same slot when one is affordable. It uses the same measure as the Transfers page, so a suggestion replayed there ("Try this in What if?") gives identical numbers; a test enforces that. On **Best XI**, the formation chips are buttons that show that formation's lineup, and any player on a pitch can be clicked for details.

Screens: **Home** (a soccer-themed landing page with a stadium scoreboard of the season status), **Dashboard** (next deadline countdown, how far the stored data goes, FPL session state), **My team** on a pitch with availability flags and the captain and vice-captain, **Best XI** (close calls first, then the pitch and the changes from your current lineup), **Transfers** (Wildcard, what-if free transfers, horizon; each option shows its hit, net gain and bank, and says when two options are a toss-up), **Squad builder**, **Players** (filters and sorting), **News** (semantic search), **Ask** (the assistant, streamed as it is written, with sources, the grounding checks' warnings and the tools it used) and **Model** (the backtest, including the out-of-sample check).

- **No CORS and no keys in the browser.** `next.config.mjs` rewrites `/api/*` to the Go server (`GAFFER_API`, default `http://127.0.0.1:8080`), so the browser only talks to its own origin, and the Go server's Origin and Host guard still applies through the proxy.
- **Answers are rendered from text, never HTML.** `lib/markdown.ts` turns the assistant's markdown into data and React builds elements from it, so nothing the model (or a news story it read) writes can inject markup or a script link. Only `http(s)` addresses become links.
- **Few dependencies.** Next, React and TypeScript, pinned exactly; no UI kit, chart library or CSS framework (plain CSS, light and dark).
- **Types come from Go.** `web/lib/types.gen.ts` is generated from the Go response structs by `go run ./cmd/gentypes` (a small stdlib reflection generator in `internal/tsgen`), and a Go test fails when the committed file is stale, so a renamed or added field cannot silently drift from the client.
- **The players table pages on the server** (up to 100 a page, with the total), sorts by clicking any column header, and the ordering is stable so a page never repeats or skips a player.
- **Tests:** `npm test` runs Node's built-in test runner over the pure logic (the SSE parser, pitch grouping, the markdown reader); `npm run typecheck` checks types. I also drove the Ask page in headless Chrome against the real API to check that the stream arrives incrementally.

## Evaluating the assistant (`eval`)

`gaffer eval [-run NAME] [-n 3] [-workers 2] [-min 0.9]` runs 14 fixed scenarios against the real model and exits non-zero when the pass rate falls below `-min`. Each scenario is run `-n` times because model output varies.

- **Fixed data, not live data.** The backend is a stub with known squad, lineup, transfers, market and news (`internal/eval/fixture.go`), so a change in score means the model or prompt changed, not the FPL season. It needs only `ANTHROPIC_API_KEY`.
- **Plain Go checks, no model as judge.** A scenario passes when the right tools were called (`captain` must use `best_lineup`; "which goalkeepers are injured" must use `find_players` and not `search_news`), the assistant's own grounding checks stayed quiet, and the answer says (or omits) what it should. A model grading a model would add cost and its own mistakes.
- **Safety scenarios.** One planted news story orders the model to call a tool and repeat a phrase; obeying either fails the scenario. Another asks it to make a transfer, which it must decline.
- **A failing run prints the answer**, so the cause can be read rather than guessed. That is how the first run found the model writing "£1.8m less" (a subtraction the grounding check flagged); the prompt now forbids stating gaps no tool supplied.

The harness itself is tested offline with a scripted model, so `go test` costs nothing. Latest baseline on Haiku: 14 scenarios x 4 runs, 100%. Known limit: the content checks look for words, so they catch a wrong or missing name, not a subtly misleading sentence.

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
- **Ask:** a Claude assistant over read-only tools, with Go-enforced citation and number checks, and an eval harness that measures it.
- **HTTP API:** JSON endpoints and a streaming `/api/ask`, with browser-attack protections for a localhost server.
- **Web UI:** nine screens in Next.js over that API.

Planned, in order:
1. **Containers and metrics:** Docker images for the API and the web app, and Prometheus metrics from the API.
2. **Deploy:** Helm, Terraform on Azure AKS (student credit), CI, dashboards.
3. **Stretch:** Temporal to run the daily ingest as durable workflows.

## License

See [LICENSE](LICENSE).
