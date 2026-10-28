package telemetry

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// NewLogger writes to w as JSON when format is "json" (what the Datadog Agent parses from pod
// logs) and as text otherwise. Either way, a line logged with a context inside a span carries the
// span's ids, so the line and its trace link to each other.
func NewLogger(w io.Writer, format string) *slog.Logger {
	var h slog.Handler
	if strings.EqualFold(strings.TrimSpace(format), "json") {
		h = slog.NewJSONHandler(w, nil)
	} else {
		h = slog.NewTextHandler(w, nil)
	}
	return slog.New(traceHandler{h})
}

// traceHandler adds the current span's ids to each record twice over: trace_id and span_id in
// OpenTelemetry's hex, and dd.trace_id and dd.span_id in Datadog's form (the low 64 bits as a
// decimal number), which is what it matches a log line to a trace by.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		tid, sid := sc.TraceID(), sc.SpanID()
		r.AddAttrs(
			slog.String("trace_id", tid.String()),
			slog.String("span_id", sid.String()),
			slog.String("dd.trace_id", strconv.FormatUint(binary.BigEndian.Uint64(tid[8:]), 10)),
			slog.String("dd.span_id", strconv.FormatUint(binary.BigEndian.Uint64(sid[:]), 10)),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(as)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
