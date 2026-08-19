package pkg

import (
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"strconv"
	"time"
)

var (
	HttpRequestsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total number of edge requests proxied through the Go gateway.",
		},
		[]string{"method", "status"},
	)

	HttpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "Latency distribution profile of handled gateway routes.",
			Buckets: []float64{0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"method"},
	)
)

func init() {
	prometheus.MustRegister(HttpRequestsCounter)
	prometheus.MustRegister(HttpRequestDuration)
}

// MetricsMiddleware interceptor records raw traffic profiles for scalability validation
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		startTime := time.Now()

		wrappedWriter := &StatusResponseWriter{ResponseWriter: w, StatusCode: http.StatusOK}

		next.ServeHTTP(wrappedWriter, r)

		elapsedDuration := time.Since(startTime).Seconds()
		statusCodeString := strconv.Itoa(wrappedWriter.StatusCode)

		HttpRequestsCounter.WithLabelValues(r.Method, statusCodeString).Inc()
		HttpRequestDuration.WithLabelValues(r.Method).Observe(elapsedDuration)
	})
}
