// Package main demonstrates automatic API documentation generation with Celeris.
package main

import (
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	// Add automatic documentation middleware
	router.Use(celeris.DocsWithRouter(router))

	router.Use(celeris.Logger())

	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Auto Documentation Example",
			"docs":    "Visit /docs for automatic API documentation",
		})
	})

	router.GET("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(200, []map[string]string{
			{"id": "1", "name": "John Doe"},
			{"id": "2", "name": "Jane Smith"},
		})
	})

	router.GET("/users/:id", func(ctx *celeris.Context) error {
		id := celeris.Param(ctx, "id")
		return ctx.JSON(200, map[string]string{
			"id":   id,
			"name": "User " + id,
		})
	})

	router.POST("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(201, map[string]string{
			"message": "User created",
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)
	log.Println("Starting Auto Documentation Example on :8080")
	log.Println("Visit http://localhost:8080/docs for automatic API documentation")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait indefinitely to keep the server running
	select {}
}
