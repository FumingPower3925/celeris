---
title: "Basic Server"
weight: 1
---

# Basic Server Examples

## Minimal Server

The simplest possible Celeris server:

```go
package main

import (
    "log"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
    router := celeris.NewRouter()
    
    router.GET("/", func(ctx *celeris.Context) error {
        return ctx.String(200, "Hello, World!")
    })
    
    server := celeris.NewWithDefaults()
    log.Fatal(server.ListenAndServe(router))
}
```

## REST API Server

Complete REST API with CRUD operations:

```go
package main

import (
    "log"
    "sync"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

type User struct {
    ID    int    `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}

var (
    users   = make(map[int]*User)
    usersMu sync.RWMutex
    nextID  = 1
)

func main() {
    router := celeris.NewRouter()
    
    // Global middleware
    router.Use(celeris.Recovery(), celeris.Logger())
    
    // Routes
    router.GET("/users", listUsers)
    router.GET("/users/:id", getUser)
    router.POST("/users", createUser)
    router.PUT("/users/:id", updateUser)
    router.DELETE("/users/:id", deleteUser)
    
    // Start server
    server := celeris.NewWithDefaults()
    log.Println("Server starting on :8080")
    log.Fatal(server.ListenAndServe(router))
}

func listUsers(ctx *celeris.Context) error {
    usersMu.RLock()
    defer usersMu.RUnlock()
    
    userList := make([]*User, 0, len(users))
    for _, user := range users {
        userList = append(userList, user)
    }
    
    return ctx.JSON(200, userList)
}

func getUser(ctx *celeris.Context) error {
    id := celeris.MustParam(ctx, "id")
    
    usersMu.RLock()
    defer usersMu.RUnlock()
    
    // Convert id string to int (simplified)
    var userID int
    fmt.Sscanf(id, "%d", &userID)
    
    user, ok := users[userID]
    if !ok {
        return ctx.JSON(404, map[string]string{
            "error": "User not found",
        })
    }
    
    return ctx.JSON(200, user)
}

func createUser(ctx *celeris.Context) error {
    var user User
    if err := ctx.BindJSON(&user); err != nil {
        return ctx.JSON(400, map[string]string{
            "error": "Invalid JSON",
        })
    }
    
    usersMu.Lock()
    defer usersMu.Unlock()
    
    user.ID = nextID
    nextID++
    users[user.ID] = &user
    
    return ctx.JSON(201, user)
}

func updateUser(ctx *celeris.Context) error {
    id := celeris.MustParam(ctx, "id")
    var userID int
    fmt.Sscanf(id, "%d", &userID)
    
    var updatedUser User
    if err := ctx.BindJSON(&updatedUser); err != nil {
        return ctx.JSON(400, map[string]string{
            "error": "Invalid JSON",
        })
    }
    
    usersMu.Lock()
    defer usersMu.Unlock()
    
    user, ok := users[userID]
    if !ok {
        return ctx.JSON(404, map[string]string{
            "error": "User not found",
        })
    }
    
    user.Name = updatedUser.Name
    user.Email = updatedUser.Email
    
    return ctx.JSON(200, user)
}

func deleteUser(ctx *celeris.Context) error {
    id := celeris.MustParam(ctx, "id")
    var userID int
    fmt.Sscanf(id, "%d", &userID)
    
    usersMu.Lock()
    defer usersMu.Unlock()
    
    if _, ok := users[userID]; !ok {
        return ctx.JSON(404, map[string]string{
            "error": "User not found",
        })
    }
    
    delete(users, userID)
    
    return ctx.NoContent(204)
}
```

## Server with Groups

Organizing routes with groups:

```go
package main

import (
    "log"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
    router := celeris.NewRouter()
    router.Use(celeris.Recovery(), celeris.Logger())
    
    // Public routes
    router.GET("/", homeHandler)
    router.GET("/about", aboutHandler)
    
    // API v1
    v1 := router.Group("/api/v1")
    v1.Use(authMiddleware)
    
    v1.GET("/users", listUsers)
    v1.POST("/users", createUser)
    
    // API v2
    v2 := router.Group("/api/v2")
    v2.Use(authMiddleware)
    
    v2.GET("/users", listUsersV2)
    v2.POST("/users", createUserV2)
    
    // Admin routes
    admin := router.Group("/admin")
    admin.Use(authMiddleware, adminMiddleware)
    
    admin.GET("/stats", statsHandler)
    admin.POST("/config", configHandler)
    
    // Start server
    server := celeris.NewWithDefaults()
    log.Fatal(server.ListenAndServe(router))
}

func authMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        token := ctx.Header().Get("authorization")
        if token == "" {
            return ctx.JSON(401, map[string]string{
                "error": "Unauthorized",
            })
        }
        return next.ServeHTTP2(ctx)
    })
}

func adminMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        // Check admin privileges
        return next.ServeHTTP2(ctx)
    })
}

// Handler implementations...
```

## File Server

Serving static files:

```go
package main

import (
    "io/ioutil"
    "log"
    "path/filepath"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func main() {
    router := celeris.NewRouter()
    
    router.GET("/files/*path", fileHandler)
    
    server := celeris.NewWithDefaults()
    log.Fatal(server.ListenAndServe(router))
}

func fileHandler(ctx *celeris.Context) error {
    path := celeris.Param(ctx, "path")
    
    // Security: prevent directory traversal
    cleanPath := filepath.Clean(path)
    if cleanPath == ".." || cleanPath == "." {
        return ctx.String(403, "Forbidden")
    }
    
    // Read file
    fullPath := filepath.Join("./static", cleanPath)
    data, err := ioutil.ReadFile(fullPath)
    if err != nil {
        return ctx.String(404, "File not found")
    }
    
    // Determine content type
    contentType := getContentType(fullPath)
    
    return ctx.Data(200, contentType, data)
}

func getContentType(path string) string {
    ext := filepath.Ext(path)
    switch ext {
    case ".html":
        return "text/html"
    case ".css":
        return "text/css"
    case ".js":
        return "application/javascript"
    case ".json":
        return "application/json"
    case ".png":
        return "image/png"
    case ".jpg", ".jpeg":
        return "image/jpeg"
    default:
        return "application/octet-stream"
    }
}
```

## Testing Your Server

```go
package main

import (
    "testing"
    "net/http"
    "io/ioutil"
)

func TestServer(t *testing.T) {
    // Start server in background
    router := celeris.NewRouter()
    router.GET("/test", func(ctx *celeris.Context) error {
        return ctx.JSON(200, map[string]string{"status": "ok"})
    })
    
    server := celeris.New(celeris.Config{Addr: ":8081"})
    go server.ListenAndServe(router)
    
    // Give server time to start
    time.Sleep(100 * time.Millisecond)
    
    // Make HTTP/2 request
    client := &http.Client{
        Transport: &http2.Transport{
            AllowHTTP: true,
            DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
                return net.Dial(network, addr)
            },
        },
    }
    
    resp, err := client.Get("http://localhost:8081/test")
    if err != nil {
        t.Fatalf("Request failed: %v", err)
    }
    defer resp.Body.Close()
    
    body, _ := ioutil.ReadAll(resp.Body)
    if string(body) != `{"status":"ok"}` {
        t.Errorf("Unexpected response: %s", body)
    }
}
```

