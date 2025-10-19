---
title: "Middleware Examples"
weight: 2
---

# Middleware Examples

Complete middleware implementations for common scenarios.

## Authentication Middleware

JWT-style authentication:

```go
package main

import (
    "strings"
    "github.com/albertbausili/celeris/pkg/celeris"
)

func JWTAuth(secretKey string) celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            authHeader := ctx.Header().Get("authorization")
            
            if authHeader == "" {
                return ctx.JSON(401, map[string]string{
                    "error": "Missing authorization header",
                })
            }
            
            // Extract token
            parts := strings.Split(authHeader, " ")
            if len(parts) != 2 || parts[0] != "Bearer" {
                return ctx.JSON(401, map[string]string{
                    "error": "Invalid authorization header format",
                })
            }
            
            token := parts[1]
            
            // Validate token (simplified)
            userID, err := validateJWT(token, secretKey)
            if err != nil {
                return ctx.JSON(401, map[string]string{
                    "error": "Invalid token",
                })
            }
            
            // Store user ID in context
            ctx.Set("user-id", userID)
            
            return next.ServeHTTP2(ctx)
        })
    }
}

func validateJWT(token, secretKey string) (string, error) {
    // Implement JWT validation
    // This is a placeholder
    return "user123", nil
}

// Usage
func main() {
    router := celeris.NewRouter()
    
    // Public routes
    router.POST("/login", loginHandler)
    
    // Protected routes
    api := router.Group("/api")
    api.Use(JWTAuth("your-secret-key"))
    
    api.GET("/profile", profileHandler)
}
```

## Rate Limiting

Per-IP rate limiting with sliding window:

```go
package main

import (
    "sync"
    "time"
    "github.com/albertbausili/celeris/pkg/celeris"
)

type RateLimiter struct {
    requests map[string]*requestLog
    mu       sync.Mutex
    limit    int
    window   time.Duration
}

type requestLog struct {
    timestamps []time.Time
    mu         sync.Mutex
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
    return &RateLimiter{
        requests: make(map[string]*requestLog),
        limit:    limit,
        window:   window,
    }
}

func (rl *RateLimiter) Middleware() celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            // Get client identifier (IP)
            ip := ctx.Header().Get("x-real-ip")
            if ip == "" {
                ip = ctx.Header().Get("x-forwarded-for")
            }
            if ip == "" {
                ip = "unknown"
            }
            
            // Get or create request log
            rl.mu.Lock()
            log, ok := rl.requests[ip]
            if !ok {
                log = &requestLog{
                    timestamps: make([]time.Time, 0),
                }
                rl.requests[ip] = log
            }
            rl.mu.Unlock()
            
            // Check rate limit
            log.mu.Lock()
            defer log.mu.Unlock()
            
            now := time.Now()
            
            // Remove old timestamps
            var validTimestamps []time.Time
            for _, t := range log.timestamps {
                if now.Sub(t) < rl.window {
                    validTimestamps = append(validTimestamps, t)
                }
            }
            log.timestamps = validTimestamps
            
            // Check if limit exceeded
            if len(log.timestamps) >= rl.limit {
                ctx.SetHeader("X-RateLimit-Limit", fmt.Sprintf("%d", rl.limit))
                ctx.SetHeader("X-RateLimit-Remaining", "0")
                ctx.SetHeader("X-RateLimit-Reset", fmt.Sprintf("%d", now.Add(rl.window).Unix()))
                
                return ctx.JSON(429, map[string]string{
                    "error": "Rate limit exceeded",
                })
            }
            
            // Add current request
            log.timestamps = append(log.timestamps, now)
            
            // Set rate limit headers
            remaining := rl.limit - len(log.timestamps)
            ctx.SetHeader("X-RateLimit-Limit", fmt.Sprintf("%d", rl.limit))
            ctx.SetHeader("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
            
            return next.ServeHTTP2(ctx)
        })
    }
}

// Usage: 100 requests per minute
func main() {
    router := celeris.NewRouter()
    
    limiter := NewRateLimiter(100, time.Minute)
    router.Use(limiter.Middleware())
    
    router.GET("/api/data", dataHandler)
}
```

## Request Validation

Validate request data:

```go
package main

import (
    "github.com/albertbausili/celeris/pkg/celeris"
)

type Validator interface {
    Validate() error
}

func ValidateJSON() celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            contentType := ctx.Header().Get("content-type")
            
            if ctx.Method() == "POST" || ctx.Method() == "PUT" {
                if contentType != "application/json" {
                    return ctx.JSON(400, map[string]string{
                        "error": "Content-Type must be application/json",
                    })
                }
            }
            
            return next.ServeHTTP2(ctx)
        })
    }
}

// Usage
func main() {
    router := celeris.NewRouter()
    
    api := router.Group("/api")
    api.Use(ValidateJSON())
    
    api.POST("/users", createUserHandler)
}

type CreateUserRequest struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func (r *CreateUserRequest) Validate() error {
    if r.Name == "" {
        return fmt.Errorf("name is required")
    }
    if r.Email == "" {
        return fmt.Errorf("email is required")
    }
    if !strings.Contains(r.Email, "@") {
        return fmt.Errorf("invalid email format")
    }
    return nil
}

func createUserHandler(ctx *celeris.Context) error {
    var req CreateUserRequest
    if err := ctx.BindJSON(&req); err != nil {
        return ctx.JSON(400, map[string]string{
            "error": "Invalid JSON",
        })
    }
    
    if err := req.Validate(); err != nil {
        return ctx.JSON(400, map[string]string{
            "error": err.Error(),
        })
    }
    
    // Create user...
    return ctx.JSON(201, req)
}
```

## Logging with Structured Logs

Structured logging middleware:

```go
package main

import (
    "log/slog"
    "time"
    "github.com/albertbausili/celeris/pkg/celeris"
)

func StructuredLogger(logger *slog.Logger) celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            start := time.Now()
            
            // Get request ID if exists
            reqID, _ := ctx.Get("request-id")
            
            // Call handler
            err := next.ServeHTTP2(ctx)
            
            // Log request
            duration := time.Since(start)
            
            logger.Info("http request",
                slog.String("method", ctx.Method()),
                slog.String("path", ctx.Path()),
                slog.Int("status", ctx.statusCode),
                slog.Duration("duration", duration),
                slog.Any("request_id", reqID),
            )
            
            if err != nil {
                logger.Error("request error",
                    slog.String("error", err.Error()),
                    slog.Any("request_id", reqID),
                )
            }
            
            return err
        })
    }
}

// Usage
func main() {
    logger := slog.Default()
    
    router := celeris.NewRouter()
    router.Use(
        celeris.RequestID(),
        StructuredLogger(logger),
    )
}
```

## CORS with Credentials

Advanced CORS configuration:

```go
package main

import (
    "github.com/albertbausili/celeris/pkg/celeris"
)

func CustomCORS() celeris.Middleware {
    allowedOrigins := map[string]bool{
        "https://example.com":     true,
        "https://app.example.com": true,
    }
    
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            origin := ctx.Header().Get("origin")
            
            // Check if origin is allowed
            if allowedOrigins[origin] {
                ctx.SetHeader("Access-Control-Allow-Origin", origin)
                ctx.SetHeader("Access-Control-Allow-Credentials", "true")
            }
            
            ctx.SetHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
            ctx.SetHeader("Access-Control-Allow-Headers", "Content-Type, Authorization")
            ctx.SetHeader("Access-Control-Max-Age", "3600")
            
            // Handle preflight
            if ctx.Method() == "OPTIONS" {
                return ctx.NoContent(204)
            }
            
            return next.ServeHTTP2(ctx)
        })
    }
}

// Usage
func main() {
    router := celeris.NewRouter()
    router.Use(CustomCORS())
}
```

## Request Timeout

Per-route timeout:

```go
package main

import (
    "context"
    "time"
    "github.com/albertbausili/celeris/pkg/celeris"
)

func TimeoutMiddleware(timeout time.Duration) celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            // Create timeout context
            timeoutCtx, cancel := context.WithTimeout(ctx.Context(), timeout)
            defer cancel()
            
            // Channel for handler result
            done := make(chan error, 1)
            
            // Run handler in goroutine
            go func() {
                done <- next.ServeHTTP2(ctx)
            }()
            
            // Wait for completion or timeout
            select {
            case err := <-done:
                return err
            case <-timeoutCtx.Done():
                return ctx.JSON(504, map[string]string{
                    "error": "Request timeout",
                })
            }
        })
    }
}

// Usage
func main() {
    router := celeris.NewRouter()
    
    // Fast endpoints
    api := router.Group("/api")
    api.Use(TimeoutMiddleware(5 * time.Second))
    
    // Slow endpoints
    slow := router.Group("/slow")
    slow.Use(TimeoutMiddleware(30 * time.Second))
}
```

