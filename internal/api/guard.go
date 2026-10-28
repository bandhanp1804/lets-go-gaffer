package api

import (
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// guard refuses requests that did not come from the manager's own UI.
//
// Binding to 127.0.0.1 keeps other machines out, but not other web pages: any site
// the manager has open can make their browser call localhost. Two attacks follow.
//   - A page at evil.example posts to /api/ask and spends the manager's API credits,
//     or reads the squad. The browser always sends an Origin on cross-origin requests,
//     so an Origin that is not on the allowlist is refused.
//   - DNS rebinding: evil.example re-points its own name at 127.0.0.1, which makes the
//     page "same origin" with this server and sends no Origin at all. The request still
//     carries Host: evil.example, so a Host that is not loopback is refused.
//
// A POST must also be application/json. A page cannot send that cross-origin without a
// CORS preflight, and this server never answers a preflight.
//
// Behind a login proxy (the Kubernetes ingress) the server also answers to allowedHosts, the
// names the proxy forwards. Those requests have already passed the login.
func guard(allowedOrigins, allowedHosts []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) && !namedHost(r.Host, allowedHosts) {
			writeError(w, http.StatusForbidden, "this server answers only to localhost")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !slices.Contains(allowedOrigins, o) {
			writeError(w, http.StatusForbidden, "this origin is not allowed")
			return
		}
		if r.Method == http.MethodPost && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "send Content-Type: application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost reports whether a Host header names this machine.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// namedHost reports whether a Host header is one of names, ignoring case and port.
func namedHost(hostport string, names []string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	for _, n := range names {
		if strings.EqualFold(host, n) {
			return true
		}
	}
	return false
}

// recoverer turns a panic in a handler into a 500 instead of a dropped connection.
func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("handler panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter records the status for the log. It forwards Flush (and, through Unwrap,
// everything else) so streaming still works.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		log.InfoContext(r.Context(), "request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "took", time.Since(start).Round(time.Millisecond))
	})
}
