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
internal/optimizer/ squad rules + greedy optimizer   (implemented)
internal/embed/    embedding interface           (planned)
internal/retrieval/ vector search                (planned)
internal/router/   LLM tool-calling router       (planned)
internal/server/   HTTP API + SSE                (planned)
```

## Getting started

Requires Go 1.23+.

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --wait
export GAFFER_DSN='postgres://gaffer:gaffer@localhost:5433/gaffer?sslmode=disable'
go run ./cmd/gaffer sync
```

`sync` applies migrations, then pulls all teams, players, and per-gameweek history from the live FPL API into Postgres. A full run takes about a minute: 8 workers fetch concurrently, paced to 10 requests/second so the API isn't hammered. Ctrl-C cancels cleanly.

```bash
go run ./cmd/gaffer squad
```

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

## Roadmap

1. **Core data and optimizer:** hardened FPL client, Postgres + pgvector, worker-pool ingestion, `Validate(squad)`, greedy and DP optimizers
2. **RAG and router:** embeddings, retrieval with recency and player filters, LLM interface with citations, tool-calling router, eval harness
3. **Service:** HTTP API + SSE, Docker, structured logging, metrics
4. **Deploy:** Helm, Terraform, CI, dashboards

## License

See [LICENSE](LICENSE).
