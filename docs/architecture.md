# Architecture

## Principles

- **The LLM never does math or rule checking.** Squad validation, optimisation, scoring and every figure the assistant quotes are plain, tested Go. The model only narrates results and retrieves news, and Go checks what it says (see [the assistant](assistant.md)).
- **Stdlib first.** Dependencies are added only when justified. The Go side uses pgx and pgvector-go (and what they depend on) and nothing else; the web app uses Next, React and TypeScript.
- **Small interfaces, defined where they are used.** `FPL`, `Store`, `Embedder`, `Model`, `Backend` and the API's `Service` are each declared by their consumer, so every one can be faked in tests.
- **Costs are integers.** Prices are in tenths of millions (`65` = £6.5m), as in the FPL API, so budget arithmetic never touches floats.
- **Read-only.** Nothing here changes anything on FPL: the assistant cannot make transfers, and the web app's what-if only weighs a plan.

## How the pieces fit

```text
FPL API ──► ingest ──► Postgres (players, history, news + pgvector embeddings)
news feeds ─► embed ──┘
                          │
              service layer: read-only questions in plain Go
        ┌─────────────┬───┴───────────┬──────────────────────┐
       CLI        HTTP API      assistant (Claude + tools)    │
                     │                                        │
                Next.js web UI                       eval harness
```

Everything that computes lives in a handful of pure packages (`optimizer`, `projection`, `lineup`, `transfers`, `myteam`). The **service layer** (`internal/service`) turns them into typed, already-formatted views (prices as `"£6.1m"`, expected points rounded once), and the CLI, the HTTP API and the assistant all read those same views. That is why the three can never disagree, and why the assistant is given numbers it can quote and is never asked to calculate.

## Layout

| Path | What it holds |
|---|---|
| `cmd/gaffer/` | the command-line entry point and every subcommand |
| `cmd/gentypes/` | writes `web/lib/types.gen.ts` from the Go response structs |
| `internal/fpl/` | FPL API client and types, including the logged-in `my-team` call |
| `internal/httpx/` | retry with jitter, rate limiting, typed HTTP errors |
| `internal/db/` | Postgres + pgvector storage and semantic search |
| `internal/ingest/` | the concurrent worker pool behind `sync` |
| `internal/news/` | RSS and FPL news, linking stories to players, the embedding pipeline |
| `internal/embed/` | Voyage AI client and an offline test embedder |
| `internal/projection/` | the expected-points model and its backtest |
| `internal/optimizer/` | squad rules, the greedy baseline, the exact optimiser |
| `internal/lineup/` | best XI, captain, bench order, close calls |
| `internal/transfers/` | transfer search with free-transfer and hit costs, Wildcard |
| `internal/myteam/` | your squad, free transfers, selling prices, live squad |
| `internal/service/` | the read-only questions: team, points, lineup, transfers, what-if, chips, look-ahead, players, news |
| `internal/llm/` | Anthropic Messages API client: streaming, tool use, thinking blocks |
| `internal/assistant/` | the tool-calling assistant and its checks |
| `internal/eval/` | the assistant's evaluation scenarios and stub backend |
| `internal/api/` | the HTTP API, sessions and the localhost guard |
| `internal/tsgen/` | the Go-to-TypeScript type generator |
| `internal/config/` | `.env` loading, including values re-read while running |
| `migrations/` | SQL schema migrations |
| `deploy/docker/` | Postgres + pgvector via docker compose |
| `web/` | the Next.js UI ([web-ui.md](web-ui.md)) |

## Decisions worth knowing

- **The upcoming gameweek comes from FPL's schedule**, not from how far the stored history goes. Once a deadline passes, that gameweek is locked and you are deciding for the next.
- **FPL's player notes are state, not events.** Only a player's current note is searched, so a recovered player is never reported as injured.
- **A live squad uses the manager's login token, kept in the browser.** The server uses it for one request, sends it only to FPL, and stores and logs nothing ([api.md](api.md#whose-squad)).
- **Every Go response type is mirrored in TypeScript by a generator**, and a test fails when the committed file is stale ([development.md](development.md)).
