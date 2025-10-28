// Package main demonstrates a Celeris feature.
package main

import (
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	router.Use(celeris.Logger())

	// CORS middleware
	router.Use(celeris.CORS(celeris.DefaultCORSConfig()))

	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "CORS Example",
			"note":    "This API supports cross-origin requests",
		})
	})

	router.POST("/data", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "POST request successful",
		})
	})

	router.OPTIONS("/data", func(ctx *celeris.Context) error {
		return ctx.NoContent(204)
	})

	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)
	log.Println("Starting CORS Example on :8080")
	log.Println("Test with: curl -H 'Origin: http://localhost:3000' http://localhost:8080/")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
