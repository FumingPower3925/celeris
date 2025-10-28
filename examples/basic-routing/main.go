// Package main demonstrates a Celeris feature.
package main

import (
	"log"

	"github.com/albertbausili/celeris/pkg/celeris"
)

func main() {
	router := celeris.NewRouter()

	router.Use(celeris.Logger())

	// Basic routing examples
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"message": "Basic Routing Example",
		})
	})

	// Route with parameters
	router.GET("/users/:id", func(ctx *celeris.Context) error {
		id := celeris.Param(ctx, "id")
		return ctx.JSON(200, map[string]string{
			"user_id": id,
			"name":    "User " + id,
		})
	})

	// Route with multiple parameters
	router.GET("/users/:id/posts/:postId", func(ctx *celeris.Context) error {
		userID := celeris.Param(ctx, "id")
		postID := celeris.Param(ctx, "postId")
		return ctx.JSON(200, map[string]string{
			"user_id": userID,
			"post_id": postID,
			"title":   "Post " + postID + " by User " + userID,
		})
	})

	// Route groups
	api := router.Group("/api/v1")
	api.GET("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(200, []string{"user1", "user2"})
	})
	api.POST("/users", func(ctx *celeris.Context) error {
		return ctx.JSON(201, map[string]string{"message": "User created"})
	})

	// Query parameters
	router.GET("/search", func(ctx *celeris.Context) error {
		query := ctx.Query("q")
		limit := ctx.QueryDefault("limit", "10")
		return ctx.JSON(200, map[string]string{
			"query": query,
			"limit": limit,
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = ":8080"
	server := celeris.New(config)
	log.Println("Starting Basic Routing Example on :8080")
	log.Println("Try: /users/123, /users/123/posts/456, /api/v1/users, /search?q=test")

	if err := server.ListenAndServe(router); err != nil {
		log.Fatal(err)
	}
}
