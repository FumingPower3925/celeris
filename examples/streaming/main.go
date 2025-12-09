// Package main demonstrates a Celeris feature.
package main

import (
	"fmt"
	"io"
	"log"
	"time"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	router.Use(celeris.Logger())

	// Server-Sent Events example
	router.GET("/events", func(ctx *celeris.Context) error {
		// Set SSE headers
		ctx.SetHeader("Content-Type", "text/event-stream")
		ctx.SetHeader("Cache-Control", "no-cache")
		ctx.SetHeader("Connection", "keep-alive")

		// Send events every second
		for i := 0; i < 10; i++ {
			event := celeris.SSEEvent{
				Event: "message",
				Data:  fmt.Sprintf(`{"id": %d, "message": "Hello from SSE!", "time": "%s"}`, i+1, time.Now().Format(time.RFC3339)),
			}

			if err := ctx.SSE(event); err != nil {
				return err
			}

			// time.Sleep(1 * time.Second) // Removed to avoid blocking gnet loop
		}

		return nil
	})

	// Streaming response example
	router.GET("/stream", func(ctx *celeris.Context) error {
		ctx.SetHeader("Content-Type", "text/plain")
		ctx.SetHeader("Transfer-Encoding", "chunked")

		return ctx.Stream(func(w io.Writer) error {
			for i := 0; i < 5; i++ {
				_, err := fmt.Fprintf(w, "Chunk %d: Streaming data...\n", i+1)
				if err != nil {
					return err
				}
				// time.Sleep(500 * time.Millisecond) // Removed to avoid blocking gnet loop
			}
			return nil
		})
	})

	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Streaming Example",
			"events":  "Visit /events for Server-Sent Events",
			"stream":  "Visit /stream for streaming response",
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)
	log.Println("Starting Streaming Example on :8080")
	log.Println("Visit http://localhost:8080/events for SSE")
	log.Println("Visit http://localhost:8080/stream for streaming")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait indefinitely to keep the server running
	select {}
}
