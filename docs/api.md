# HTTP API

`gaffer serve [-addr 127.0.0.1:8080] [-origins http://localhost:3001,...]` serves the same service layer the CLI uses, as JSON. It needs `GAFFER_DSN`; `/api/ask` also needs `ANTHROPIC_API_KEY`, and news search `VOYAGE_API_KEY`.

## Endpoints

| Endpoint | Parameters and what it returns |
|---|---|
| `GET /api/status` | The dashboard: upcoming gameweek, the gameweek in play, the deadline countdown, how far the stored history goes, player, club and news counts, and the FPL session state (never the token). |
| `GET /api/team` | Your squad, bank and free transfers with `squad_as_of` and freshness notes, and `points`: how the team is scoring in the gameweek being played, with each player's match-by-match breakdown. |
| `GET /api/lineup` | The best XI, captain, bench order and close calls for the upcoming gameweek. `formation` (such as `3-5-2`) shows that formation; `captain` and `vice` (player ids) choose the armband. Returns `all_formations`, and `captain_options` (each starter's expected points, his double, and what choosing him gives up). |
| `GET /api/transfers` | `wildcard`, `free_transfers`, `horizon`, `max_transfers`: ranked moves with hit, net gain and bank. |
| `POST /api/whatif` | Your own plan: `{"moves": [{"out": 101, "in": 201}], "horizon": 4, "free_transfers": 1}`. Checks the rules and returns the verdict, the hit, the money, the lineup before and after, and plain-sentence insights. It changes nothing; it is a POST only because the plan is a body. |
| `POST /api/whatif/subs` | Substitutions: `{"swaps": [{"out": 110, "in": 115}]}` (a starter off, a bench player on; up to 4). Returns `valid` and `problems` (FPL's rules), then the expected points before and after, the formation, the captain (moved if he is benched), the gap to the best XI, the XI and bench after, and plain-sentence `insights`. Changes nothing. |
| `GET /api/chips` | When to play Triple Captain, Bench Boost, Free Hit and Wildcard. `captain` (a player id) makes the Triple Captain card about that player. |
| `GET /api/squad` | `horizon` (default 4): the best legal squad from scratch under £100.0m. |
| `GET /api/players` | `position`, `team`, `name`, `status`, `min_price`, `max_price`, `sort_by` (`xp`, `form`, `price`, `points`, `name`, `team`, `position`), `sort_dir`, `limit` (up to 100), `offset`. Returns `players`, `total`, `offset`, `limit`. |
| `GET /api/news` | `q` (required), `player`, `days`, `limit`, `detect=off`. Stories ranked by meaning, with a `similarity` for each. A player named in the question (accents and case ignored: "joao pedro" finds João Pedro) narrows the search to stories about him, because otherwise "injured" matches injury notes for anyone; `detect=off` searches everything instead. The result also carries `player` (who it narrowed to, with FPL's status and note), `player_detected`, `notes` (what the search did: nothing is stored about him, the matches are loose, the stored news is old) and `newest_story_age`. |
| `POST /api/news/refresh` | Pulls the feeds again and embeds what is new, in the background: it answers `202` at once with the status, and a refresh already running is not started twice. `GET /api/news/refresh` returns `available` (false without a Voyage key), `running`, `every`, `last_finished`, `last_fetched`, `last_new`, `last_failed_feeds` and `last_error`. With Temporal it starts a `NewsRefresh` workflow and reports the newest run, scheduled or clicked. The News page's Refresh button polls it. |
| `GET /api/usage` | The signed-in user's daily limits: `unlimited` (an owner, or the local user), each limited feature's `used` and `limit`, and `resets_in`. Past a limit, `/api/ask`, `/api/news` and `POST /api/news/refresh` answer `429` with code `limit_reached`. |
| `GET /api/settings`, `POST /api/settings` | The signed-in user's saved team and token status (never the token): `user`, `team_id`, `token` (`saved`, `expired`, `expires_at`, `expires_in`). POST `{"team_id": 1234567, "token": "...", "clear_token": false, "clear_team": false}`; absent fields stay as they are. `503` without `GAFFER_DATA_KEY`. |
| `GET /api/alerts` | The newest alerts, `unread`, and `live` (alerts are being produced: Temporal is configured). |
| `POST /api/alerts/read` | `{"up_to": 12}` marks every alert up to that id read. |
| `GET /api/watchlist`, `POST /api/watchlist` | The watched players; `{"player_id": 4, "watch": true}` stars or unstars one and starts or stops his watch workflow. |
| `GET /api/health/runs` | The nightly evals and the default model's backtests, newest first. |
| `GET /api/backtest` | How the scoring model did on played gameweeks, including the out-of-sample check. Cached until new history arrives. |
| `POST /api/ask` | `{"question": "...", "session_id": "..."}`, answered as a server-sent-event stream (below). |
| `GET /api/healthz` | Liveness. |

```bash
curl -s 'localhost:8080/api/players?position=MID&max_price=8&limit=3'
curl -sN -H 'Content-Type: application/json' -d '{"question":"Who should I captain?"}' localhost:8080/api/ask
```

## Conventions

- **Errors** are JSON `{"error": "...", "code": "..."}`. A bad parameter or an unknown club is `400` with a message written for the caller; `code` (for example `team_required`) is for a client to act on. An internal failure is a generic `500`: the detail is in the server log, never the response.
- **Lists are never `null`.** The JSON writer turns every nil list into `[]` (`internal/api/nonnil.go`), and the generated TypeScript types rely on it.
- **Types are generated.** `web/lib/types.gen.ts` comes from the Go response structs (`go run ./cmd/gentypes`), so the client cannot drift from the server.

## `/api/ask` streaming

Events arrive in this order: `session` (the conversation id, sent first), a stream of `text` deltas, then one `done` event carrying the full text, `sources`, `warnings` (the assistant's own checks), the `tools` used and token `usage`. Send the `session_id` back to continue the conversation. One question runs at a time per conversation (a second gets `409`), a conversation is forgotten after 30 idle minutes, and a conversation belongs to the team it started with. A failure after streaming starts arrives as an `error` event with a generic message.

## Whose squad

The web app sends the team id in `X-Gaffer-Team` and, for a live squad, the manager's token in `X-Gaffer-Token`. They are headers, never the URL, so they are not in logs or browser history. The server uses the token for that one request, sends it only to `fantasy.premierleague.com`, and keeps nothing: the squad cache is keyed by team and a *hash* of the token, a conversation is bound to the team it started with, and the server's own configured token is never applied to anyone else's team. With no headers the API serves the signed-in user's saved team and token (Setup page), else the team in `GAFFER_TEAM_ID`. A saved token is only ever applied to that user's saved team. Behind the login proxy the user is the account it names (`X-Auth-Request-Email`, else `X-Auth-Request-User`); the ingress strips those headers from what the browser sends, so only the proxy can set them. Without a proxy there is one user, `local`.

## Why it refuses some requests

The server has no login, because it is for your own machine, and it will not listen on anything but loopback. But a browser will call `localhost` on behalf of any page you have open, so a hostile site could read your squad or spend your API credits. Three rules stop that:

1. A request whose `Origin` is not on the `-origins` list gets 403.
2. A request whose `Host` is not `localhost` or `127.0.0.1` gets 403. This defeats DNS rebinding, where a hostile name is re-pointed at 127.0.0.1 and the browser sends no `Origin`.
3. A `POST` must be `application/json`, which a page cannot send cross-origin without a CORS preflight that this server never answers.

No CORS headers are sent at all: the web app reaches the API through a Next.js rewrite, so it is same-origin.
