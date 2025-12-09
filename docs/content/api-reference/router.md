---
title: "Router"
weight: 4
---

# Router API

The `Router` provides HTTP routing functionality with parameters, wildcards, and middleware support.

## Creating a Router

### NewRouter

```go
func NewRouter() *Router
```

Creates a new router instance.

**Example:**

```go
router := celeris.NewRouter()
```

## Route Registration

### GET

```go
func (r *Router) GET(path string, handler Handler)
```

Registers a GET route.

### POST

```go
func (r *Router) POST(path string, handler Handler)
```

Registers a POST route.

### PUT

```go
func (r *Router) PUT(path string, handler Handler)
```

Registers a PUT route.

### DELETE

```go
func (r *Router) DELETE(path string, handler Handler)
```

Registers a DELETE route.

### PATCH

```go
func (r *Router) PATCH(path string, handler Handler)
```

Registers a PATCH route.

### HEAD

```go
func (r *Router) HEAD(path string, handler Handler)
```

Registers a HEAD route.

### OPTIONS

```go
func (r *Router) OPTIONS(path string, handler Handler)
```

Registers an OPTIONS route.

### Handle

```go
func (r *Router) Handle(method, path string, handler Handler)
```

Registers a route with a custom HTTP method.

**Example:**

```go
router.GET("/users", listUsers)
router.POST("/users", createUser)
router.PUT("/users/:id", updateUser)
router.DELETE("/users/:id", deleteUser)
```

## Path Parameters

### Parameter Syntax

- **Named Parameters**: `:param` matches a single path segment
- **Wildcards**: `*param` matches the rest of the path

**Examples:**

```go
// Named parameters
router.GET("/users/:id", getUser)           // Matches: /users/123
router.GET("/posts/:id/comments/:cid", ...) // Matches: /posts/1/comments/5

// Wildcards
router.GET("/files/*path", serveFile)       // Matches: /files/doc/file.pdf
```

### Retrieving Parameters

Use the `Param` function to retrieve parameters:

```go
func getUser(ctx *celeris.Context) error {
    id := celeris.Param(ctx, "id")
    // Use id...
    return ctx.JSON(200, map[string]string{"id": id})
}
```

## Middleware

### Use

```go
func (r *Router) Use(middlewares ...Middleware)
```

Adds global middleware to the router.

**Example:**

```go
router.Use(
    celeris.Recovery(),
    celeris.Logger(),
    customMiddleware,
)
```

## Not Found Handler

### NotFound

```go
func (r *Router) NotFound(handler Handler)
```

Sets a custom handler for 404 Not Found responses.

**Example:**

```go
router.NotFound(celeris.HandlerFunc(func(ctx *celeris.Context) error {
    return ctx.JSON(404, map[string]string{
        "error": "Route not found",
    })
}))
```

## Route Groups

### Group

```go
func (r *Router) Group(prefix string, middlewares ...Middleware) *Group
```

Creates a route group with a common prefix and middleware.

**Example:**

```go
api := router.Group("/api/v1")
api.Use(authMiddleware)

api.GET("/users", listUsers)
api.POST("/users", createUser)
```

## Group Methods

### Use

```go
func (g *Group) Use(middlewares ...Middleware)
```

Adds middleware to the group.

### GET, POST, PUT, DELETE, etc.

Same methods as `Router` but with the group prefix prepended.

### Group

```go
func (g *Group) Group(prefix string, middlewares ...Middleware) *Group
```

Creates a sub-group.

**Example:**

```go
api := router.Group("/api")
v1 := api.Group("/v1")
v2 := api.Group("/v2")

v1.GET("/users", v1ListUsers)
v2.GET("/users", v2ListUsers)
```

## Helper Functions

### Param

```go
func Param(ctx *Context, name string) string
```

Retrieves a route parameter from the context.

### MustParam

```go
func MustParam(ctx *Context, name string) string
```

Retrieves a route parameter or panics if not found.

## Static Files

### Static

```go
func (r *Router) Static(prefix, root string)
```

Registers a route to serve static files from a directory.

**Parameters:**
- `prefix`: URL path prefix (e.g., "/assets")
- `root`: Local directory path (e.g., "./public")

**Example:**

```go
router.Static("/assets", "./public")
// Serves ./public/style.css at /assets/style.css
```

## Examples

### Basic Routing

```go
router := celeris.NewRouter()

router.GET("/", func(ctx *celeris.Context) error {
    return ctx.String(200, "Home")
})

router.GET("/about", func(ctx *celeris.Context) error {
    return ctx.String(200, "About")
})
```

### Parameters

```go
router.GET("/users/:id", func(ctx *celeris.Context) error {
    id := celeris.Param(ctx, "id")
    return ctx.JSON(200, map[string]string{"id": id})
})

router.GET("/users/:id/posts/:postId", func(ctx *celeris.Context) error {
    userID := celeris.Param(ctx, "id")
    postID := celeris.Param(ctx, "postId")
    return ctx.JSON(200, map[string]string{
        "user_id": userID,
        "post_id": postID,
    })
})
```

### Wildcards

```go
router.GET("/files/*path", func(ctx *celeris.Context) error {
    path := celeris.Param(ctx, "path")
    // Serve file at path...
    return ctx.String(200, "File: " + path)
})
```

### Groups

```go
router := celeris.NewRouter()
router.Use(celeris.Logger())

// Public routes
router.GET("/", homeHandler)
router.GET("/about", aboutHandler)

// API v1
api := router.Group("/api/v1")
api.Use(authMiddleware)

api.GET("/users", listUsers)
api.POST("/users", createUser)
api.GET("/users/:id", getUser)
api.PUT("/users/:id", updateUser)
api.DELETE("/users/:id", deleteUser)

// Admin routes (nested group)
admin := api.Group("/admin")
admin.Use(adminMiddleware)

admin.GET("/stats", statsHandler)
admin.POST("/config", configHandler)
```

### Custom 404 Handler

```go
router.NotFound(celeris.HandlerFunc(func(ctx *celeris.Context) error {
    return ctx.JSON(404, map[string]interface{}{
        "error":   "Not Found",
        "path":    ctx.Path(),
        "message": "The requested resource was not found",
    })
}))
```

