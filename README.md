# Let's Go Gaffer

**Live at [letsgogaffer.tech](https://letsgogaffer.tech)**: sign in with GitHub, Google or Microsoft.

A Fantasy Premier League assistant. It shows your squad and how it is scoring, picks your best XI and captain, weighs transfers (including ones you try yourself), says when to play your chips, gathers team news from several outlets, and answers questions in plain English with its numbers checked by code.

**The rule that shapes it: the LLM never does maths or checks rules.** Expected points, lineups, transfers, chips and FPL's squad rules are all worked out in Go. Claude only narrates the results and retrieves news, and what it says is checked against the tools' numbers before you see it.

## What is in here

| Area | What it shows |
|---|---|
| **Go backend** | A stdlib-first HTTP API, an expected-points model backtested on gameweeks it never saw, a lineup and transfer optimizer, and table-driven tests throughout. |
| **Retrieval (RAG)** | News from BBC, Sky, the Guardian and more, embedded with Voyage into Postgres + pgvector, and searched by meaning and narrowed to the player asked about. |
| **The assistant** | Claude with tool use and streaming answers, with an eval suite run nightly. |
| **Workflows** | Temporal: scheduled news pulls, per-player watch workflows that alert whoever starred a player, a gameweek briefing, and resumable backfills. |
| **Web** | Next.js, with types generated from the Go structs. |
| **Infrastructure** | Terraform on Azure: AKS with autoscaling, managed Postgres, and DNS. Helm, Traefik, cert-manager, and sign-in through Dex and oauth2-proxy, with per-user daily limits. |
| **Delivery** | GitHub Actions with OIDC (no stored cloud keys), a Terraform plan on each pull request, a preview environment per pull request (torn down on merge), and manual deploys from `main`. |
| **Observability** | OpenTelemetry traces and metrics from Go to Datadog, logs tied to traces, and dashboards, monitors and an SLO as code. |

The [architecture](docs/architecture.md) and [deployment](docs/deploy.md) docs explain the choices.

## Start the app

**You need:** Go 1.26+, Docker, and Node 20.9+ (Node 23.6+ to run the web tests).

**1. Start the database** (Postgres with pgvector on port 5433, and the Temporal dev server for the optional background workflows):

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --wait
```

**2. Configure** (`.env` is gitignored; it needs no keys for the first run):

```bash
cp .env.example .env
```

| Add this to `.env` | To get |
|---|---|
| `GAFFER_TEAM_ID` | a default team (optional: the web app lets you enter any team) |
| `ANTHROPIC_API_KEY` | the Ask page and `gaffer ask` |
| `VOYAGE_API_KEY` | news search |

**3. Load the data** (about a minute; add `news` if you set a Voyage key):

```bash
go run ./cmd/gaffer sync
go run ./cmd/gaffer news
```

**4. Run the API** (leave it running; it listens on `127.0.0.1:8080`):

```bash
go run ./cmd/gaffer serve
```

**5. Run the web app** in a second terminal, then open <http://127.0.0.1:3001>:

```bash
cd web && npm install && npm run dev
```

**Optional: background workflows.** Set `TEMPORAL_ADDRESS=localhost:7233` in `.env`, restart step 4, and run `go run ./cmd/gaffer worker` in a third terminal for scheduled news, watched-player alerts, a gameweek briefing and a nightly model check ([temporal.md](docs/temporal.md)).

Click **Load my team**, enter your team id (the number in the address of your FPL Points page, `.../entry/<ID>/event/N`) and you are in. Switch on **Live squad** if you want transfers made since the deadline; the dialog explains how to get the login token it needs.

### Prefer the command line?

```bash
go run ./cmd/gaffer lineup                        # best XI, captain and bench
go run ./cmd/gaffer transfers                     # best moves this week
go run ./cmd/gaffer ask "Who should I captain?"   # needs ANTHROPIC_API_KEY
```

See the [command reference](docs/cli.md) for the rest.

### If something looks wrong

- **"The API did not respond"**: `gaffer serve` is not running (step 4).
- **The page crashes after you pull new code**: restart `gaffer serve`; an older API lacks fields the new page expects.
- **"FPL rejected the session"**: the login token lasts about an hour; paste a fresh one. More in [troubleshooting](docs/development.md#troubleshooting).

## Documentation

| | |
|---|---|
| [Architecture](docs/architecture.md) | principles, how the pieces fit, the code layout |
| [Configuration](docs/configuration.md) | every `.env` setting, your live squad and its token, ports |
| [Command-line reference](docs/cli.md) | every `gaffer` command |
| [HTTP API](docs/api.md) | endpoints, streaming, and why the local server refuses some requests |
| [Web UI](docs/web-ui.md) | the screens, choosing a team, design notes |
| [The assistant](docs/assistant.md) | its tools, the checks on what it says, and how it is evaluated |
| [How the numbers are made](docs/model.md) | the expected-points model, its backtest, lineup, transfers, chips, points |
| [Sign-in](docs/sign-in.md) | GitHub, Google or Microsoft through Dex, the daily limits |
| [Deploying to Azure](docs/deploy.md) | Terraform, AKS, Helm, costs, stop and start |
| [Background workflows](docs/temporal.md) | Temporal: schedules, alerts, the briefing, backfills |
| [Observability](docs/observability.md) | traces, metrics, logs, dashboards and alerts in Datadog |
| [Development](docs/development.md) | tests, generated types, opt-in live tests, troubleshooting |
| [Roadmap](docs/roadmap.md) | what is done and what is next |
| [Security](SECURITY.md) | how to report a vulnerability, and how secrets are kept out of the repository |

## License

See [LICENSE](LICENSE).
