// Package main demonstrates a Celeris feature.
package main

import (
	"log"
	"os"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	// Create a new router
	router := celeris.NewRouter()

	// Add structured logging middleware
	logConfig := celeris.LoggerConfig{
		Output:    os.Stdout,
		Format:    "json",              // Use JSON format for structured logging
		SkipPaths: []string{"/health"}, // Skip logging for health checks
		CustomFields: func(_ *celeris.Context) map[string]interface{} {
			// Add custom fields to each log entry
			return map[string]interface{}{
				"service": "logger-example",
				"version": "1.0.0",
			}
		},
	}
	router.Use(celeris.LoggerWithConfig(logConfig))

	// Example routes demonstrating different log scenarios
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Welcome to Logger Example",
			"note":    "Check the console for structured JSON logs",
		})
	})

	router.GET("/users", func(ctx *celeris.Context) error {
		// This will be logged with custom fields
		return ctx.JSON(200, []map[string]string{
			{"id": "1", "name": "John Doe"},
			{"id": "2", "name": "Jane Smith"},
		})
	})

	router.POST("/users", func(ctx *celeris.Context) error {
		// POST requests will show request body in logs
		return ctx.JSON(201, map[string]string{
			"message": "User created successfully",
		})
	})

	router.GET("/health", func(ctx *celeris.Context) error {
		// This endpoint is skipped from logging
		return ctx.JSON(200, map[string]string{
			"status": "healthy",
		})
	})

	router.GET("/slow", func(ctx *celeris.Context) error {
		// Simulate a slow request to see timing in logs
		time.Sleep(2 * time.Second)
		return ctx.JSON(200, map[string]string{
			"message": "This was a slow request",
		})
	})

	// Create and start server
	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)

	log.Println("Starting Logger Example Server on :8080")
	log.Println("Try these endpoints:")
	log.Println("  GET  http://localhost:8080/")
	log.Println("  GET  http://localhost:8080/users")
	log.Println("  POST http://localhost:8080/users")
	log.Println("  GET  http://localhost:8080/health (skipped from logs)")
	log.Println("  GET  http://localhost:8080/slow (slow request)")
	log.Println("")
	log.Println("All requests will be logged in structured JSON format to stdout")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
