// Package main demonstrates a Celeris feature.
package main

import (
	"log"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	// Create a new router
	router := celeris.NewRouter()

	// Add rate limiting middleware (using default KeyFunc)
	router.Use(celeris.RateLimiter(1)) // 1 request per second for testing

	// Add logging to see rate limiting in action
	router.Use(celeris.Logger())

	// Normal route
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Welcome to Rate Limiting Example",
			"note":    "Try making multiple requests quickly to see rate limiting",
		})
	})

	// Route that's not rate limited
	router.GET("/health", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"status": "healthy",
			"note":   "This endpoint is not rate limited",
		})
	})

	// Route that simulates processing time
	router.GET("/slow", func(ctx *celeris.Context) error {
		time.Sleep(500 * time.Millisecond)
		return ctx.JSON(200, map[string]string{
			"message": "Slow response completed",
		})
	})

	// Route that shows rate limit headers
	router.GET("/headers", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]interface{}{
			"message": "Check response headers for rate limit info",
			"headers": map[string]string{
				"X-RateLimit-Limit":     "5",
				"X-RateLimit-Remaining": "varies",
				"X-RateLimit-Reset":     "timestamp",
			},
		})
	})

	// Create and start server
	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)

	log.Println("Starting Rate Limiting Example Server on :8080")
	log.Println("Rate limit: 1 request per second, burst size: 2")
	log.Println("Try these endpoints:")
	log.Println("  GET http://localhost:8080/")
	log.Println("  GET http://localhost:8080/health (not rate limited)")
	log.Println("  GET http://localhost:8080/slow")
	log.Println("  GET http://localhost:8080/headers")
	log.Println("")
	log.Println("Make multiple requests quickly to see rate limiting in action")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
