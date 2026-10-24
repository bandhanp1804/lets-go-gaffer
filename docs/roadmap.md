# Roadmap

## Done

- **Core data:** a hardened FPL client, Postgres + pgvector, worker-pool ingestion, news ingestion and semantic search, your real squad, free-transfer and selling-price modelling.
- **Decisions:** a backtested expected-points model, an exact squad optimiser, transfer and Wildcard suggestions, the lineup advisor, a what-if for your own transfers, captain choice, and a chip planner.
- **Ask:** a Claude assistant over read-only tools with Go-enforced number, citation and club checks, and an evaluation harness that measures it.
- **HTTP API:** JSON endpoints and a streaming `/api/ask`, with browser-attack protections for a localhost server, and any team chosen from the browser with its token kept there.
- **Web UI:** a soccer-themed landing page and the screens listed in [web-ui.md](web-ui.md).

## Planned, in order

1. **UI polish** from hands-on testing.
2. **Temporal** to run the daily ingest (sync, news, embed) as a durable workflow. Approved, and paused until the UI work settles. It needs the `go.temporal.io/sdk` dependency and a Temporal dev server in docker compose; the ingest stages are plain functions so they can be wrapped.
3. **Containers and metrics:** Docker images for the API and the web app, and Prometheus metrics from the API.
4. **Deploy:** Helm, Terraform on Azure AKS (student credit), CI and dashboards.

## Ideas not scheduled

- Showing the look-ahead for one player on a page, not only in Ask.
- A retrieval-quality benchmark with labelled questions, to measure the news search separately from the assistant.
