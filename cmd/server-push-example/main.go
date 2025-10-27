// Package main demonstrates HTTP/2 server push functionality with Celeris.
package main

import (
	"fmt"
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// main demonstrates HTTP/2 Server Push functionality with Celeris.
func main() {
	router := celeris.NewRouter()

	// Add middleware for logging requests
	router.Use(celeris.Logger())

	// Homepage demonstrating server push for critical resources
	router.GET("/", func(ctx *celeris.Context) error {
		// Push critical CSS to allow browser to start downloading immediately
		err := ctx.PushPromise("/static/style.css", map[string]string{
			"content-type": "text/css",
		})
		if err != nil {
			log.Printf("Push failed for style.css: %v", err)
		}

		// Push critical JavaScript
		err = ctx.PushPromise("/static/app.js", map[string]string{
			"content-type": "application/javascript",
		})
		if err != nil {
			log.Printf("Push failed for app.js: %v", err)
		}

		// Push logo image as a critical resource
		err = ctx.PushPromise("/static/logo.png", map[string]string{
			"content-type": "image/png",
		})
		if err != nil {
			log.Printf("Push failed for logo.png: %v", err)
		}

		// Send HTML response with embedded resources references
		html := `<!DOCTYPE html>
<html>
<head>
    <title>Celeris Server Push Example</title>
    <link rel="stylesheet" href="/static/style.css">
</head>
<body>
    <img src="/static/logo.png" alt="Logo">
    <h1>Welcome to Celeris!</h1>
    <p>This page demonstrates HTTP/2 Server Push.</p>
    <p>The CSS, JavaScript, and logo were pushed before you requested them!</p>
    <script src="/static/app.js"></script>
</body>
</html>`
		return ctx.HTML(200, html)
	})

	// Serve the pushed CSS file with caching headers
	router.GET("/static/style.css", func(ctx *celeris.Context) error {
		css := `
body {
    font-family: Arial, sans-serif;
    margin: 40px;
    background-color: #f0f0f0;
}

h1 {
    color: #333;
}

img {
    max-width: 200px;
    margin-bottom: 20px;
}
`
		ctx.SetHeader("content-type", "text/css")
		ctx.SetHeader("cache-control", "max-age=3600")
		return ctx.String(200, "%s", css)
	})

	// Serve the pushed JavaScript file with caching headers
	router.GET("/static/app.js", func(ctx *celeris.Context) error {
		js := `
console.log('Celeris Server Push Example');
console.log('This JavaScript was pushed by the server!');

document.addEventListener('DOMContentLoaded', function() {
    console.log('Page loaded with Server Push');
});
`
		ctx.SetHeader("content-type", "application/javascript")
		ctx.SetHeader("cache-control", "max-age=3600")
		return ctx.String(200, "%s", js)
	})

	// Serve the pushed logo image (placeholder in this example)
	router.GET("/static/logo.png", func(ctx *celeris.Context) error {
		ctx.SetHeader("content-type", "image/png")
		ctx.SetHeader("cache-control", "max-age=3600")
		// In a real app, you would return actual image bytes
		return ctx.String(200, "PNG_IMAGE_DATA_HERE")
	})

	// Dashboard route with conditional server push
	router.GET("/dashboard", func(ctx *celeris.Context) error {
		// Only push if client supports it
		resources := []string{
			"/static/dashboard.css",
			"/static/dashboard.js",
			"/api/user/data.json",
		}

		for _, resource := range resources {
			if err := ctx.PushPromise(resource, nil); err != nil {
				// Client might not support push or it's disabled
				// Log the error but continue as client will request normally
				log.Printf("Push not available for %s: %v", resource, err)
			}
		}

		return ctx.HTML(200, "<html><body><h1>Dashboard</h1></body></html>")
	})

	// Configure and start server
	config := celeris.DefaultConfig()
	config.Addr = ":8080"

	server := celeris.New(config)

	fmt.Println("🚀 Celeris Server Push Example")
	fmt.Println("📡 Server listening on :8080")
	fmt.Println("🌐 Open: http://localhost:8080")
	fmt.Println("💡 Use Chrome DevTools Network tab to see Server Push in action!")
	fmt.Println("   Look for 'Push' in the Initiator column")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
