// Package main demonstrates all the features of the Celeris HTTP/2 framework
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// Create router with all new features
	router := celeris.NewRouter()

	// 1. Logger middleware (structured logging)
	logConfig := celeris.LoggerConfig{
		Output:    os.Stdout,
		Format:    "json", // or "text"
		SkipPaths: []string{"/health", "/metrics"},
	}
	router.Use(celeris.LoggerWithConfig(logConfig))

	// 2. Recovery middleware
	router.Use(celeris.Recovery())

	// 3. Request ID middleware
	router.Use(celeris.RequestID())

	// 4. Compression middleware (gzip + brotli)
	compressConfig := celeris.CompressConfig{
		Level:         6,
		MinSize:       1024,
		ExcludedTypes: []string{"image/", "video/"},
	}
	router.Use(celeris.CompressWithConfig(compressConfig))

	// 5. Prometheus metrics middleware
	router.Use(celeris.Prometheus())

	// 6. OpenTelemetry tracing middleware
	router.Use(celeris.Tracing())

	// 7. CORS middleware
	router.Use(celeris.CORS(celeris.DefaultCORSConfig()))

	// Basic routes
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Welcome to Celeris Features Demo",
			"version": "1.0.0",
		})
	})

	// Health check (skipped by logger and metrics)
	router.GET("/health", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"status": "ok"})
	})

	// Query parameter parsing
	router.GET("/search", func(ctx *celeris.Context) error {
		q := ctx.Query("q")
		page, _ := ctx.QueryInt("page")
		limit := ctx.QueryDefault("limit", "10")

		return ctx.JSON(200, map[string]interface{}{
			"query": q,
			"page":  page,
			"limit": limit,
		})
	})

	// Cookie handling
	router.GET("/set-cookie", func(ctx *celeris.Context) error {
		ctx.SetHeader("set-cookie", "session=abc123; Path=/; HttpOnly")
		return ctx.String(200, "Cookie set")
	})

	router.GET("/get-cookie", func(ctx *celeris.Context) error {
		session := ctx.Cookie("session")
		return ctx.JSON(200, map[string]string{
			"session": session,
		})
	})

	// Form handling
	router.POST("/form", func(ctx *celeris.Context) error {
		name, _ := ctx.FormValue("name")
		email, _ := ctx.FormValue("email")

		return ctx.JSON(200, map[string]string{
			"name":  name,
			"email": email,
		})
	})

	// URL parameters
	router.GET("/users/:id", func(ctx *celeris.Context) error {
		id := ctx.Param("id")
		return ctx.JSON(200, map[string]interface{}{
			"id":   id,
			"name": "John Doe",
		})
	})

	// Static file serving
	router.Static("/static", "./static")

	// File download
	router.GET("/download/:filename", func(ctx *celeris.Context) error {
		filename := ctx.Param("filename")
		// In production, validate and sanitize filename
		return ctx.Attachment(filename, "./files/"+filename)
	})

	// Streaming response
	router.GET("/stream", func(ctx *celeris.Context) error {
		ctx.SetHeader("content-type", "text/plain")
		ctx.SetStatus(200)

		for i := 0; i < 5; i++ {
			_, _ = fmt.Fprintf(ctx.Writer(), "Chunk %d\n", i)
			if err := ctx.Flush(); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
		}
		return nil
	})

	// Server-Sent Events (SSE)
	router.GET("/events", func(ctx *celeris.Context) error {
		ctx.SetHeader("content-type", "text/event-stream")
		ctx.SetHeader("cache-control", "no-cache")

		for i := 0; i < 10; i++ {
			if err := ctx.SSE(celeris.SSEEvent{
				ID:    fmt.Sprintf("%d", i),
				Event: "message",
				Data:  fmt.Sprintf("Event %d at %s", i, time.Now().Format(time.RFC3339)),
			}); err != nil {
				return err
			}

			if err := ctx.Flush(); err != nil {
				return err
			}

			time.Sleep(1 * time.Second)
		}
		return nil
	})

	// Error handling with HTTPError
	router.GET("/error", func(_ *celeris.Context) error {
		return celeris.NewHTTPError(400, "Bad Request").WithDetails(map[string]string{
			"field": "username",
			"issue": "required",
		})
	})

	// Custom error handler
	router.ErrorHandler(func(ctx *celeris.Context, err error) error {
		log.Printf("Error occurred: %v", err)
		return celeris.DefaultErrorHandler(ctx, err)
	})

	// Prometheus metrics endpoint (standard HTTP handler)
	// Note: This would need a wrapper, for now just document it
	router.GET("/metrics", func(ctx *celeris.Context) error {
		// In production, you'd expose promhttp.Handler() via a wrapper
		return ctx.String(200, "Prometheus metrics at /metrics (use standard HTTP client)")
	})

	// Create server with custom config
	config := celeris.Config{
		Addr:                 ":8080",
		Multicore:            true,
		NumEventLoop:         0, // auto
		ReusePort:            true,
		MaxConcurrentStreams: 100,
	}

	server := celeris.New(config)

	log.Println("Starting Celeris server on :8080")
	log.Println("Features:")
	log.Println("  - Structured logging (JSON)")
	log.Println("  - Compression (gzip + brotli)")
	log.Println("  - Prometheus metrics")
	log.Println("  - OpenTelemetry tracing")
	log.Println("  - Query/Cookie/Form parsing")
	log.Println("  - Static file serving")
	log.Println("  - Streaming responses")
	log.Println("  - Server-Sent Events")
	log.Println("  - Error handling")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}

	// For Prometheus HTTP endpoint, you can run a separate net/http server:
	// go func() {
	// 	http.Handle("/metrics", promhttp.Handler())
	// 	http.ListenAndServe(":9090", nil)
	// }()
	_ = promhttp.Handler() // silence unused warning
}
