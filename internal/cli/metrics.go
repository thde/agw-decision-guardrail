package cli

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// instrumentHandler records requests to next. next must be a *http.ServeMux:
// it sets r.Pattern, which bounds label cardinality.
func instrumentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		pattern := r.Pattern
		if pattern == "" {
			pattern = "unmatched"
		}
		metrics.GetOrCreateCounter(fmt.Sprintf(`http_server_requests_total{pattern=%q,code="%d"}`, pattern, sw.code)).Inc()
		metrics.GetOrCreateHistogram(fmt.Sprintf(`http_server_request_duration_seconds{pattern=%q}`, pattern)).UpdateDuration(start)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type instrumentedTransport struct{ next http.RoundTripper }

func (t instrumentedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(r)
	code := "error"
	if err == nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	metrics.GetOrCreateCounter(fmt.Sprintf(`http_client_requests_total{host=%q,method=%q,code=%q}`, r.URL.Host, r.Method, code)).Inc()
	metrics.GetOrCreateHistogram(fmt.Sprintf(`http_client_request_duration_seconds{host=%q,method=%q}`, r.URL.Host, r.Method)).UpdateDuration(start)
	return resp, err //nolint:wrapcheck // Transparent decorator; http.Client adds context.
}
