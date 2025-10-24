// Package main provides a test server for HTTP/2 conformance testing with Celeris.
package main

import (
	"fmt"
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	// Basic routes for testing
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
		return ctx.String(200, "echo")
	})

	config := celeris.DefaultConfig()
	config.Addr = ":18080"
	// Set concurrency limit for h2spec
	config.MaxConcurrentStreams = 100

	server := celeris.New(config)

	fmt.Println("Test server running on :18080")
	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
