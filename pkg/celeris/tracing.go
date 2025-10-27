package celeris

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TracingConfig defines the configuration options for the OpenTelemetry tracing middleware.
type TracingConfig struct {
	// TracerName is the name of the tracer (default: "celeris")
	TracerName string
	// SkipPaths lists paths to skip tracing (e.g., health checks)
	SkipPaths []string
	// Propagator is the propagation format (default: TraceContext)
	Propagator propagation.TextMapPropagator
}

// DefaultTracingConfig returns a TracingConfig with sensible defaults.
func DefaultTracingConfig() TracingConfig {
	return TracingConfig{
		TracerName: "celeris",
		SkipPaths:  []string{"/health", "/metrics"},
		Propagator: propagation.TraceContext{},
	}
}

// Tracing returns a middleware that adds OpenTelemetry tracing to HTTP requests.
// It uses default configuration settings and skips tracing for health and metrics endpoints.
func Tracing() Middleware {
	return TracingWithConfig(DefaultTracingConfig())
}

// TracingWithConfig returns a middleware that adds OpenTelemetry tracing with custom configuration.
// It creates spans for incoming requests and propagates trace context through headers.
func TracingWithConfig(config TracingConfig) Middleware {
	if config.TracerName == "" {
		config.TracerName = "celeris"
	}
	if config.Propagator == nil {
		config.Propagator = propagation.TraceContext{}
	}

	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	tracer := otel.Tracer(config.TracerName)

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			// Skip tracing for paths in the skip list
			if skipMap[ctx.Path()] {
				return next.ServeHTTP2(ctx)
			}

			// Extract parent span context from headers
			carrier := &headerCarrier{headers: ctx.Header()}
			parentCtx := config.Propagator.Extract(ctx.Context(), carrier)

			// Start span
			spanCtx, span := tracer.Start(
				parentCtx,
				ctx.Method()+" "+ctx.Path(),
				trace.WithSpanKind(trace.SpanKindServer),
			)
			defer span.End()

			// Set standard HTTP span attributes
			span.SetAttributes(
				attribute.String("http.method", ctx.Method()),
				attribute.String("http.target", ctx.Path()),
				attribute.String("http.scheme", ctx.Scheme()),
				attribute.String("http.host", ctx.Authority()),
				attribute.Int("http.request_content_length", 0), // TODO: get actual length
			)

			// Add request ID if available
			if reqID, ok := ctx.Get("request-id"); ok {
				if reqIDStr, ok := reqID.(string); ok {
					span.SetAttributes(attribute.String("http.request_id", reqIDStr))
				}
			}

			// Update context with span context
			originalCtx := ctx.ctx
			ctx.ctx = spanCtx

			// Execute handler
			err := next.ServeHTTP2(ctx)

			// Restore original context
			ctx.ctx = originalCtx

			// Record status code
			span.SetAttributes(attribute.Int("http.status_code", ctx.Status()))

			// Record error if present
			switch {
			case err != nil:
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			case ctx.Status() >= 400:
				span.SetStatus(codes.Error, "HTTP error")
			default:
				span.SetStatus(codes.Ok, "")
			}

			return err
		})
	}
}

// headerCarrier adapts Celeris Headers to propagation.TextMapCarrier.
type headerCarrier struct {
	headers *Headers
}

func (hc *headerCarrier) Get(key string) string {
	return hc.headers.Get(key)
}

func (hc *headerCarrier) Set(key, value string) {
	hc.headers.Set(key, value)
}

func (hc *headerCarrier) Keys() []string {
	keys := make([]string, 0, len(hc.headers.headers))
	for _, h := range hc.headers.headers {
		keys = append(keys, h[0])
	}
	return keys
}
