package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// The app's own metrics. Every series is a Datadog custom metric, so each has a few low-cardinality
// tags at most: never a user, a player, or a question. They are made from the global providers,
// which forward to the real ones once Setup has run and do nothing if it never does.
var (
	meter  = otel.Meter("github.com/bandhanpatel/lets-go-gaffer")
	tracer = otel.Tracer("github.com/bandhanpatel/lets-go-gaffer")

	llmTokens  = counter(meter.Int64Counter("gaffer.llm.tokens", metric.WithDescription("Claude tokens by model and kind (input, output, cache_read, cache_write)")))
	llmCost    = counter(meter.Float64Counter("gaffer.llm.cost", metric.WithUnit("USD"), metric.WithDescription("Estimated Claude spend, worked out from the token counts")))
	limitHits  = counter(meter.Int64Counter("gaffer.limit.hits", metric.WithDescription("Requests refused by a daily limit, by feature")))
	newsItems  = counter(meter.Int64Counter("gaffer.news.stories", metric.WithDescription("Stories seen by a news refresh: new or duplicate")))
	feedFailed = counter(meter.Int64Counter("gaffer.news.feed_failures", metric.WithDescription("Feeds that failed during a news refresh")))
	alertsSent = counter(meter.Int64Counter("gaffer.alerts.sent", metric.WithDescription("Alerts stored, by inbox: owners or visitor")))
)

// counter keeps the program running if an instrument cannot be made: the SDK then hands back a
// working no-op, and losing a metric is better than failing to start.
func counter[T any](c T, _ error) T { return c }

// LLMCall is an open span around one Claude request.
type LLMCall struct {
	span  trace.Span
	model string
}

// StartLLM opens a span for a Claude request; the HTTP span of each attempt nests inside it.
func StartLLM(ctx context.Context, model string) (context.Context, LLMCall) {
	ctx, span := tracer.Start(ctx, "claude.messages", trace.WithAttributes(
		attribute.String("gen_ai.system", "anthropic"),
		attribute.String("gen_ai.request.model", model),
	))
	return ctx, LLMCall{span: span, model: model}
}

// End records the tokens the request used, on the span and in the token and spend metrics, and
// closes the span. Tokens are counted even when the request failed partway: they were billed.
func (c LLMCall) End(ctx context.Context, t Tokens, err error) {
	c.span.SetAttributes(
		attribute.Int("gen_ai.usage.input_tokens", t.Input),
		attribute.Int("gen_ai.usage.output_tokens", t.Output),
		attribute.Int("gen_ai.usage.cache_read_tokens", t.CacheRead),
		attribute.Int("gen_ai.usage.cache_write_tokens", t.CacheWrite),
	)
	if err != nil {
		c.span.RecordError(err)
		c.span.SetStatus(codes.Error, err.Error())
	}
	c.span.End()

	model := attribute.String("model", c.model)
	for kind, n := range map[string]int{"input": t.Input, "output": t.Output, "cache_read": t.CacheRead, "cache_write": t.CacheWrite} {
		if n > 0 {
			llmTokens.Add(ctx, int64(n), metric.WithAttributes(model, attribute.String("kind", kind)))
		}
	}
	if usd, ok := Cost(c.model, t); ok && usd > 0 {
		llmCost.Add(ctx, usd, metric.WithAttributes(model))
	}
}

// LimitHit counts a request refused by the daily limit on feature.
func LimitHit(ctx context.Context, feature string) {
	limitHits.Add(ctx, 1, metric.WithAttributes(attribute.String("feature", feature)))
}

// NewsRefreshed counts what one news refresh saw.
func NewsRefreshed(ctx context.Context, fresh, duplicates int, failedFeeds []string) {
	newsItems.Add(ctx, int64(fresh), metric.WithAttributes(attribute.String("result", "new")))
	newsItems.Add(ctx, int64(duplicates), metric.WithAttributes(attribute.String("result", "duplicate")))
	for _, f := range failedFeeds {
		feedFailed.Add(ctx, 1, metric.WithAttributes(attribute.String("feed", f)))
	}
}

// AlertSent counts an alert stored in the owners' inbox or a visitor's.
func AlertSent(ctx context.Context, owners bool) {
	inbox := "visitor"
	if owners {
		inbox = "owners"
	}
	alertsSent.Add(ctx, 1, metric.WithAttributes(attribute.String("inbox", inbox)))
}

// PoolStats is a database pool's state at one moment.
type PoolStats struct {
	Acquired, Idle, Max int32
	EmptyAcquires       int64 // how many times a caller had to wait for a connection, ever
}

// ObservePool reports a database pool's connections every export. Call the returned function
// when the pool closes.
func ObservePool(stats func() PoolStats) (stop func(), err error) {
	conns, err := meter.Int64ObservableGauge("gaffer.db.pool.connections", metric.WithDescription("Database connections by state: acquired, idle, max"))
	if err != nil {
		return nil, err
	}
	waits, err := meter.Int64ObservableCounter("gaffer.db.pool.waits", metric.WithDescription("Times a caller waited for a free database connection"))
	if err != nil {
		return nil, err
	}
	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := stats()
		o.ObserveInt64(conns, int64(s.Acquired), metric.WithAttributes(attribute.String("state", "acquired")))
		o.ObserveInt64(conns, int64(s.Idle), metric.WithAttributes(attribute.String("state", "idle")))
		o.ObserveInt64(conns, int64(s.Max), metric.WithAttributes(attribute.String("state", "max")))
		o.ObserveInt64(waits, s.EmptyAcquires)
		return nil
	}, conns, waits)
	if err != nil {
		return nil, err
	}
	return func() { _ = reg.Unregister() }, nil
}
