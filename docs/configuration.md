# Configuration

Settings are read from `.env` in the project root (copy `.env.example`, which is documented line by line). Real environment variables take precedence over the file. `.env` is gitignored: never commit real keys.

| Variable | Needed for | Notes |
|---|---|---|
| `GAFFER_DSN` | everything | Postgres connection string. The default in `.env.example` matches the docker compose file (host port **5433**, because a native Postgres often owns 5432). |
| `GAFFER_MIGRATIONS` | `sync` | Where the SQL migrations are; defaults to `migrations`. |
| `GAFFER_TEAM_ID` | optional | The team shown when the web app has not been told another one, and the one `gaffer team` and `lineup` use. The web app can pick any team itself. Find it in the address of your Points page on the FPL site: `.../entry/<TEAM_ID>/event/<N>`. |
| `VOYAGE_API_KEY` | `news`, `search`, news in Ask and the UI | Voyage AI embeddings. Without it everything else works and the news tool reports itself unavailable. |
| `VOYAGE_RPM` | optional | Requests per minute. Accounts without a payment method on file are limited to 3 (the default); raise it after adding one. A first `news` run takes a few minutes at 3. |
| `GAFFER_NEWS_REFRESH` | optional | How often the news is pulled by itself (by `gaffer serve`, or by the worker's schedule when Temporal is on): a duration such as `30m` (the default; at least `5m`) or `off`. The Refresh button on the News page works either way. Needs `VOYAGE_API_KEY`. |
| `VOYAGE_MODEL` | optional | Defaults to `voyage-4` (1024 dimensions, which the schema fixes). |
| `ANTHROPIC_API_KEY` | `ask`, `chat`, `eval`, the Ask page | Without it `gaffer serve` still runs and `/api/ask` is off. |
| `ANTHROPIC_MODEL` | optional | Defaults to `claude-haiku-5-5`: questions are short and Go does the computing. Use `claude-sonnet-5-5` for richer prose at a higher price. |
| `FPL_AUTH_HEADER` | optional | Your login token, for a **live squad** from the command line or as the server's default. See below. |
| `GAFFER_OWNERS` | the deployed site | Comma-separated emails never held to the daily limits ([sign-in.md](sign-in.md#limits)). The local user on your own computer is never limited. |
| `GAFFER_LIMIT_ASK`, `GAFFER_LIMIT_SEARCH`, `GAFFER_LIMIT_REFRESH` | optional | Each signed-in visitor's daily allowance of Ask questions (15), news searches (40) and news refreshes (3). `0` turns one off. |
| `GAFFER_DATA_KEY` | the Setup page | Base64 32-byte key (`openssl rand -base64 32`) that encrypts each user's saved team and token. Unset, settings are off and the browser remembers the team. Changing it makes saved settings unreadable, and users save them again. In the cluster `deploy.sh` makes it once and keeps it. |
| `TEMPORAL_ADDRESS` | background workflows | Where the Temporal server listens, e.g. `localhost:7233`. Set, `gaffer serve` hands news pulls and watches to the workflows; unset, it refreshes news in-process. `gaffer worker` defaults to `localhost:7233`. See [temporal.md](temporal.md). |
| `TEMPORAL_NAMESPACE`, `TEMPORAL_UI` | optional | Default `default` and `http://localhost:8233`. |
| `GAFFER_EVAL_SCHEDULE` | optional | Cron for the nightly eval, in the machine's time zone; default `0 3 * * *`, or `off`. |
| `GAFFER_LOG_FORMAT` | optional | `json` writes JSON log lines (the cluster sets it, for Datadog); anything else, text. Either way a line logged inside a trace carries its ids. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | optional | Where `serve` and `worker` send OpenTelemetry traces and metrics, e.g. `http://localhost:4318`. Unset: nothing is sent. The other standard `OTEL_*` variables apply too ([observability.md](observability.md)). |
| `NTFY_TOPIC` | optional | Also push alerts to your phone through ntfy. Treat it like a password. |
| `NTFY_SERVER` | optional | Default `https://ntfy.sh`. |
| `GAFFER_TEST_DSN` | tests | A database whose name ends in `_test`: the tests truncate it. |

## Your live squad

FPL publishes a squad only as of the last deadline; transfers made since are visible only to the logged-in manager. Two ways to work from the squad you really have:

- **The web app** asks for the token in its team dialog and keeps it in the browser tab only ([web-ui.md](web-ui.md#choosing-a-team)). Nothing needs to go in `.env`.
- **The command line, or the server's own default team,** reads `FPL_AUTH_HEADER` from `.env`. It is the `x-api-authorization` value from a logged-in browser (it starts with `Bearer `): log in at fantasy.premierleague.com, open the Transfers page, then DevTools > Network > filter `my-team` > reload > click the request > Request Headers > copy the value. The cookie is not needed.

The token is a standard 60-minute token and a login for your account, so treat it like a password. It is sent only to `fantasy.premierleague.com`, over HTTPS, on a copy of the request (so a redirect cannot forward it), and is never logged or printed. If it is missing, expired or rejected, the tools say so and fall back to public data. A running `gaffer serve` re-reads `.env` whenever it changes and drops its cached squad when the token does, so pasting a fresh token needs no restart.

Without any token, `-swap "Out>In"` (repeatable; see [cli.md](cli.md)) applies transfers you tell it about to the public squad.

## Ports

| Port | What |
|---|---|
| 5433 | Postgres + pgvector (docker compose) |
| 8080 | the Go API (`gaffer serve`) |
| 3001 | the web UI (`npm run dev`) |

The API listens on loopback only and refuses any other address; the web app reaches it through a rewrite (`GAFFER_API`, default `http://127.0.0.1:8080`). To run on other ports, pass `-addr` and `-origins` to `gaffer serve` and set `GAFFER_API` for the web app.
