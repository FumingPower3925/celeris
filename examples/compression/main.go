// Package main demonstrates a Celeris feature.
package main

import (
	"log"
	"strings"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	// Create a new router
	router := celeris.NewRouter()

	// Add compression middleware with custom configuration
	compressConfig := celeris.CompressConfig{
		Level:         6,                                               // Compression level (1-9)
		MinSize:       1024,                                            // Minimum size to compress (1KB)
		ExcludedTypes: []string{"image/", "video/", "application/pdf"}, // Don't compress these types
	}
	router.Use(celeris.CompressWithConfig(compressConfig))

	// Add logging to see compression in action
	router.Use(celeris.Logger())

	// Route with small response (won't be compressed due to MinSize)
	router.GET("/small", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "This is a small response",
			"note":    "Too small to compress (under 1KB)",
		})
	})

	// Route with large response (will be compressed)
	router.GET("/large", func(ctx *celeris.Context) error {
		// Generate a large response
		largeData := make([]map[string]string, 100)
		for i := 0; i < 100; i++ {
			largeData[i] = map[string]string{
				"id":      string(rune(i + 1)),
				"name":    "User " + string(rune(i+1)),
				"email":   "user" + string(rune(i+1)) + "@example.com",
				"details": strings.Repeat("This is some detailed information about the user. ", 10),
			}
		}

		return ctx.JSON(200, map[string]interface{}{
			"message": "Large response with compression",
			"count":   len(largeData),
			"data":    largeData,
		})
	})

	// Route that returns HTML (will be compressed)
	router.GET("/html", func(ctx *celeris.Context) error {
		html := `
<!DOCTYPE html>
<html>
<head>
    <title>Compression Example</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .container { max-width: 800px; margin: 0 auto; }
        .highlight { background-color: #f0f0f0; padding: 20px; border-radius: 5px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Compression Example</h1>
        <div class="highlight">
            <p>This HTML response will be compressed by the middleware.</p>
            <p>Check the response headers to see the compression details.</p>
            <p>Content-Encoding: gzip or br (brotli)</p>
        </div>
        <p>Large amounts of text content are ideal for compression.</p>
        <p>` + strings.Repeat("This is repeated content to make the response larger. ", 50) + `</p>
    </div>
</body>
</html>`

		return ctx.HTML(200, html)
	})

	// Route that returns plain text (will be compressed)
	router.GET("/text", func(ctx *celeris.Context) error {
		text := "This is a plain text response that will be compressed.\n\n"
		text += strings.Repeat("This is repeated content to demonstrate compression effectiveness. ", 100)

		return ctx.Plain(200, text)
	})

	// Route that returns JSON with different content types
	router.GET("/json-large", func(ctx *celeris.Context) error {
		// Set content type explicitly
		ctx.SetHeader("Content-Type", "application/json")

		// Generate large JSON response
		largeData := make([]map[string]interface{}, 200)
		for i := 0; i < 200; i++ {
			largeData[i] = map[string]interface{}{
				"id":          i + 1,
				"name":        "Item " + string(rune(i+1)),
				"description": strings.Repeat("This is a detailed description of the item. ", 20),
				"metadata": map[string]string{
					"category": "example",
					"tags":     strings.Repeat("tag"+string(rune(i+1))+" ", 10),
				},
			}
		}

		return ctx.JSON(200, map[string]interface{}{
			"message": "Large JSON response",
			"count":   len(largeData),
			"items":   largeData,
		})
	})

	// Route that demonstrates compression headers
	router.GET("/headers", func(ctx *celeris.Context) error {
		// This will show compression headers in the response
		return ctx.JSON(200, map[string]interface{}{
			"message": "Check response headers for compression info",
			"headers": map[string]string{
				"Content-Encoding": "gzip or br",
				"Vary":             "Accept-Encoding",
			},
			"note": "Large responses are automatically compressed",
		})
	})

	// Create and start server
	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)

	log.Println("Starting Compression Example Server on :8080")
	log.Println("Try these endpoints:")
	log.Println("  GET http://localhost:8080/small      # Small response (no compression)")
	log.Println("  GET http://localhost:8080/large      # Large response (compressed)")
	log.Println("  GET http://localhost:8080/html       # HTML response (compressed)")
	log.Println("  GET http://localhost:8080/text       # Plain text (compressed)")
	log.Println("  GET http://localhost:8080/json-large # Large JSON (compressed)")
	log.Println("  GET http://localhost:8080/headers    # Shows compression headers")
	log.Println("")
	log.Println("Use curl with -H 'Accept-Encoding: gzip, br' to see compression")
	log.Println("Check response headers for Content-Encoding and Vary")

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(router); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait indefinitely to keep the server running
	select {}
}
