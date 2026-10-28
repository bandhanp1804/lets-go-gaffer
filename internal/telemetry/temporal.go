package telemetry

import (
	"context"
	"strings"
	"unicode"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TemporalSpan starts the spans of Temporal's tracing interceptor (its TracerOptions.SpanStarter).
// The interceptor names them "RunActivity:FetchNews"; this files each under an operation for its
// kind of step (temporal.run_activity) and a resource for its type (FetchNews), so Datadog keeps
// one set of trace metrics per kind, broken down by type, instead of one per name.
func TemporalSpan(ctx context.Context, t trace.Tracer, name string, opts ...trace.SpanStartOption) trace.Span {
	kind, typ, ok := strings.Cut(name, ":")
	if ok {
		opts = append(opts, trace.WithAttributes(operation("temporal."+snake(kind)), attribute.String("resource.name", typ)))
	}
	_, span := t.Start(ctx, name, opts...)
	return span
}

// snake turns "RunWorkflow" into "run_workflow".
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
