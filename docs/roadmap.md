# Roadmap

## Done

- **Core data:** a hardened FPL client, Postgres + pgvector, worker-pool ingestion, news ingestion and semantic search, your real squad, free-transfer and selling-price modelling.
- **Decisions:** a backtested expected-points model, an exact squad optimiser, transfer and Wildcard suggestions, the lineup advisor, a what-if for your own transfers, captain choice, and a chip planner.
- **Ask:** a Claude assistant over read-only tools with Go-enforced number, citation and club checks, and an evaluation harness that measures it.
- **HTTP API:** JSON endpoints and a streaming `/api/ask`, with browser-attack protections for a localhost server, and any team chosen from the browser with its token kept there.
- **Web UI:** a soccer-themed landing page and the screens listed in [web-ui.md](web-ui.md).
- **Deployed to Azure:** Terraform, AKS with autoscaling, Helm, GitHub login, container images, and GitHub Actions (CI on every PR, a Terraform plan on PRs, deploys from main) ([deploy.md](deploy.md)).
- **Background workflows (Temporal):** scheduled news pulls, watched-player alerts, the gameweek briefing, the full sync and backtest, the nightly eval, and resumable backfills, with alerts in an inbox and on the phone ([temporal.md](temporal.md)).

## Planned, in order

1. **UI polish** from hands-on testing.
2. **Metrics:** Prometheus metrics from the API and worker, and dashboards.

## Ideas not scheduled

- Showing the look-ahead for one player on a page, not only in Ask.
- A retrieval-quality benchmark with labelled questions, to measure the news search separately from the assistant.
