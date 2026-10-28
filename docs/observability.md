# Observability: traces, metrics, logs and alerts in Datadog

The app sends **OpenTelemetry** traces and metrics to the **Datadog Agent** in the cluster. The agent also collects every pod's logs and the Kubernetes state, and ships all of it to Datadog (site `us5.datadoghq.com`, the GitHub Student Pack's Pro plan). Dashboards, monitors and an SLO live in this repository and are published on every deploy.

```text
Browser ─▶ Traefik ── span ─────────────────────────────────────────────┐
             └─▶ gaffer-api   spans named by route; JSON logs with trace ids
                   ├─▶ FPL, feeds, Voyage, Claude, ntfy   one span per HTTP attempt
                   └─▶ Temporal ─▶ gaffer-worker   workflow and activity spans, same trace
gaffer-api, gaffer-worker ── OTLP/HTTP :4318 (node-local Service) ──────┤
Datadog Agent (DaemonSet) + Cluster Agent (namespace datadog) ◀──────────┘
  ├─ pod logs (JSON parsed), kubelet and Kubernetes state
  ├─ Temporal server and cert-manager, through their Datadog integrations
  └─▶ Datadog us5: APM, Logs, Metrics, dashboards, monitors ──▶ ntfy webhook ──▶ your phone
```

## Why it is built this way

- **The code speaks OpenTelemetry, not Datadog.** [`internal/telemetry`](../internal/telemetry) uses the vendor-neutral OpenTelemetry SDK. The agent accepts OTLP, so moving to Grafana, Honeycomb or another backend means changing the agent, not the Go code.
- **Custom metrics are kept few.** Datadog counts each OTLP metric series as a custom metric, and the Pro plan allows about 100 per host.
  - **Taken from the traces:** request rate, errors and latency. APM derives them (`trace.http.server.request.hits`, `.errors` and the latency distribution), and they are free.
  - **Dropped in code:** the HTTP library's own metrics.
  - **Our own `gaffer.*` metrics:** a few, each with a few low-cardinality tags. None is tagged with a user, a player or a question.
- **Spans have fixed operation names**, set with the `operation.name` attribute: `http.server.request`, `http.client.request`, `claude.messages`, and `temporal.run_workflow` / `temporal.run_activity` / and so on. Datadog keeps one set of trace metrics per operation, and the dashboards and monitors query those names. The thing within the operation (the route, the host, the workflow type) is the **resource**.
- **Logs link to traces.** In the cluster the app logs JSON (`GAFFER_LOG_FORMAT=json`). A line written inside a span carries `trace_id` and `span_id` in OpenTelemetry's form, and `dd.trace_id` and `dd.span_id` in Datadog's form (the low 64 bits in decimal). From a trace, **Logs** shows its lines; from a log line, **View trace** opens it.
- **One trace runs end to end.**
  - Traefik starts the trace and passes `traceparent` to the API.
  - The Temporal interceptor carries it into the workflow and its activities.
  - The outbound calls hang under whichever step made them.
- **Unified service tags.** Each pod is labelled `tags.datadoghq.com/service|env|version`, and the OTLP resource carries the same values, so logs, traces and Kubernetes metrics line up:
  - **service:** `gaffer-api`, `gaffer-worker` or `gaffer-web`
  - **env:** `prod`, or `pr-N` for a preview
  - **version:** the image tag
- **Nothing breaks without it.** With no `OTEL_EXPORTER_OTLP_ENDPOINT` the Go code sends nothing, so local runs and tests are unchanged. With no `DD_API_KEY` the deploy skips the agent.

## What is collected

| Signal | Where it comes from | In Datadog |
|---|---|---|
| API requests | `otelhttp` server spans, named `GET /api/news` (the route pattern, never the raw path; probes skipped) | APM → service `gaffer-api` |
| Outbound calls | `otelhttp` client spans per attempt, under the retry transport; URL cut to the host | APM, resource `POST api.anthropic.com` |
| Claude requests | `claude.messages` span with token counts (`gen_ai.usage.*`) | APM, resource = model |
| Workflows | Temporal's OpenTelemetry interceptor | APM → service `gaffer-worker` |
| `gaffer.llm.tokens{model,kind}` | each Claude response's usage | Metrics |
| `gaffer.llm.cost{model}` | tokens × list price, worked out in Go ([cost.go](../internal/telemetry/cost.go)) | Metrics (estimate; Anthropic's console has the bill) |
| `gaffer.limit.hits{feature}` | the daily limiter | Metrics |
| `gaffer.news.stories{result}`, `gaffer.news.feed_failures{feed}` | each news refresh | Metrics |
| `gaffer.alerts.sent{inbox}` | the notifier (owners or visitor) | Metrics |
| `gaffer.db.pool.connections{state}`, `gaffer.db.pool.waits` | the pgx pool | Metrics |
| Logs | every container's output except `kube-system` and `datadog` | Logs |
| Kubernetes | kubelet, kube-state-metrics core, orchestrator explorer | Infrastructure, Containers |
| Temporal server, cert-manager | Datadog integrations through pod annotations | their built-in dashboards |

Traefik's own metrics are not scraped (they would be custom metrics); its spans carry the edge view.

## Dashboards, monitors and the SLO

These are defined in [deploy/observability](../deploy/observability) and published by [deploy/scripts/observability.sh](../deploy/scripts/observability.sh), which the Deploy workflow runs after the app. The script finds each item by title or name and updates it, so **edit the JSON in the repository**: changes made in Datadog's UI are overwritten on the next deploy.

- **Dashboards**
  - **Service overview:** requests, errors and p95 by endpoint, outbound latency by host, memory, CPU and restarts, node memory, the database pool, and recent errors.
  - **Claude and spend:** spend, tokens, cache hit share, Claude and Ask latency, and limit hits.
  - **Workflows and news:** workflow runs and failures, activity duration and retries, stories, feed failures, alerts, and the worker log.
- **Monitors:** these go to the `gaffer-ntfy` webhook, so to the phone, and to Datadog.

  | Monitor | Fires when |
  |---|---|
  | API errors | more than 5% of requests return 5xx over 10 minutes |
  | API latency | p95 above 3 s over 15 minutes (Ask excluded) |
  | Ask latency | p95 above 45 s |
  | Pod restarts | a pod restarts |
  | Workflows | any workflow fails |
  | News | no new story in 24 hours, or no refresh reporting at all |
  | Claude spend | above $2 in a day |
  | Node memory | above 90% |
  | TLS certificate | not ready for an hour |
  | API availability SLO | burning its budget fast |

- **SLO:** 99% of API requests succeed over 7 days.

## One-time setup (yours, in the browser)

1. **Activate the offer.** In the [GitHub Student Developer Pack](https://education.github.com/pack), open the Datadog offer and sign up. Your account lives at `https://us5.datadoghq.com`. Then open **Plan & Usage** and check that APM and Log Management are included.
2. **API key.** **Organization Settings → API Keys → New Key**, named `gaffer-aks`. **→ save as `DD_API_KEY`**
3. **Application key.** This is used only by the publish script. **Organization Settings → Application Keys → New Key**, named `gaffer-deploy`. If it asks for scopes, give it `dashboards_read`, `dashboards_write`, `monitors_read`, `monitors_write`, `slos_read`, `slos_write`, `integrations_read`, `integrations_write` and `create_webhooks`. **→ save as `DD_APP_KEY`**
4. **Save them in `deploy/azure.env`:**

   ```sh
   DD_API_KEY=<step 2>
   DD_APP_KEY=<step 3>
   ```

5. Rerun `deploy/scripts/ci-setup.sh` to copy them into GitHub, then run **Deploy**.
6. **Integrations.** In Datadog, open **Integrations** and install **Temporal**, **cert-manager**, **Kubernetes** and **Webhooks**. This adds their dashboards; the agent already collects their data.

## Running it locally

Without an endpoint nothing is sent. To watch traces on your own computer, run any OTLP collector with a UI, for example [Jaeger](https://www.jaegertracing.io/), which needs no account:

```sh
docker run --rm -p 16686:16686 -p 4318:4318 jaegertracing/jaeger:2.1.0
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go run ./cmd/gaffer serve
```

Open http://localhost:16686 and look for the service `gaffer-api`. Jaeger shows traces only. The OTLP metric exporter will log errors, because Jaeger does not accept metrics; that is harmless.

## Adding a metric

1. Add the instrument and a small recording function to [metrics.go](../internal/telemetry/metrics.go). Tags must stay low-cardinality: a few known values, never ids or free text.
2. Call it where the work happens.
3. Add a table-driven test that reads it back with the test `ManualReader`.
4. Add it to a dashboard JSON, and to this page's table.

## Cost

| | Cost |
|---|---|
| Datadog | $0 under the student Pro offer (up to 10 hosts, 2 years, while you are a student); the cluster has 1–2 nodes |
| Node memory | about 350 MB requested (agent, trace agent, cluster agent), so the one node stays under about 85% requested |
| After the offer ends | Pro is billed per host; switching backend means replacing the agent, not the code |
