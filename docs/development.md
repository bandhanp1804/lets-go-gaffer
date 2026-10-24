# Development

## Tests

```bash
go vet ./... && go test -race ./...        # Go
cd web && npm run typecheck && npm test    # web
```

Database tests need Postgres with pgvector and skip without `GAFFER_TEST_DSN`. They truncate every table and refuse to run against a database whose name does not end in `_test`:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --wait
docker compose -f deploy/docker/docker-compose.yml exec postgres createdb -U gaffer gaffer_test
export GAFFER_TEST_DSN='postgres://gaffer:gaffer@localhost:5433/gaffer_test?sslmode=disable'
go test -race ./...
```

`npm test` uses Node's built-in test runner on TypeScript files directly, which needs Node 23.6 or later.

### Opt-in tests against real services

These skip unless you set the variable, so `go test ./...` never spends tokens or reads your account.

| Variable | Test | What it checks |
|---|---|---|
| `ANTHROPIC_API_KEY` | `internal/llm` live test | the Anthropic client against the real API (two tiny requests) |
| `GAFFER_LIVE_FPL_TEAM=<team id>` | `go test ./internal/service -run TestLive_Points -v` | the points calculation against FPL's own figure for every finished gameweek of that one team |
| `ANTHROPIC_API_KEY` | `gaffer eval` | the assistant, end to end ([assistant.md](assistant.md#evaluating-it)) |

## Generated TypeScript types

`web/lib/types.gen.ts` is written from the Go response structs (`internal/tsgen` reads their `json` tags). After changing a response struct run:

```bash
go run ./cmd/gentypes
```

A Go test fails when the committed file is stale, and the TypeScript compiler then shows every place the change reaches. A new response type must also be listed in `cmd/gentypes/main.go`.

## Conventions

The project rules are in `CLAUDE.md`: stdlib first (ask before adding a dependency); small interfaces defined where they are used; errors wrapped with context; every network or database call takes a `context.Context` first; every goroutine has a shutdown path; table-driven tests; and nothing is done until `go vet ./... && go test -race ./...` passes. Squads are validated in plain Go, never by the model.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| The UI says "The API did not respond" | `gaffer serve` is not running, or is on another port. Start it, or set `GAFFER_API` for the web app. |
| The UI crashes with `Cannot read properties of undefined` after you pull changes | The API is an older build than the UI, so it lacks fields the page expects. Restart `gaffer serve`. |
| "FPL rejected the session" or "expired" | The login token lasts about an hour. Paste a fresh one ([configuration.md](configuration.md#your-live-squad)); a running server picks it up without a restart. |
| Port 5432 is taken | Postgres here is on **5433** on purpose (a native Postgres often owns 5432). |
| `next build` fails with `Duplicate identifier 'LayoutProps'` | Stray `… 2.ts` copies in `web/.next/types`. Delete them (the tsconfig now ignores them). Build into a separate folder with `GAFFER_DIST=.next-test` so a build never collides with a running `next dev`. |
| A first `news` run is slow | Voyage accounts without a payment method are limited to 3 requests a minute (`VOYAGE_RPM`). Progress is saved, so it resumes. |
