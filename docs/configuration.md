# Configuration

Settings are read from `.env` in the project root (copy `.env.example`, which is documented line by line). Real environment variables take precedence over the file. `.env` is gitignored: never commit real keys.

| Variable | Needed for | Notes |
|---|---|---|
| `GAFFER_DSN` | everything | Postgres connection string. The default in `.env.example` matches the docker compose file (host port **5433**, because a native Postgres often owns 5432). |
| `GAFFER_MIGRATIONS` | `sync` | Where the SQL migrations are; defaults to `migrations`. |
| `GAFFER_TEAM_ID` | optional | The team shown when the web app has not been told another one, and the one `gaffer team` and `lineup` use. The web app can pick any team itself. Find it in the address of your Points page on the FPL site: `.../entry/<TEAM_ID>/event/<N>`. |
| `VOYAGE_API_KEY` | `news`, `search`, news in Ask and the UI | Voyage AI embeddings. Without it everything else works and the news tool reports itself unavailable. |
| `VOYAGE_RPM` | optional | Requests per minute. Accounts without a payment method on file are limited to 3 (the default); raise it after adding one. A first `news` run takes a few minutes at 3. |
| `VOYAGE_MODEL` | optional | Defaults to `voyage-4` (1024 dimensions, which the schema fixes). |
| `ANTHROPIC_API_KEY` | `ask`, `chat`, `eval`, the Ask page | Without it `gaffer serve` still runs and `/api/ask` is off. |
| `ANTHROPIC_MODEL` | optional | Defaults to `claude-haiku-5-5`: questions are short and Go does the computing. Use `claude-sonnet-5-5` for richer prose at a higher price. |
| `FPL_AUTH_HEADER` | optional | Your login token, for a **live squad** from the command line or as the server's default. See below. |
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
