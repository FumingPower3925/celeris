// Package main demonstrates an HTTP/2-only Celeris server (H2C).
package main

import (
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	config := celeris.DefaultConfig()
	config.EnableH1 = false // Disable HTTP/1.1 (Only upgrade/preface allowed or H2C)
	config.EnableH2 = true

	// Note: Pure HTTP/2 often requires TLS (h2) or H2C. Celeris supports H2C by default on cleartext.

	server := celeris.New(config)

	router := celeris.NewRouter()
	router.GET("/hello", func(c *celeris.Context) error {
		return c.String(200, "Hello from HTTP/2 only!")
	})

	log.Println("Starting HTTP/2 only server on :8080")
	if err := server.ListenAndServe(router); err != nil {
		log.Printf("Server failed: %v", err)
	}
}
