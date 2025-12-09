// Package main provides a test server for HTTP/2 conformance testing with Celeris.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	// Register basic routes for conformance testing
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.String(200, "hello")
	})

	router.GET("/ping", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"message": "pong"})
	})

	router.GET("/users/123", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{"id": "123"})
	})

	router.POST("/echo", func(ctx *celeris.Context) error {
		// Read body to avoid flow control stall if h2spec sends data
		_, _ = ctx.BodyBytes()
		return ctx.String(200, "echo")
	})

	config := celeris.DefaultConfig()
	config.Addr = ":18081"
	// Enable HTTP/2 only for h2spec testing
	config.EnableH1 = false
	config.EnableH2 = true
	// Set concurrency limit for h2spec
	config.MaxConcurrentStreams = 100

	server := celeris.New(config)

	fmt.Println("Test server running on :18081 (HTTP/2 only)")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
}
