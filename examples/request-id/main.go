// Package main demonstrates a Celeris feature.
package main

import (
	"log"
	"time"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
	// Create a new router
	router := celeris.NewRouter()

	// Add request ID middleware
	router.Use(celeris.RequestID())

	// Add logging to see request IDs in action
	router.Use(celeris.Logger())

	// Simple route that shows the request ID
	router.GET("/", func(ctx *celeris.Context) error {
		// Get the request ID from the context
		requestID := ctx.Header().Get("X-Request-ID")

		return ctx.JSON(200, map[string]string{
			"message":    "Welcome to Request ID Example",
			"request_id": requestID,
			"note":       "Each request gets a unique ID for tracing",
		})
	})

	// Route that demonstrates request ID propagation
	router.GET("/trace", func(ctx *celeris.Context) error {
		requestID := ctx.Header().Get("X-Request-ID")

		// Simulate some processing time
		time.Sleep(100 * time.Millisecond)

		return ctx.JSON(200, map[string]interface{}{
			"message":         "Request traced successfully",
			"request_id":      requestID,
			"timestamp":       time.Now().UTC().Format(time.RFC3339),
			"processing_time": "100ms",
		})
	})

	// Route that shows how request IDs help with debugging
	router.GET("/debug", func(ctx *celeris.Context) error {
		requestID := ctx.Header().Get("X-Request-ID")

		// Simulate a complex operation with multiple steps
		log.Printf("[%s] Starting debug operation", requestID)

		time.Sleep(50 * time.Millisecond)
		log.Printf("[%s] Step 1 completed", requestID)

		time.Sleep(50 * time.Millisecond)
		log.Printf("[%s] Step 2 completed", requestID)

		time.Sleep(50 * time.Millisecond)
		log.Printf("[%s] Debug operation finished", requestID)

		return ctx.JSON(200, map[string]interface{}{
			"message":    "Debug operation completed",
			"request_id": requestID,
			"steps":      []string{"Step 1", "Step 2", "Step 3"},
		})
	})

	// Route that demonstrates request ID in error scenarios
	router.GET("/error", func(ctx *celeris.Context) error {
		requestID := ctx.Header().Get("X-Request-ID")

		log.Printf("[%s] Simulating an error", requestID)

		return celeris.NewHTTPError(400, "This is a test error with request ID: "+requestID)
	})

	// Route that shows request ID with custom headers
	router.GET("/headers", func(ctx *celeris.Context) error {
		requestID := ctx.Header().Get("X-Request-ID")

		// Add custom headers that include the request ID
		ctx.SetHeader("X-Custom-Header", "Custom value for request "+requestID)
		ctx.SetHeader("X-Processing-Time", time.Now().Format(time.RFC3339))

		return ctx.JSON(200, map[string]interface{}{
			"message":    "Response with custom headers",
			"request_id": requestID,
			"headers": map[string]string{
				"X-Request-ID":    requestID,
				"X-Custom-Header": "Custom value for request " + requestID,
			},
		})
	})

	// Create and start server
	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)

	log.Println("Starting Request ID Example Server on :8080")
	log.Println("Try these endpoints:")
	log.Println("  GET http://localhost:8080/")
	log.Println("  GET http://localhost:8080/trace")
	log.Println("  GET http://localhost:8080/debug")
	log.Println("  GET http://localhost:8080/error")
	log.Println("  GET http://localhost:8080/headers")
	log.Println("")
	log.Println("Each request will get a unique X-Request-ID header")
	log.Println("Check the console logs to see request IDs in action")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait indefinitely to keep the server running
	select {}
}
