package celeris

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "celeris_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "celeris_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	httpRequestsInFlight = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "celeris_http_requests_in_flight",
			Help: "Current number of HTTP requests being served",
		},
	)

	httpResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "celeris_http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"method", "path", "status"},
	)
)

// PrometheusConfig holds configuration for Prometheus metrics middleware.
type PrometheusConfig struct {
	// Subsystem is the Prometheus subsystem name (default: "http")
	Subsystem string
	// SkipPaths lists paths to skip metrics collection (e.g., /metrics, /health)
	SkipPaths []string
	// Buckets defines histogram buckets for request duration
	Buckets []float64
}

// DefaultPrometheusConfig returns a PrometheusConfig with sensible defaults.
func DefaultPrometheusConfig() PrometheusConfig {
	return PrometheusConfig{
		Subsystem: "http",
		SkipPaths: []string{"/metrics"},
		Buckets:   prometheus.DefBuckets,
	}
}

// Prometheus returns a middleware that collects Prometheus metrics.
func Prometheus() Middleware {
	return PrometheusWithConfig(DefaultPrometheusConfig())
}

// PrometheusWithConfig returns a middleware that collects Prometheus metrics with custom configuration.
func PrometheusWithConfig(config PrometheusConfig) Middleware {
	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			// Skip metrics for specified paths
			if skipMap[ctx.Path()] {
				return next.ServeHTTP2(ctx)
			}

			start := time.Now()
			httpRequestsInFlight.Inc()
			defer httpRequestsInFlight.Dec()

			// Execute handler
			err := next.ServeHTTP2(ctx)

			// Record metrics
			duration := time.Since(start).Seconds()
			status := strconv.Itoa(ctx.Status())
			method := ctx.Method()
			path := ctx.Path()

			httpRequestsTotal.WithLabelValues(method, path, status).Inc()
			httpRequestDuration.WithLabelValues(method, path, status).Observe(duration)

			// Estimate response size (from buffer)
			responseSize := float64(ctx.responseBody.Len())
			httpResponseSize.WithLabelValues(method, path, status).Observe(responseSize)

			return err
		})
	}
}
