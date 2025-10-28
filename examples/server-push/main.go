// Package main demonstrates a Celeris feature.
package main

import (
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	router.Use(celeris.Logger())

	// HTTP/2 Server Push example
	router.GET("/", func(ctx *celeris.Context) error {
		// Push critical CSS
		if err := ctx.PushPromise("/static/style.css", map[string]string{
			"content-type": "text/css",
		}); err != nil {
			log.Printf("Failed to push CSS: %v", err)
		}

		// Push critical JS
		if err := ctx.PushPromise("/static/app.js", map[string]string{
			"content-type": "application/javascript",
		}); err != nil {
			log.Printf("Failed to push JS: %v", err)
		}

		html := `
<!DOCTYPE html>
<html>
<head>
    <title>Server Push Example</title>
    <link rel="stylesheet" href="/static/style.css">
</head>
<body>
    <h1>HTTP/2 Server Push Example</h1>
    <p>Critical resources are pushed automatically!</p>
    <script src="/static/app.js"></script>
</body>
</html>`
		return ctx.HTML(200, html)
	})

	// Serve static files (simulated)
	router.GET("/static/style.css", func(ctx *celeris.Context) error {
		ctx.SetHeader("Content-Type", "text/css")
		return ctx.String(200, "body { font-family: Arial; margin: 40px; }")
	})

	router.GET("/static/app.js", func(ctx *celeris.Context) error {
		ctx.SetHeader("Content-Type", "application/javascript")
		return ctx.String(200, "console.log('Critical JS loaded!');")
	})

	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)
	log.Println("Starting Server Push Example on :8080")
	log.Println("Visit http://localhost:8080/ to see server push in action")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
