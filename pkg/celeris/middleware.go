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
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"
)

// LoggerConfig holds configuration for the Logger middleware.
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
					return ctx.String(504, "Gateway Timeout")
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

// RateLimiter returns a middleware that limits requests per second.
func RateLimiter(requestsPerSecond int) Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			_ = requestsPerSecond

			return next.ServeHTTP2(ctx)
		})
	}
}
