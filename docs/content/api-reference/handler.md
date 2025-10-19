---
title: "Handler"
weight: 2
---

# Handler API

Handlers process HTTP/2 requests in Celeris.

## Handler Interface

```go
type Handler interface {
    ServeHTTP2(ctx *Context) error
}
```

Any type that implements this interface can handle requests.

## HandlerFunc

```go
type HandlerFunc func(ctx *Context) error
```

`HandlerFunc` is an adapter that allows functions to be used as handlers.

**Example:**

```go
func myHandler(ctx *celeris.Context) error {
    return ctx.String(200, "Hello!")
}

router.GET("/", celeris.HandlerFunc(myHandler))

// Or more commonly, routers accept functions directly:
router.GET("/", myHandler)
```

## Middleware

### Middleware Type

```go
type Middleware func(Handler) Handler
```

Middleware wraps a handler to add functionality.

### Chain

```go
func Chain(middlewares ...Middleware) Middleware
```

Chains multiple middleware together.

**Example:**

```go
combined := celeris.Chain(
    celeris.Recovery(),
    celeris.Logger(),
    authMiddleware,
)

router.Use(combined)
```

## Built-in Middleware

### Recovery

```go
func Recovery() Middleware
```

Recovers from panics and returns a 500 error.

**Example:**

```go
router.Use(celeris.Recovery())
```

### Logger

```go
func Logger() Middleware
```

Logs request details (method, path, status, duration).

**Example:**

```go
router.Use(celeris.Logger())
```

### CORS

```go
func CORS(config CORSConfig) Middleware
```

Adds CORS headers to responses.

**Example:**

```go
router.Use(celeris.CORS(celeris.DefaultCORSConfig()))

// Or custom:
router.Use(celeris.CORS(celeris.CORSConfig{
    AllowOrigin:      "https://example.com",
    AllowMethods:     "GET, POST",
    AllowHeaders:     "Content-Type",
    AllowCredentials: true,
    MaxAge:           3600,
}))
```

### RequestID

```go
func RequestID() Middleware
```

Adds a unique request ID to each request.

**Example:**

```go
router.Use(celeris.RequestID())

func handler(ctx *celeris.Context) error {
    reqID, _ := ctx.Get("request-id")
    // Use request ID...
}
```

### Timeout

```go
func Timeout(duration time.Duration) Middleware
```

Enforces a timeout on request processing.

**Example:**

```go
router.Use(celeris.Timeout(5 * time.Second))
```

## Creating Custom Middleware

### Basic Middleware

```go
func MyMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        // Before handler
        start := time.Now()
        
        // Call next handler
        err := next.ServeHTTP2(ctx)
        
        // After handler
        duration := time.Since(start)
        log.Printf("Request took %v", duration)
        
        return err
    })
}

// Usage
router.Use(MyMiddleware)
```

### Middleware with Configuration

```go
func AuthMiddleware(apiKey string) celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            key := ctx.Header().Get("x-api-key")
            if key != apiKey {
                return ctx.JSON(401, map[string]string{
                    "error": "Unauthorized",
                })
            }
            return next.ServeHTTP2(ctx)
        })
    }
}

// Usage
router.Use(AuthMiddleware("secret-key"))
```

### Conditional Middleware

```go
func ConditionalMiddleware(condition func(*celeris.Context) bool, mw celeris.Middleware) celeris.Middleware {
    return func(next celeris.Handler) celeris.Handler {
        wrapped := mw(next)
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            if condition(ctx) {
                return wrapped.ServeHTTP2(ctx)
            }
            return next.ServeHTTP2(ctx)
        })
    }
}

// Usage
isAPI := func(ctx *celeris.Context) bool {
    return strings.HasPrefix(ctx.Path(), "/api")
}

router.Use(ConditionalMiddleware(isAPI, authMiddleware))
```

## Error Handling

Handlers return errors. You can handle them in middleware:

```go
func ErrorHandler(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        err := next.ServeHTTP2(ctx)
        if err != nil {
            // Log error
            log.Printf("Error: %v", err)
            
            // Return error response
            return ctx.JSON(500, map[string]string{
                "error": err.Error(),
            })
        }
        return nil
    })
}
```

## Examples

### Logging Middleware

```go
func LoggingMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        start := time.Now()
        
        // Process request
        err := next.ServeHTTP2(ctx)
        
        // Log
        log.Printf("[%s] %s %s - %d (%v)",
            start.Format("2006/01/02 15:04:05"),
            ctx.Method(),
            ctx.Path(),
            ctx.statusCode,
            time.Since(start),
        )
        
        return err
    })
}
```

### Authentication Middleware

```go
func AuthMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        token := ctx.Header().Get("authorization")
        
        if token == "" {
            return ctx.JSON(401, map[string]string{
                "error": "Missing authorization header",
            })
        }
        
        // Validate token
        userID, err := validateToken(token)
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
```

### Rate Limiting Middleware

```go
type rateLimiter struct {
    requests map[string][]time.Time
    mu       sync.Mutex
    limit    int
    window   time.Duration
}

func RateLimitMiddleware(limit int, window time.Duration) celeris.Middleware {
    rl := &rateLimiter{
        requests: make(map[string][]time.Time),
        limit:    limit,
        window:   window,
    }
    
    return func(next celeris.Handler) celeris.Handler {
        return celeris.HandlerFunc(func(ctx *celeris.Context) error {
            ip := ctx.Header().Get("x-forwarded-for")
            if ip == "" {
                ip = "unknown"
            }
            
            rl.mu.Lock()
            defer rl.mu.Unlock()
            
            now := time.Now()
            requests := rl.requests[ip]
            
            // Remove old requests
            var validRequests []time.Time
            for _, t := range requests {
                if now.Sub(t) < rl.window {
                    validRequests = append(validRequests, t)
                }
            }
            
            // Check limit
            if len(validRequests) >= rl.limit {
                return ctx.JSON(429, map[string]string{
                    "error": "Rate limit exceeded",
                })
            }
            
            // Add current request
            validRequests = append(validRequests, now)
            rl.requests[ip] = validRequests
            
            return next.ServeHTTP2(ctx)
        })
    }
}

// Usage: 100 requests per minute
router.Use(RateLimitMiddleware(100, time.Minute))
```

### Compression Middleware (Placeholder)

```go
func CompressionMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        // Check if client accepts compression
        acceptEncoding := ctx.Header().Get("accept-encoding")
        
        // Process request
        err := next.ServeHTTP2(ctx)
        
        // TODO: Compress response if supported
        _ = acceptEncoding
        
        return err
    })
}
```

