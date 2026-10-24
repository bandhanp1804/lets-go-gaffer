# Background workflows (Temporal)

The app works without this. Add it when you want the news pulled, players watched, and the gameweek planned for you in the background, with every run visible and retried on failure.

## Start it

```bash
docker compose -f deploy/docker/docker-compose.yml up -d   # Postgres and the Temporal dev server
echo 'TEMPORAL_ADDRESS=localhost:7233' >> .env             # so `gaffer serve` uses it
go run ./cmd/gaffer worker                                  # third terminal; leave it running
```

Restart `gaffer serve` after setting `TEMPORAL_ADDRESS`. The Temporal web UI is at <http://localhost:8233>: every run, its inputs, each activity and retry, and the schedules.

Without `TEMPORAL_ADDRESS`, `gaffer serve` pulls the news on its own in-process timer (`GAFFER_NEWS_REFRESH`), the watchlist is kept but makes no alerts, and nothing else on this page runs.

## What runs

| Workflow | When | What it does |
|---|---|---|
| `NewsRefresh` | every `GAFFER_NEWS_REFRESH` (30m), and the **Refresh news** button | Refreshes players, reads each feed as its own activity (a failing feed is retried, then recorded as failed while the rest carry on), embeds new stories one chunk per activity (a rate-limited Voyage key waits and retries), then signals the watch of each watched player who is in a new story or whose FPL status changed. |
| `WatchPlayer` | one per watched player, from starring him until unstarring | Turns news and status signals into alerts. An unchanged status is never announced twice. |
| `BriefingCheck` → `GameweekBriefing` | hourly check; one briefing per gameweek | Sleeps on a durable timer until 24 hours before the deadline, pulls the news, then sends the plan for `GAFFER_TEAM_ID`: captain and vice, best XI and its expected points, doubtful players, the best transfer, and any chip worth playing this week. Every number is computed in Go. Sent once per gameweek even across restarts; not sent once the deadline has passed. |
| `FullSync` → `Backtest` | checked every 6 hours; runs when a gameweek has finished since the stored history | Refreshes every player's history, one activity per player, 8 at a time, each retried on its own; more than 20 failures stop it. Then backtests the scoring model and raises an alert if its rank correlation falls by more than 0.05. |
| `NightlyEval` | 03:00 local (`GAFFER_EVAL_SCHEDULE`) | Runs the assistant's eval cases once each, keeps the result for the Model page, and raises an alert below 90%. Reports itself skipped without `ANTHROPIC_API_KEY`. |
| `Relink`, `Reembed` | by hand | Backfills over every stored story a page at a time: work out the player links again, or embed again after changing `VOYAGE_MODEL`. The cursor is workflow state, so a worker that dies mid-way resumes at the next page. |

Start any of them by hand, for example after changing settings:

```bash
go run ./cmd/gaffer workflow news          # waits and prints the result
go run ./cmd/gaffer workflow sync -force   # sync even if the history is up to date
go run ./cmd/gaffer workflow -detach eval  # start and return
```

## Alerts

Alerts are stored in Postgres and shown on the **Alerts** page, with an unread count in the nav. Set `NTFY_TOPIC` to also get them on your phone through [ntfy](https://ntfy.sh): install the app and subscribe to the same topic. Anyone who knows the topic can read it, so pick something unguessable and keep it in `.env`. `NTFY_SERVER` points at a self-hosted ntfy instead of ntfy.sh.

## Design notes

- **Workflows decide, activities act.** Workflow code (`internal/workflows`) only calls activities, timers and signals and reads time with `workflow.Now`, so Temporal can replay it. Everything that touches the network or the database is an activity, built on the same functions the CLI uses (`ingest.Bootstrap`, `news.Merge`/`DropStored`/`EmbedAndSave`, `projection.RunBacktest`, the eval runner).
- **Vectors stay out of history.** Stories are embedded and saved inside one activity, so the 1024-number vectors never enter workflow history.
- **Schedules are ensured, not added.** `gaffer worker` creates each schedule or brings it up to date on start, so new settings never leave two of the same. A run that is still going when the next is due is skipped.
- **The inbox is the record.** An alert is stored before it is pushed; a failed push leaves it stored, and a failed store pushes nothing.
- **Testing.** Workflows are tested with Temporal's test environment, which skips timers: the briefing's day-long sleep, signals, continue-as-new and the sync's failure limit are all checked in milliseconds.
