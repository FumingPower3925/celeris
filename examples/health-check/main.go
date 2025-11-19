// Package main demonstrates a Celeris feature.
package main

import (
	"log"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	// Add health check middleware
	healthConfig := celeris.HealthConfig{
		Path: "/health",
		Handler: func(ctx *celeris.Context) error {
			return ctx.JSON(200, map[string]interface{}{
				"status":    "healthy",
				"timestamp": time.Now().UTC().Format(time.RFC3339),
				"uptime":    "running",
				"version":   "1.0.0",
				"services": map[string]string{
					"database": "connected",
					"cache":    "connected",
					"queue":    "connected",
				},
			})
		},
	}
	router.Use(celeris.HealthWithConfig(healthConfig))

	router.Use(celeris.Logger())

	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Health Check Example",
			"health":  "Visit /health for health status",
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)
	log.Println("Starting Health Check Example on :8080")
	log.Println("Visit http://localhost:8080/health for health status")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait indefinitely to keep the server running
	select {}
}
