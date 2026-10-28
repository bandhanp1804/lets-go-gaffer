package telemetry

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// quiet paths are probed every few seconds; a span for each would bury the real requests.
var quiet = map[string]bool{"/api/healthz": true, "/api/readyz": true}

// Server traces every request except the probes. It reads the caller's traceparent, so a trace
// that Traefik started continues here.
func Server(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "http.request",
		otelhttp.WithFilter(func(r *http.Request) bool { return !quiet[r.URL.Path] }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return spanName(r) }),
	)
}

// spanName is "GET /api/news" once the mux has matched a pattern, and the method alone before
// (or if nothing matched): never the raw path, whose query strings and ids would make every
// request its own endpoint.
func spanName(r *http.Request) string {
	if route := routeOf(r); route != "" {
		return r.Method + " " + route
	}
	return r.Method
}

// routeOf is the path part of the pattern the mux matched ("GET /api/news" → "/api/news").
func routeOf(r *http.Request) string {
	if _, path, ok := strings.Cut(r.Pattern, " "); ok {
		return path
	}
	return r.Pattern
}

// Routes names the request's span after the ServeMux pattern it matched, so /api/players?id=1
// and ?id=2 are one endpoint in Datadog, not two. It must wrap the mux directly: the mux records
// the pattern on the request it is handed, and the middleware in between pass on copies, so
// Server never sees it.
func Routes(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		route := routeOf(r)
		if route == "" {
			return
		}
		span := trace.SpanFromContext(r.Context())
		span.SetName(spanName(r))
		span.SetAttributes(attribute.String("http.route", route), attribute.String("resource.name", spanName(r)))
	})
}

// Transport traces each outbound request as a child of the caller's span, named by method and
// host ("POST api.anthropic.com"). Put it underneath the retry transport so every attempt shows.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + r.URL.Host }),
	)
}
