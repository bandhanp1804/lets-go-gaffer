package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/eval"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
	"github.com/bandhanpatel/lets-go-gaffer/internal/workflows"
)

// temporalAddress is where the workflow server listens. For `gaffer serve` an empty value means
// "no Temporal": news refreshes in-process and watches make no alerts. The worker and the
// workflow command need one, so they default to the local dev server.
func temporalAddress(required bool) string {
	addr := strings.TrimSpace(os.Getenv("TEMPORAL_ADDRESS"))
	if addr == "" && required {
		return "localhost:7233"
	}
	return addr
}

// temporalUI is the address of the workflow server's web UI, for links.
func temporalUI() string {
	if v := strings.TrimSpace(os.Getenv("TEMPORAL_UI")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8233"
}

func temporalNamespace() string {
	if v := strings.TrimSpace(os.Getenv("TEMPORAL_NAMESPACE")); v != "" {
		return v
	}
	return "default"
}

// dialTemporal connects to the workflow server, failing fast if it is not there.
func dialTemporal(ctx context.Context, addr string) (client.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := client.DialContext(ctx, client.Options{
		HostPort: addr, Namespace: temporalNamespace(),
		Logger: tlog.NewStructuredLogger(slog.Default()),
	})
	if err != nil {
		return nil, fmt.Errorf("connect to Temporal at %s (is it running? docker compose -f deploy/docker/docker-compose.yml up -d): %w", addr, err)
	}
	return c, nil
}

// workflowLink is the web UI page of a workflow run.
func workflowLink(id string) string {
	return fmt.Sprintf("%s/namespaces/%s/workflows/%s", temporalUI(), temporalNamespace(), id)
}

func feedNames() []string {
	names := make([]string, len(feeds))
	for i, f := range feeds {
		names[i] = f.name
	}
	return names
}

func feedSources() map[string]news.Source {
	c := politeClient(500 * time.Millisecond)
	out := make(map[string]news.Source, len(feeds))
	for _, f := range feeds {
		out[f.name] = news.NewRSSSource(f.name, f.url, c)
	}
	return out
}

// notifier stores every alert and, when NTFY_TOPIC is set, pushes it to the phone too.
func notifier(store notify.AlertStore) notify.Notifier {
	m := notify.Multi{Inbox: notify.Inbox{Store: store}}
	if topic := strings.TrimSpace(os.Getenv("NTFY_TOPIC")); topic != "" {
		server := strings.TrimSpace(os.Getenv("NTFY_SERVER"))
		if server == "" {
			server = "https://ntfy.sh"
		}
		m.Pushes = append(m.Pushes, notify.Ntfy{Server: server, Topic: topic, Client: &http.Client{Timeout: 15 * time.Second}})
	}
	return m
}

// evaluator runs the eval cases once each for the nightly workflow.
type evaluator struct{ model *llm.Anthropic }

func (e evaluator) Evaluate(ctx context.Context) (workflows.EvalOutcome, error) {
	fix := eval.NewFixture()
	a := assistant.New(e.model, fix, assistant.Config{Model: llm.ModelFromEnv(), Clubs: fix.Clubs()})
	results := eval.Runner{NewSession: a.NewSession, Runs: 1, Workers: 2}.Run(ctx, eval.Cases())
	if err := ctx.Err(); err != nil {
		return workflows.EvalOutcome{}, err
	}
	out := workflows.EvalOutcome{Model: llm.ModelFromEnv(), PassRate: eval.PassRate(results)}
	for _, c := range results {
		out.Runs += len(c.Runs)
		if c.Passes() < len(c.Runs) {
			out.Failing = append(out.Failing, c.Case)
		}
		for _, r := range c.Runs {
			out.Tokens += r.Tokens.InputTokens + r.Tokens.OutputTokens
		}
	}
	return out, nil
}

// localRefresher adapts the in-process refresher to the API, for a server without Temporal.
type localRefresher struct{ r *news.Refresher }

func (l localRefresher) Start(context.Context) (bool, error) { return l.r.Start(), nil }
func (l localRefresher) Status(context.Context) (news.RefreshStatus, error) {
	return l.r.Status(), nil
}
