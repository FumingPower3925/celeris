package celeris

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"
)

// LoggerConfig defines the configuration options for the Logger middleware.
type LoggerConfig struct {
	// Output specifies where logs are written (defaults to os.Stdout)
	Output io.Writer
	// Format specifies the log format: "json" or "text" (default: "text")
	Format string
	// SkipPaths lists paths to skip logging (e.g., health checks)
	SkipPaths []string
	// CustomFields allows adding custom fields to each log entry
	CustomFields func(ctx *Context) map[string]interface{}
}

// DefaultLoggerConfig returns a LoggerConfig with sensible defaults.
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Output: os.Stdout,
		Format: "text",
	}
}

// Logger returns a middleware that logs HTTP requests with structured output.
// By default, it logs to stdout in text format, excluding no paths.
func Logger() Middleware {
	return LoggerWithConfig(DefaultLoggerConfig())
}

// LoggerWithConfig returns a middleware that logs HTTP requests with custom configuration.
func LoggerWithConfig(config LoggerConfig) Middleware {
	if config.Output == nil {
		config.Output = os.Stdout
	}
	if config.Format == "" {
		config.Format = "text"
	}

	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			// Skip logging for specified paths
			if skipMap[ctx.Path()] {
				return next.ServeHTTP2(ctx)
			}

			start := time.Now()

			// Execute handler
			err := next.ServeHTTP2(ctx)

			// Calculate duration
			duration := time.Since(start)

			// Build log entry
			entry := map[string]interface{}{
				"time":      start.Format(time.RFC3339),
				"method":    ctx.Method(),
				"path":      ctx.Path(),
				"status":    ctx.Status(),
				"duration":  duration.Milliseconds(),
				"remote_ip": ctx.Header().Get(":authority"),
			}

			// Add request ID if available
			if reqID, ok := ctx.Get("request-id"); ok {
				entry["request_id"] = reqID
			}

			// Add custom fields if provided
			if config.CustomFields != nil {
				for k, v := range config.CustomFields(ctx) {
					entry[k] = v
				}
			}

			// Add error if present
			if err != nil {
				entry["error"] = err.Error()
			}

			// Format and write log
			if config.Format == "json" {
				data, _ := json.Marshal(entry)
				_, _ = fmt.Fprintf(config.Output, "%s\n", data)
			} else {
				// Text format
				_, _ = fmt.Fprintf(config.Output, "[%s] %s %s %d %dms",
					entry["time"],
					entry["method"],
					entry["path"],
					entry["status"],
					entry["duration"])
				if reqID, ok := entry["request_id"]; ok {
					_, _ = fmt.Fprintf(config.Output, " req_id=%v", reqID)
				}
				if err != nil {
					_, _ = fmt.Fprintf(config.Output, " error=%q", err.Error())
				}
				_, _ = fmt.Fprintln(config.Output)
			}

			return err
		})
	}
}

// Recovery returns a middleware that recovers from panics.
// It catches panics during request handling and returns a 500 Internal Server Error response.
func Recovery() Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			defer func() {
				if r := recover(); r != nil {
					_ = ctx.String(500, "Internal Server Error")
				}
			}()

			return next.ServeHTTP2(ctx)
		})
	}
}

// CORS returns a middleware that handles Cross-Origin Resource Sharing.
// It sets appropriate CORS headers and handles preflight OPTIONS requests.
func CORS(config CORSConfig) Middleware {
	if config.AllowOrigin == "" {
		config.AllowOrigin = "*"
	}
	if config.AllowMethods == "" {
		config.AllowMethods = "GET, POST, PUT, DELETE, OPTIONS, PATCH"
	}
	if config.AllowHeaders == "" {
		config.AllowHeaders = "Accept, Content-Type, Content-Length, Authorization"
	}

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			ctx.SetHeader("Access-Control-Allow-Origin", config.AllowOrigin)
			ctx.SetHeader("Access-Control-Allow-Methods", config.AllowMethods)
			ctx.SetHeader("Access-Control-Allow-Headers", config.AllowHeaders)

			if config.AllowCredentials {
				ctx.SetHeader("Access-Control-Allow-Credentials", "true")
			}

			if config.MaxAge > 0 {
				ctx.SetHeader("Access-Control-Max-Age", fmt.Sprintf("%d", config.MaxAge))
			}

			if ctx.Method() == "OPTIONS" {
				return ctx.NoContent(204)
			}

			return next.ServeHTTP2(ctx)
		})
	}
}

// CORSConfig holds CORS middleware configuration.
type CORSConfig struct {
	AllowOrigin      string
	AllowMethods     string
	AllowHeaders     string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig returns sensible CORS defaults.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigin:      "*",
		AllowMethods:     "GET, POST, PUT, DELETE, OPTIONS, PATCH",
		AllowHeaders:     "Accept, Content-Type, Content-Length, Authorization",
		AllowCredentials: false,
		MaxAge:           3600,
	}
}

// RequestID returns a middleware that adds a unique request ID to each request.
// If a request ID is not already present in the headers, one is generated.
// The request ID is added to both the context and response headers.
func RequestID() Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			requestID := ctx.Header().Get("x-request-id")
			if requestID == "" {
				requestID = generateRequestID()
			}

			ctx.Set("request-id", requestID)
			ctx.SetHeader("X-Request-ID", requestID)

			return next.ServeHTTP2(ctx)
		})
	}
}

var requestIDCounter uint64

func generateRequestID() string {
	// Combine timestamp with counter and random number for uniqueness
	counter := atomic.AddUint64(&requestIDCounter, 1)

	// Use crypto/rand for secure random number
	var randomBytes [8]byte
	_, _ = rand.Read(randomBytes[:])
	randomNum := binary.BigEndian.Uint64(randomBytes[:])

	return fmt.Sprintf("%d-%d-%d", time.Now().UnixNano(), counter, randomNum)
}

// Timeout returns a middleware that limits request processing time.
// It cancels requests that exceed the specified duration with a 504 Gateway Timeout response.
func Timeout(duration time.Duration) Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			timeoutCtx, cancel := context.WithTimeout(ctx.Context(), duration)
			defer cancel()

			done := make(chan error, 1)
			responded := atomic.Bool{}

			go func() {
				err := next.ServeHTTP2(ctx)
				responded.Store(true)
				done <- err
			}()

			select {
			case err := <-done:
				return err
			case <-timeoutCtx.Done():
				// Give the handler a moment to finish if it's close
				time.Sleep(10 * time.Millisecond)
				// Only send timeout response if handler hasn't responded yet
				if !responded.Load() {
					// Use a new context to avoid race conditions
					timeoutCtx := &Context{
						StreamID:        ctx.StreamID,
						headers:         ctx.headers,
						body:            ctx.body,
						statusCode:      504,
						responseHeaders: NewHeaders(),
						responseBody:    responseBufPool.Get().(*bytes.Buffer),
						stream:          ctx.stream,
						ctx:             ctx.ctx,
						writeResponse:   ctx.writeResponse,
						pushPromise:     ctx.pushPromise,
						values:          nil,
						hasFlushed:      false,
						streamBuffer:    nil,
						method:          ctx.method,
						path:            ctx.path,
						scheme:          ctx.scheme,
						authority:       ctx.authority,
						writeMu:         sync.Mutex{},
					}
					defer func() {
						timeoutCtx.responseBody.Reset()
						responseBufPool.Put(timeoutCtx.responseBody)
					}()
					return timeoutCtx.String(504, "Gateway Timeout")
				}
				// Handler finished just in time, return its error
				return <-done
			}
		})
	}
}

// CompressConfig holds configuration for the Compress middleware.
type CompressConfig struct {
	// Level specifies the compression level (1-9 for gzip, 0-11 for brotli)
	Level int
	// MinSize specifies the minimum response size to compress (default: 1024 bytes)
	MinSize int
	// ExcludedTypes lists content types to skip compression
	ExcludedTypes []string
}

// DefaultCompressConfig returns a CompressConfig with sensible defaults.
func DefaultCompressConfig() CompressConfig {
	return CompressConfig{
		Level:   6, // balanced compression
		MinSize: 1024,
		ExcludedTypes: []string{
			"image/",
			"video/",
			"audio/",
			"application/zip",
			"application/gzip",
		},
	}
}

// Compress returns a middleware that compresses response bodies with gzip or brotli.
// It uses default compression settings with a minimum response size of 1024 bytes.
func Compress() Middleware {
	return CompressWithConfig(DefaultCompressConfig())
}

// CompressWithConfig returns a middleware that compresses response bodies with custom configuration.
func CompressWithConfig(config CompressConfig) Middleware {
	if config.MinSize == 0 {
		config.MinSize = 1024
	}
	if config.Level == 0 {
		config.Level = 6
	}

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			acceptEncoding := ctx.Header().Get("accept-encoding")

			// Check if client supports compression
			supportsBrotli := strings.Contains(acceptEncoding, "br")
			supportsGzip := strings.Contains(acceptEncoding, "gzip")

			if !supportsBrotli && !supportsGzip {
				return next.ServeHTTP2(ctx)
			}

			// Create a wrapper to intercept the response
			originalBuf := ctx.responseBody
			tempBuf := responseBufPool.Get().(*bytes.Buffer)
			tempBuf.Reset()
			ctx.responseBody = tempBuf

			// Execute handler
			err := next.ServeHTTP2(ctx)

			// Get response body
			body := tempBuf.Bytes()

			// Restore original buffer
			ctx.responseBody = originalBuf
			tempBuf.Reset()
			responseBufPool.Put(tempBuf)

			// Check if we should compress
			shouldCompress := len(body) >= config.MinSize

			// Check content type exclusions
			contentType := ctx.responseHeaders.Get("content-type")
			for _, excluded := range config.ExcludedTypes {
				if strings.HasPrefix(contentType, excluded) {
					shouldCompress = false
					break
				}
			}

			if !shouldCompress {
				_, writeErr := ctx.responseBody.Write(body)
				if writeErr != nil && err == nil {
					err = writeErr
				}
				return err
			}

			// Compress the body
			var compressed bytes.Buffer
			var encoding string

			if supportsBrotli {
				// Brotli compression
				writer := brotli.NewWriterLevel(&compressed, config.Level)
				if _, werr := writer.Write(body); werr != nil {
					_ = writer.Close()
					// Fallback to uncompressed
					_, _ = ctx.responseBody.Write(body)
					return err
				}
				_ = writer.Close()
				encoding = "br"
			} else if supportsGzip {
				// Gzip compression
				writer, _ := gzip.NewWriterLevel(&compressed, config.Level)
				if _, werr := writer.Write(body); werr != nil {
					_ = writer.Close()
					// Fallback to uncompressed
					_, _ = ctx.responseBody.Write(body)
					return err
				}
				_ = writer.Close()
				encoding = "gzip"
			}

			// Only use compressed version if it's actually smaller
			if compressed.Len() < len(body) && compressed.Len() > 0 {
				ctx.SetHeader("Content-Encoding", encoding)
				ctx.SetHeader("Vary", "Accept-Encoding")
				_, writeErr := ctx.responseBody.Write(compressed.Bytes())
				if writeErr != nil && err == nil {
					err = writeErr
				}
			} else {
				// Compressed version is larger, use original
				_, writeErr := ctx.responseBody.Write(body)
				if writeErr != nil && err == nil {
					err = writeErr
				}
			}

			return err
		})
	}
}

// RateLimiterConfig holds configuration for the RateLimiter middleware.
type RateLimiterConfig struct {
	// RequestsPerSecond is the maximum number of requests allowed per second
	RequestsPerSecond int
	// BurstSize is the maximum number of requests that can be burst at once
	BurstSize int
	// KeyFunc is a function that returns a unique key for rate limiting (default: uses client IP)
	KeyFunc func(ctx *Context) string
	// SkipPaths lists paths to skip rate limiting (e.g., health checks)
	SkipPaths []string
	// ErrorHandler is called when rate limit is exceeded (default: returns 429)
	ErrorHandler func(ctx *Context) error
}

// DefaultRateLimiterConfig returns a RateLimiterConfig with sensible defaults.
func DefaultRateLimiterConfig(requestsPerSecond int) RateLimiterConfig {
	return RateLimiterConfig{
		RequestsPerSecond: requestsPerSecond,
		BurstSize:         requestsPerSecond * 2, // Allow 2x burst
		KeyFunc: func(ctx *Context) string {
			// Use client IP for rate limiting
			clientIP := ctx.Header().Get("x-forwarded-for")
			if clientIP == "" {
				clientIP = ctx.Header().Get("x-real-ip")
			}
			if clientIP == "" {
				clientIP = ctx.Authority()
			}
			return clientIP
		},
		SkipPaths: []string{"/health", "/metrics"},
		ErrorHandler: func(ctx *Context) error {
			ctx.SetHeader("x-ratelimit-limit", fmt.Sprintf("%d", requestsPerSecond))
			ctx.SetHeader("x-ratelimit-remaining", "0")
			ctx.SetHeader("retry-after", "1")
			return ctx.String(429, "Too Many Requests")
		},
	}
}

// RateLimiter returns a middleware that limits requests per second using a token bucket algorithm.
func RateLimiter(requestsPerSecond int) Middleware {
	return RateLimiterWithConfig(DefaultRateLimiterConfig(requestsPerSecond))
}

// RateLimiterWithConfig returns a middleware that limits requests with custom configuration.
func RateLimiterWithConfig(config RateLimiterConfig) Middleware {
	if config.RequestsPerSecond <= 0 {
		panic("requests per second must be positive")
	}
	if config.BurstSize <= 0 {
		config.BurstSize = config.RequestsPerSecond * 2
	}
	if config.KeyFunc == nil {
		config.KeyFunc = func(ctx *Context) string {
			clientIP := ctx.Header().Get("x-forwarded-for")
			if clientIP == "" {
				clientIP = ctx.Header().Get("x-real-ip")
			}
			if clientIP == "" {
				clientIP = ctx.Authority()
			}
			if clientIP == "" {
				// Fallback for localhost connections
				clientIP = "localhost"
			}
			return clientIP
		}
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(ctx *Context) error {
			ctx.SetHeader("x-ratelimit-limit", fmt.Sprintf("%d", config.RequestsPerSecond))
			ctx.SetHeader("x-ratelimit-remaining", "0")
			ctx.SetHeader("retry-after", "1")
			return ctx.String(429, "Too Many Requests")
		}
	}

	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	// Token bucket limiter
	limiters := make(map[string]*tokenBucket)
	mu := sync.RWMutex{}

	// Cleanup old limiters periodically
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			for key, limiter := range limiters {
				if time.Since(limiter.lastAccess) > 10*time.Minute {
					delete(limiters, key)
				}
			}
			mu.Unlock()
		}
	}()

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			// Skip rate limiting for specified paths
			if skipMap[ctx.Path()] {
				return next.ServeHTTP2(ctx)
			}

			key := config.KeyFunc(ctx)
			if key == "" {
				// If we can't identify the client, allow the request
				return next.ServeHTTP2(ctx)
			}

			// Get or create limiter for this key
			mu.Lock()
			limiter, exists := limiters[key]
			if !exists {
				limiter = newTokenBucket(config.RequestsPerSecond, config.BurstSize)
				limiters[key] = limiter
			}
			limiter.lastAccess = time.Now()
			mu.Unlock()

			// Check if request is allowed
			if !limiter.allow() {
				// Set rate limit headers
				ctx.SetHeader("x-ratelimit-limit", fmt.Sprintf("%d", config.RequestsPerSecond))
				ctx.SetHeader("x-ratelimit-remaining", "0")
				ctx.SetHeader("x-ratelimit-reset", fmt.Sprintf("%d", time.Now().Add(time.Second).Unix()))
				ctx.SetHeader("retry-after", "1")
				return config.ErrorHandler(ctx)
			}

			// Set rate limit headers for successful requests
			remaining := limiter.tokens
			if remaining < 0 {
				remaining = 0
			}
			ctx.SetHeader("x-ratelimit-limit", fmt.Sprintf("%d", config.RequestsPerSecond))
			ctx.SetHeader("x-ratelimit-remaining", fmt.Sprintf("%d", remaining))
			ctx.SetHeader("x-ratelimit-reset", fmt.Sprintf("%d", time.Now().Add(time.Second).Unix()))

			return next.ServeHTTP2(ctx)
		})
	}
}

// tokenBucket implements a token bucket rate limiter
type tokenBucket struct {
	capacity   int
	tokens     int
	refillRate int
	lastRefill time.Time
	lastAccess time.Time
	mu         sync.Mutex
}

// newTokenBucket creates a new token bucket with the given capacity and refill rate
func newTokenBucket(rate, burst int) *tokenBucket {
	return &tokenBucket{
		capacity:   burst,
		tokens:     burst,
		refillRate: rate,
		lastRefill: time.Now(),
		lastAccess: time.Now(),
	}
}

// allow checks if a request is allowed and consumes a token if so
func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()

	// Refill tokens based on time elapsed
	elapsed := now.Sub(tb.lastRefill)
	tokensToAdd := int(float64(elapsed.Nanoseconds()) / float64(time.Second) * float64(tb.refillRate))

	if tokensToAdd > 0 {
		tb.tokens += tokensToAdd
		if tb.tokens > tb.capacity {
			tb.tokens = tb.capacity
		}
		tb.lastRefill = now
	}

	// Check if we have tokens available
	if tb.tokens > 0 {
		tb.tokens--
		return true
	}

	return false
}

// HealthConfig holds configuration for the Health middleware.
type HealthConfig struct {
	// Path is the endpoint path for health checks (default: "/health")
	Path string
	// Handler is a custom health check handler (optional)
	Handler func(ctx *Context) error
	// SkipPaths lists additional paths to skip health check middleware
	SkipPaths []string
}

// DefaultHealthConfig returns a HealthConfig with sensible defaults.
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		Path: "/health",
		Handler: func(ctx *Context) error {
			return ctx.JSON(200, map[string]interface{}{
				"status":    "ok",
				"timestamp": time.Now().UTC().Format(time.RFC3339),
				"uptime":    time.Since(startTime).String(),
			})
		},
		SkipPaths: []string{},
	}
}

var startTime = time.Now()

// Health returns a middleware that sets up a health check endpoint.
func Health() Middleware {
	return HealthWithConfig(DefaultHealthConfig())
}

// HealthWithConfig returns a middleware that sets up a health check endpoint with custom configuration.
func HealthWithConfig(config HealthConfig) Middleware {
	if config.Path == "" {
		config.Path = "/health"
	}
	if config.Handler == nil {
		config.Handler = func(ctx *Context) error {
			return ctx.JSON(200, map[string]interface{}{
				"status":    "ok",
				"timestamp": time.Now().UTC().Format(time.RFC3339),
				"uptime":    time.Since(startTime).String(),
			})
		}
	}

	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			// Handle health check endpoint
			if ctx.Path() == config.Path {
				return config.Handler(ctx)
			}

			// Skip health check middleware for specified paths
			if skipMap[ctx.Path()] {
				return next.ServeHTTP2(ctx)
			}

			return next.ServeHTTP2(ctx)
		})
	}
}

// DocsConfig holds configuration for the Docs middleware.
type DocsConfig struct {
	// Path is the endpoint path for documentation (default: "/docs")
	Path string
	// Title is the title of the API documentation (default: "API Documentation")
	Title string
	// Description is the description of the API (default: "API Documentation")
	Description string
	// Version is the API version (default: "1.0.0")
	Version string
	// ContactName is the contact name for the API
	ContactName string
	// ContactEmail is the contact email for the API
	ContactEmail string
	// ContactURL is the contact URL for the API
	ContactURL string
	// LicenseName is the license name for the API
	LicenseName string
	// LicenseURL is the license URL for the API
	LicenseURL string
	// ServerURL is the server URL (default: "http://localhost:8080")
	ServerURL string
	// Routes is a list of routes to document (optional, will be auto-discovered if empty)
	Routes []RouteInfo
	// SkipPaths lists additional paths to skip docs middleware
	SkipPaths []string
	// AutoDiscover enables automatic route discovery from the router
	AutoDiscover bool
	// Router is the router to discover routes from (required if AutoDiscover is true)
	Router *Router
}

// RouteInfo represents information about an API route.
type RouteInfo struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Tags        []string          `json:"tags"`
	Parameters  []ParameterInfo   `json:"parameters,omitempty"`
	Responses   map[string]string `json:"responses,omitempty"`
}

// ParameterInfo represents information about a route parameter.
type ParameterInfo struct {
	Name        string `json:"name"`
	In          string `json:"in"` // "path", "query", "header", "cookie"
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

// DefaultDocsConfig returns a DocsConfig with sensible defaults.
func DefaultDocsConfig() DocsConfig {
	return DocsConfig{
		Path:         "/docs",
		Title:        "API Documentation",
		Description:  "API Documentation",
		Version:      "1.0.0",
		ServerURL:    "http://localhost:8080",
		Routes:       []RouteInfo{},
		SkipPaths:    []string{},
		AutoDiscover: true,
		Router:       nil,
	}
}

// Docs returns a middleware that serves Swagger-style API documentation.
func Docs() Middleware {
	return DocsWithConfig(DefaultDocsConfig())
}

// DocsWithRouter returns a middleware that automatically discovers and documents routes from the given router.
func DocsWithRouter(router *Router) Middleware {
	config := DefaultDocsConfig()
	config.Router = router
	config.AutoDiscover = true
	return DocsWithConfig(config)
}

// DocsWithConfig returns a middleware that serves API documentation with custom configuration.
func DocsWithConfig(config DocsConfig) Middleware {
	if config.Path == "" {
		config.Path = "/docs"
	}
	if config.Title == "" {
		config.Title = "API Documentation"
	}
	if config.Description == "" {
		config.Description = "API Documentation"
	}
	if config.Version == "" {
		config.Version = "1.0.0"
	}
	if config.ServerURL == "" {
		config.ServerURL = "http://localhost:8080"
	}

	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			// Handle documentation endpoint
			if ctx.Path() == config.Path {
				return serveDocs(ctx, config)
			}

			// Skip docs middleware for specified paths
			if skipMap[ctx.Path()] {
				return next.ServeHTTP2(ctx)
			}

			return next.ServeHTTP2(ctx)
		})
	}
}

// serveDocs serves the Swagger UI documentation page
func serveDocs(ctx *Context, config DocsConfig) error {
	// Auto-discover routes if enabled and router is provided
	if config.AutoDiscover && config.Router != nil {
		discoveredRoutes := config.Router.GetRoutes()
		// Flatten the discovered routes into a single slice
		var allRoutes []RouteInfo
		for _, methodRoutes := range discoveredRoutes {
			allRoutes = append(allRoutes, methodRoutes...)
		}
		config.Routes = allRoutes
	}

	// Generate OpenAPI spec
	spec := generateOpenAPISpec(config)

	// Serve Swagger UI HTML
	html := generateSwaggerUIHTML(config, spec)

	ctx.SetHeader("content-type", "text/html; charset=utf-8")
	return ctx.HTML(200, html)
}

// generateOpenAPISpec generates an OpenAPI 3.0 specification
func generateOpenAPISpec(config DocsConfig) map[string]interface{} {
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":       config.Title,
			"description": config.Description,
			"version":     config.Version,
		},
		"servers": []map[string]interface{}{
			{
				"url":         config.ServerURL,
				"description": "API Server",
			},
		},
		"paths": make(map[string]interface{}),
	}

	// Add contact info if provided
	if config.ContactName != "" || config.ContactEmail != "" || config.ContactURL != "" {
		contact := make(map[string]interface{})
		if config.ContactName != "" {
			contact["name"] = config.ContactName
		}
		if config.ContactEmail != "" {
			contact["email"] = config.ContactEmail
		}
		if config.ContactURL != "" {
			contact["url"] = config.ContactURL
		}
		spec["info"].(map[string]interface{})["contact"] = contact
	}

	// Add license info if provided
	if config.LicenseName != "" || config.LicenseURL != "" {
		license := make(map[string]interface{})
		if config.LicenseName != "" {
			license["name"] = config.LicenseName
		}
		if config.LicenseURL != "" {
			license["url"] = config.LicenseURL
		}
		spec["info"].(map[string]interface{})["license"] = license
	}

	// Add routes to paths
	paths := spec["paths"].(map[string]interface{})
	for _, route := range config.Routes {
		pathItem := make(map[string]interface{})
		if paths[route.Path] != nil {
			pathItem = paths[route.Path].(map[string]interface{})
		}

		operation := map[string]interface{}{
			"summary":     route.Summary,
			"description": route.Description,
		}

		if len(route.Tags) > 0 {
			operation["tags"] = route.Tags
		}

		if len(route.Parameters) > 0 {
			operation["parameters"] = route.Parameters
		}

		if len(route.Responses) > 0 {
			operation["responses"] = route.Responses
		} else {
			operation["responses"] = map[string]interface{}{
				"200": map[string]interface{}{
					"description": "Successful response",
				},
			}
		}

		pathItem[strings.ToLower(route.Method)] = operation
		paths[route.Path] = pathItem
	}

	return spec
}

// generateSwaggerUIHTML generates the Swagger UI HTML page
func generateSwaggerUIHTML(config DocsConfig, spec map[string]interface{}) string {
	// Convert spec to JSON
	specJSON, _ := json.MarshalIndent(spec, "", "  ")
	specJSONStr := strings.ReplaceAll(string(specJSON), "`", "\\`")

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s</title>
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@4.15.5/swagger-ui.css" />
    <style>
        html {
            box-sizing: border-box;
            overflow: -moz-scrollbars-vertical;
            overflow-y: scroll;
        }
        *, *:before, *:after {
            box-sizing: inherit;
        }
        body {
            margin:0;
            background: #fafafa;
        }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@4.15.5/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@4.15.5/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            const ui = SwaggerUIBundle({
                url: '',
                spec: %s,
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout"
            });
        };
    </script>
</body>
</html>`, config.Title, specJSONStr)
}
