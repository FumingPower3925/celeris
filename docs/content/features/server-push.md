---
weight: 31
title: "Server Push"
---

# Server Push

Celeris supports HTTP/2 Server Push, allowing you to proactively send resources to clients before they request them.

## Overview

Server Push is an HTTP/2 feature that enables the server to send resources to the client without waiting for explicit requests. This can significantly improve page load times by reducing round-trip latency.

## Basic Usage

Use the `ctx.PushPromise()` method to push resources:

```go
router.GET("/", func(ctx *celeris.Context) error {
    // Push CSS before sending HTML
    err := ctx.PushPromise("/style.css", map[string]string{
        "content-type": "text/css",
    })
    if err != nil {
        // Push failed or not supported - not a fatal error
        log.Printf("Push failed: %v", err)
    }
    
    // Push JavaScript
    ctx.PushPromise("/app.js", map[string]string{
        "content-type": "application/javascript",
    })
    
    // Return the main HTML
    return ctx.HTML(200, "<html>...</html>")
})
```

## How It Works

### PUSH_PROMISE Frame

When you call `PushPromise()`, Celeris:
1. Generates a new stream ID (server-initiated streams are even-numbered)
2. Encodes headers using HPACK
3. Sends a PUSH_PROMISE frame to the client
4. Creates the promised stream in half-closed state

### Fulfilling the Promise

After sending the PUSH_PROMISE, you need to handle the pushed resource:

```go
router.GET("/", func(ctx *celeris.Context) error {
    // Promise to push CSS
    ctx.PushPromise("/style.css", nil)
    
    // The handler for /style.css will be called automatically
    return ctx.HTML(200, htmlContent)
})

router.GET("/style.css", func(ctx *celeris.Context) error {
    // This handles both direct requests AND pushed promises
    return ctx.CSS(200, cssContent)
})
```

## Advanced Usage

### Conditional Push

Check if push is supported before attempting:

```go
router.GET("/", func(ctx *celeris.Context) error {
    // Attempt push, but don't fail if unsupported
    if err := ctx.PushPromise("/critical.css", nil); err != nil {
        // Client might not support push, or push is disabled
        // Continue normally - client will request the resource
    }
    
    return ctx.HTML(200, htmlContent)
})
```

### Push Multiple Resources

Push multiple critical resources:

```go
router.GET("/page", func(ctx *celeris.Context) error {
    resources := []string{
        "/css/main.css",
        "/css/theme.css",
        "/js/runtime.js",
        "/js/vendor.js",
        "/js/app.js",
        "/images/logo.png",
    }
    
    for _, resource := range resources {
        ctx.PushPromise(resource, nil)
    }
    
    return ctx.HTML(200, pageHTML)
})
```

### Custom Headers

Specify custom headers for pushed resources:

```go
ctx.PushPromise("/api/data.json", map[string]string{
    "content-type":  "application/json",
    "cache-control": "max-age=3600",
    "vary":          "Accept-Encoding",
})
```

## Pseudo-Headers

Celeris automatically adds required pseudo-headers:
- `:method` - Always "GET" for pushed resources
- `:path` - The path you specify
- `:scheme` - Inherited from the original request

You only need to provide regular headers.

## Best Practices

### Do Push

- **Critical Resources**: CSS and JavaScript required for initial render
- **Known Dependencies**: Resources you know the page will need
- **Small Resources**: Fonts, icons, small images

### Don't Push

- **Large Resources**: Videos, large images (let client request them)
- **User-Specific Content**: Content that varies by user state
- **Cacheable Resources**: Resources the client likely has cached
- **Everything**: Over-pushing wastes bandwidth

### Example: Optimal Push Strategy

```go
router.GET("/", func(ctx *celeris.Context) error {
    // Push critical path resources
    ctx.PushPromise("/css/critical.css", nil)
    ctx.PushPromise("/js/vendor.js", nil)
    
    // Don't push:
    // - Large images (let browser request them)
    // - Below-the-fold CSS (not critical)
    // - Analytics scripts (not critical)
    
    return ctx.HTML(200, htmlContent)
})
```

## Client Behavior

### Accepting Pushes

Clients can:
- Accept the pushed resource and use it
- Cancel the push with RST_STREAM
- Ignore it if already cached

### Cache Awareness

Celeris doesn't track client cache state. It's the client's responsibility to:
- Cancel pushes for cached resources
- Accept pushes for resources it needs

## Performance Considerations

### Bandwidth

Server Push uses bandwidth for every resource pushed. If the client has the resource cached, this is wasted bandwidth.

### Connection Limits

Pushed streams count toward the connection's concurrent stream limit. Don't push so many resources that you block regular requests.

### Measurement

Monitor push effectiveness:
- Track push acceptance rate
- Measure page load time improvements
- Watch for wasted pushes

## Configuration

### Enable/Disable

Server push is enabled by default. To disable:

```go
// This is handled internally by the stream manager
// Push can be disabled per-connection based on client settings
```

### Stream Limits

Pushed streams are subject to the same limits as regular streams:
- Maximum concurrent streams
- Flow control windows
- Connection-level limits

## Debugging

### Logging

Enable debug logging to see push activity:

```go
// Celeris logs push promises at debug level
log.Printf("PUSH_PROMISE: stream %d -> path %s", streamID, path)
```

### Browser DevTools

Use browser developer tools to verify pushes:
1. Open Network tab
2. Look for "Push" in the Initiator column
3. Check timing to see if push saves time

## Compliance

Celeris implements Server Push according to:
- RFC 7540 Section 8.2 (Server Push)
- RFC 7540 Section 6.6 (PUSH_PROMISE Frame)

## Error Handling

### Push Failures

Push can fail if:
- Server push is disabled
- Stream limit reached
- Connection is closing
- Header encoding fails

Always handle push errors gracefully:

```go
if err := ctx.PushPromise("/resource", nil); err != nil {
    // Log but don't fail the request
    log.Printf("Push failed: %v", err)
}
// Continue with normal response
return ctx.HTML(200, content)
```

## Security Considerations

### Cross-Origin Pushes

Only push resources from the same origin. Celeris inherits the scheme and authority from the original request.

### Cache Poisoning

Be careful not to push resources that could poison the client's cache with incorrect content.

