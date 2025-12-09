// Package main demonstrates a Celeris feature.
package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
	// Create a new router
	router := celeris.NewRouter()

	// Add recovery middleware to catch panics
	router.Use(celeris.Recovery())

	// Add logging to see the recovery in action
	router.Use(celeris.Logger())

	// Normal route that works fine
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Welcome to Recovery Example",
			"note":    "Try /panic to see recovery middleware in action",
		})
	})

	// Route that intentionally panics to demonstrate recovery
	router.GET("/panic", func(_ *celeris.Context) error {
		panic("This is an intentional panic to demonstrate recovery middleware")
	})

	// Route that panics randomly (50% chance)
	router.GET("/random-panic", func(ctx *celeris.Context) error {
		if rand.Float32() < 0.5 { //nolint:gosec // Intentional panic for demonstration
			panic("Random panic occurred!")
		}
		return ctx.JSON(200, map[string]string{
			"message": "No panic this time!",
		})
	})

	// Route that panics with different types of panics
	router.GET("/panic-types", func(ctx *celeris.Context) error {
		panicType := ctx.Query("type")

		switch panicType {
		case "string":
			panic("String panic")
		case "error":
			panic(fmt.Errorf("error panic"))
		case "nil":
			var nilMap map[string]string
			nilMap["key"] = "value" //nolint:staticcheck // This will panic
		case "index":
			arr := []string{"one", "two"}
			_ = arr[10] //nolint:gosec // This will panic
		default:
			panic("Default panic")
		}

		// This line will never be reached due to panics above
		return nil
	})

	// Route that demonstrates graceful error handling
	router.GET("/graceful-error", func(_ *celeris.Context) error {
		// This returns an error instead of panicking
		return celeris.NewHTTPError(400, "This is a graceful error, not a panic")
	})

	// Initialize random seed
	//nolint:staticcheck // Using global rand for simplicity in example
	rand.Seed(time.Now().UnixNano())

	// Create and start server
	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)

	log.Println("Starting Recovery Example Server on :8080")
	log.Println("Try these endpoints:")
	log.Println("  GET http://localhost:8080/")
	log.Println("  GET http://localhost:8080/panic")
	log.Println("  GET http://localhost:8080/random-panic")
	log.Println("  GET http://localhost:8080/panic-types?type=string")
	log.Println("  GET http://localhost:8080/panic-types?type=error")
	log.Println("  GET http://localhost:8080/panic-types?type=nil")
	log.Println("  GET http://localhost:8080/panic-types?type=index")
	log.Println("  GET http://localhost:8080/graceful-error")
	log.Println("")
	log.Println("The recovery middleware will catch panics and return 500 errors")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait indefinitely to keep the server running
	select {}
}
