// Package main provides a basic example of using the Celeris HTTP/2 server.
package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	// Create a new router instance
	router := celeris.NewRouter()

	// Check if minimal mode is enabled for benchmarking
	minimal := os.Getenv("EXAMPLE_MINIMAL") == "1"
	if !minimal {
		// Add global middleware for recovery and logging
		router.Use(
			celeris.Recovery(),
			celeris.Logger(),
		)
	}

	// Register routes
	router.GET("/", homeHandler)
	router.GET("/hello/:name", helloHandler)
	router.POST("/api/data", dataHandler)
	router.GET("/json", jsonHandler)
	// Route with multiple parameters mirroring benchmark paths
	router.GET("/user/:userId/post/:postId", paramsHandler)
	// Simple parameter route for quick wrk tests
	router.GET("/user/:id", userParamHandler)

	// Create API route group
	api := router.Group("/api/v1")
	api.GET("/users", usersHandler)
	api.GET("/users/:id", userHandler)
	api.POST("/users", createUserHandler)

	// Configure server settings
	config := celeris.DefaultConfig()
	addr := os.Getenv("EXAMPLE_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	config.Addr = addr

	// Optimize configuration for minimal mode
	if minimal {
		// Disable logging and optimize event loops for maximum throughput
		config.Logger = log.New(io.Discard, "", 0)
		cpus := runtime.GOMAXPROCS(0)
		switch {
		case cpus <= 2:
			config.NumEventLoop = cpus
		case cpus <= 8:
			config.NumEventLoop = cpus - 1
		default:
			config.NumEventLoop = cpus - 2
		}
		config.Multicore = true
		config.ReusePort = true
	}
	config.Multicore = true

	// Create server instance
	server := celeris.New(config)

	// Start server in a separate goroutine
	go func() {
		log.Printf("Starting server on %s", config.Addr)
		log.Printf("MaxConnections: %d, MaxConcurrentStreams: %d", config.MaxConnections, config.MaxConcurrentStreams)
		if err := server.ListenAndServe(router); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
}

// homeHandler serves the home page with example endpoints information
func homeHandler(ctx *celeris.Context) error {
	return ctx.HTML(200, `
<!DOCTYPE html>
<html>
<head>
    <title>Celeris HTTP/2 Server</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
        h1 { color: #333; }
        .info { background: #f4f4f4; padding: 15px; border-radius: 5px; margin: 20px 0; }
        code { background: #eee; padding: 2px 6px; border-radius: 3px; }
    </style>
</head>
<body>
    <h1>Welcome to Celeris</h1>
    <p>A high-performance HTTP/2-only server built with gnet</p>
    <div class="info">
        <h2>Try these endpoints:</h2>
        <ul>
            <li><code>GET /hello/:name</code> - Say hello</li>
            <li><code>GET /json</code> - Get JSON response</li>
            <li><code>POST /api/data</code> - Send data</li>
            <li><code>GET /api/v1/users</code> - List users</li>
        </ul>
    </div>
</body>
</html>
`)
}

// helloHandler returns a personalized greeting in JSON format
func helloHandler(ctx *celeris.Context) error {
	name := celeris.Param(ctx, "name")
	return ctx.JSON(200, map[string]string{
		"message": "Hello, " + name + "!",
		"method":  ctx.Method(),
		"path":    ctx.Path(),
	})
}

// dataHandler processes POST requests with JSON data
func dataHandler(ctx *celeris.Context) error {
	var data map[string]interface{}
	if err := ctx.BindJSON(&data); err != nil {
		return ctx.JSON(400, map[string]string{
			"error": "Invalid JSON",
		})
	}

	return ctx.JSON(200, map[string]interface{}{
		"received": data,
		"status":   "success",
	})
}

// jsonHandler returns server status information in JSON format
func jsonHandler(ctx *celeris.Context) error {
	return ctx.JSON(200, map[string]interface{}{
		"server":  "celeris",
		"version": "0.1.0",
		"status":  "running",
	})
}

// paramsHandler extracts and returns route parameters in JSON format
func paramsHandler(ctx *celeris.Context) error {
	return ctx.JSON(200, map[string]string{
		"userId": celeris.Param(ctx, "userId"),
		"postId": celeris.Param(ctx, "postId"),
	})
}

// userParamHandler extracts and returns a single user ID parameter
func userParamHandler(ctx *celeris.Context) error {
	return ctx.JSON(200, map[string]string{
		"id": celeris.Param(ctx, "id"),
	})
}

// usersHandler returns a list of example users
func usersHandler(ctx *celeris.Context) error {
	users := []map[string]interface{}{
		{"id": 1, "name": "Alice", "email": "alice@example.com"},
		{"id": 2, "name": "Bob", "email": "bob@example.com"},
		{"id": 3, "name": "Charlie", "email": "charlie@example.com"},
	}

	return ctx.JSON(200, map[string]interface{}{
		"users": users,
		"total": len(users),
	})
}

// userHandler returns information for a specific user by ID
func userHandler(ctx *celeris.Context) error {
	id := celeris.Param(ctx, "id")

	return ctx.JSON(200, map[string]interface{}{
		"id":    id,
		"name":  "User " + id,
		"email": "user" + id + "@example.com",
	})
}

// createUserHandler simulates creation of a new user
func createUserHandler(ctx *celeris.Context) error {
	var user map[string]interface{}
	if err := ctx.BindJSON(&user); err != nil {
		return ctx.JSON(400, map[string]string{
			"error": "Invalid JSON",
		})
	}

	// Simulate user creation
	user["id"] = 4

	return ctx.JSON(201, map[string]interface{}{
		"user":    user,
		"message": "User created successfully",
	})
}
