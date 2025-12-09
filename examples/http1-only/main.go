// Package main demonstrates an HTTP/1.1-only Celeris server.
package main

import (
	"log"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
	config := celeris.DefaultConfig()
	config.EnableH1 = true
	config.EnableH2 = false // Disable HTTP/2

	server := celeris.New(config)

	router := celeris.NewRouter()
	router.GET("/hello", func(c *celeris.Context) error {
		return c.String(200, "Hello from HTTP/1.1 only!")
	})

	log.Println("Starting HTTP/1.1 only server on :8080")
	if err := server.ListenAndServe(router); err != nil {
		log.Printf("Server failed: %v", err)
	}
}
