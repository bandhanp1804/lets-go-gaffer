package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// The global providers forward the package's instruments only to the first real provider set, so
// the tests share one of each, installed here.
var (
	spans  = tracetest.NewSpanRecorder()
	reader = sdkmetric.NewManualReader()
)

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithView(dropLibraryMetrics...)))
	os.Exit(m.Run())
}

func TestCost(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		tokens Tokens
		want   float64
		ok     bool
	}{
		{"haiku short prompt", "claude-haiku-5-5", Tokens{Input: 10_000, Output: 1_000}, 0.001 + 0.0005, true},
		{"haiku cache read and write", "claude-haiku-5-5", Tokens{CacheRead: 50_000, CacheWrite: 8_000}, 0.0005 + 0.001, true},
		{"haiku long prompt uses the second card", "claude-haiku-5-5", Tokens{Input: 90_000, CacheRead: 20_000, Output: 1_000}, 0.045 + 0.001 + 0.0025, true},
		{"haiku exactly at the limit stays on the first card", "claude-haiku-5-5", Tokens{Input: 100_000}, 0.01, true},
		{"sonnet", "claude-sonnet-5-5", Tokens{Input: 1_000_000, Output: 100_000, CacheRead: 1_000_000}, 2 + 1 + 0.2, true},
		{"opus cache write", "claude-opus-5-5", Tokens{CacheWrite: 200_000}, 1, true},
		{"unknown model is unpriced, not free", "claude-unknown", Tokens{Input: 1_000}, 0, false},
		{"nothing used", "claude-opus-5-5", Tokens{}, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Cost(tc.model, tc.tokens)
			if ok != tc.ok || math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Cost = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestLoggerAddsTraceIDsOnlyInsideASpan(t *testing.T) {
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("1112131415161718")
	inSpan := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}))

	tests := []struct {
		name string
		ctx  context.Context
		want map[string]string // absent from the map: must not be logged
	}{
		{"inside a span", inSpan, map[string]string{
			"trace_id":    "0102030405060708090a0b0c0d0e0f10",
			"span_id":     "1112131415161718",
			"dd.trace_id": "651345242494996240",  // 0x090a0b0c0d0e0f10
			"dd.span_id":  "1230066625199609624", // 0x1112131415161718
		}},
		{"no span", context.Background(), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			NewLogger(&buf, "json").With("svc", "test").InfoContext(tc.ctx, "hello")
			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("not JSON: %q", buf.String())
			}
			for _, k := range []string{"trace_id", "span_id", "dd.trace_id", "dd.span_id"} {
				got, has := line[k]
				want, should := tc.want[k]
				if has != should || (should && got != want) {
					t.Errorf("%s = %v (present %v), want %q (present %v)", k, got, has, want, should)
				}
			}
		})
	}
}

func TestNewLoggerFormat(t *testing.T) {
	tests := []struct {
		format   string
		wantJSON bool
	}{{"json", true}, {" JSON ", true}, {"", false}, {"text", false}}
	for _, tc := range tests {
		var buf bytes.Buffer
		NewLogger(&buf, tc.format).Info("hi")
		if got := json.Valid(bytes.TrimSpace(buf.Bytes())); got != tc.wantJSON {
			t.Errorf("format %q: JSON %v, want %v (%q)", tc.format, got, tc.wantJSON, buf.String())
		}
	}
}

func TestServerNamesSpansByRouteAndSkipsProbes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/players", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /api/healthz", func(http.ResponseWriter, *http.Request) {})
	// A middleware between them passes a copy of the request on, as the API's do.
	copying := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r.WithContext(r.Context())) })
	}
	h := Server(copying(Routes(mux)))

	tests := []struct {
		path      string
		wantSpan  bool
		wantName  string
		wantRoute string
	}{
		{"/api/players?id=1", true, "GET /api/players", "/api/players"},
		{"/api/players?id=2", true, "GET /api/players", "/api/players"},
		{"/api/nothing", true, "GET", ""}, // no pattern matched: the method alone, never the raw path
		{"/api/healthz", false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			before := len(spans.Ended())
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))
			ended := spans.Ended()[before:]
			if !tc.wantSpan {
				if len(ended) != 0 {
					t.Fatalf("got %d spans for a probe, want none", len(ended))
				}
				return
			}
			if len(ended) != 1 {
				t.Fatalf("got %d spans, want 1", len(ended))
			}
			s := ended[0]
			if s.Name() != tc.wantName {
				t.Errorf("span name %q, want %q", s.Name(), tc.wantName)
			}
			if got := attr(s.Attributes(), "http.route"); got != tc.wantRoute {
				t.Errorf("http.route %q, want %q", got, tc.wantRoute)
			}
		})
	}
}

func TestTransportNamesClientSpansByHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	ctx, parent := otel.Tracer("test").Start(context.Background(), "parent")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/my-team/1234/?token=x", nil)
	before := len(spans.Ended())
	resp, err := (&http.Client{Transport: Transport(nil)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parent.End()

	ended := spans.Ended()[before:]
	if len(ended) != 2 {
		t.Fatalf("got %d spans, want the request's and the parent's", len(ended))
	}
	if want := "POST " + req.URL.Host; ended[0].Name() != want {
		t.Errorf("client span %q, want %q", ended[0].Name(), want)
	}
	if got, want := attr(ended[0].Attributes(), "url.full"), "http://"+req.URL.Host; got != want {
		t.Errorf("url.full %q, want only %q: the path and query must not be recorded", got, want)
	}
	if got, want := attr(ended[0].Attributes(), "resource.name"), "POST "+req.URL.Host; got != want {
		t.Errorf("resource %q, want %q", got, want)
	}
	if ended[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("the request span is not a child of the caller's span")
	}
}

func TestLLMCallRecordsTokensSpendAndErrors(t *testing.T) {
	ctx, call := StartLLM(context.Background(), "claude-sonnet-5-5")
	call.End(ctx, Tokens{Input: 1_000_000, Output: 100_000, CacheRead: 0, CacheWrite: 0}, errors.New("stream cut off"))

	s := spans.Ended()[len(spans.Ended())-1]
	if s.Name() != "claude.messages" || s.Status().Description != "stream cut off" {
		t.Errorf("span %q status %q", s.Name(), s.Status().Description)
	}
	if got := attr(s.Attributes(), "gen_ai.usage.output_tokens"); got != "100000" {
		t.Errorf("output tokens on the span: %q", got)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]int64{}
	var cost float64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				if m.Name == "gaffer.llm.tokens" {
					for _, p := range d.DataPoints {
						if attr(p.Attributes.ToSlice(), "model") == "claude-sonnet-5-5" {
							tokens[attr(p.Attributes.ToSlice(), "kind")] += p.Value
						}
					}
				}
			case metricdata.Sum[float64]:
				if m.Name == "gaffer.llm.cost" {
					for _, p := range d.DataPoints {
						if attr(p.Attributes.ToSlice(), "model") == "claude-sonnet-5-5" {
							cost += p.Value
						}
					}
				}
			}
		}
	}
	if tokens["input"] != 1_000_000 || tokens["output"] != 100_000 || tokens["cache_read"] != 0 {
		t.Errorf("tokens %v", tokens)
	}
	if math.Abs(cost-3) > 1e-9 {
		t.Errorf("cost %v, want 3", cost)
	}
}

func TestSetupWithoutAnEndpointSendsNothing(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Error("Enabled with no endpoint")
	}
}

func TestDeltaTemporality(t *testing.T) {
	tests := []struct {
		kind sdkmetric.InstrumentKind
		want metricdata.Temporality
	}{
		{sdkmetric.InstrumentKindCounter, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindHistogram, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindObservableCounter, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindObservableGauge, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindUpDownCounter, metricdata.CumulativeTemporality},
		{sdkmetric.InstrumentKindObservableUpDownCounter, metricdata.CumulativeTemporality},
	}
	for _, tc := range tests {
		if got := deltaTemporality(tc.kind); got != tc.want {
			t.Errorf("%v: %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func attr(kvs []attribute.KeyValue, key string) string {
	for _, kv := range kvs {
		if string(kv.Key) == key {
			return kv.Value.Emit()
		}
	}
	return ""
}

func TestTemporalSpanNamesOperationAndResource(t *testing.T) {
	tests := []struct {
		name, wantOp, wantResource string
	}{
		{"RunWorkflow:NewsRefresh", "temporal.run_workflow", "NewsRefresh"},
		{"StartActivity:FetchNews", "temporal.start_activity", "FetchNews"},
		{"HandleSignal", "", ""}, // not in kind:type form: left as it is
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := len(spans.Ended())
			TemporalSpan(context.Background(), otel.Tracer("test"), tc.name).End()
			s := spans.Ended()[before]
			if got := attr(s.Attributes(), "operation.name"); got != tc.wantOp {
				t.Errorf("operation %q, want %q", got, tc.wantOp)
			}
			if got := attr(s.Attributes(), "resource.name"); got != tc.wantResource {
				t.Errorf("resource %q, want %q", got, tc.wantResource)
			}
			if s.Name() != tc.name {
				t.Errorf("span renamed to %q", s.Name())
			}
		})
	}
}
