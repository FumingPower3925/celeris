---
title: "Context"
weight: 3
---

# Context API

The `Context` provides request/response handling for HTTP/2 (Celeris HTTP/2 first framework) requests.

## Request Methods

### Method

```go
func (c *Context) Method() string
```

Returns the HTTP method (GET, POST, etc.).

### Path

```go
func (c *Context) Path() string
```

Returns the request path.

### Header

```go
func (c *Context) Header() *Headers
```

Returns the request headers.

### Body

```go
func (c *Context) Body() io.Reader
```

Returns the request body reader.

### BodyBytes

```go
func (c *Context) BodyBytes() ([]byte, error)
```

Reads and returns the entire request body as bytes.

### BindJSON

```go
func (c *Context) BindJSON(v interface{}) error
```

Binds the request body as JSON into the provided interface.

**Example:**

```go
var user struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}
if err := ctx.BindJSON(&user); err != nil {
    return ctx.JSON(400, map[string]string{"error": "Invalid JSON"})
}
```

## Response Methods

### SetStatus

```go
func (c *Context) SetStatus(code int)
```

Sets the response status code.

### SetHeader

```go
func (c *Context) SetHeader(key, value string)
```

Sets a response header.

### Write

```go
func (c *Context) Write(data []byte) (int, error)
```

Writes raw bytes to the response body.

### WriteString

```go
func (c *Context) WriteString(s string) (int, error)
```

Writes a string to the response body.

### JSON

```go
func (c *Context) JSON(status int, v interface{}) error
```

Writes a JSON response.

**Example:**

```go
return ctx.JSON(200, map[string]string{
    "message": "Success",
})
```

### String

```go
func (c *Context) String(status int, format string, values ...interface{}) error
```

Writes a plain text response.

**Example:**

```go
return ctx.String(200, "Hello, %s!", name)
```

### HTML

```go
func (c *Context) HTML(status int, html string) error
```

Writes an HTML response.

**Example:**

```go
return ctx.HTML(200, "<h1>Hello World</h1>")
```

### Data

```go
func (c *Context) Data(status int, contentType string, data []byte) error
```

Writes a binary response with custom content type.

**Example:**

```go
return ctx.Data(200, "application/pdf", pdfBytes)
```

### NoContent

```go
func (c *Context) NoContent(status int) error
```

Sends a response with no body.

**Example:**

```go
return ctx.NoContent(204)
```

### Redirect

```go
func (c *Context) Redirect(status int, url string) error
```

Sends a redirect response.

**Example:**

```go
return ctx.Redirect(302, "/new-location")
```

## Value Storage

### Set

```go
func (c *Context) Set(key string, value interface{})
```

Stores a value in the context (useful for middleware).

### Get
```go
func (c *Context) Get(key string) (interface{}, bool)
```

Retrieves a value from the context.

## URL Parameters

### Query

```go
func (c *Context) Query(key string) string
```

Retrieves a query parameter by key used in the URL (?key=value).

## Request Info

### Scheme

```go
func (c *Context) Scheme() string
```

Returns the request scheme (http/https).

### Authority

```go
func (c *Context) Authority() string
```

Returns the request authority (host).

## Advanced Response

### Plain

```go
func (c *Context) Plain(status int, s string) error
```

Writes a plain text response without formatting overhead (faster than String).

### Flush

```go
func (c *Context) Flush() error
```

Flushes the current response buffer to the client.

### Stream

```go
func (c *Context) Stream(fn func(w io.Writer) error) error
```

Streams data to the client using a writer.

### SSE

```go
func (c *Context) SSE(event SSEEvent) error
```

Sends a Server-Sent Event (SSE).

```go
type SSEEvent struct {
    ID    string
    Event string
    Data  string
    Retry int
}
```

### MustGet

```go
func (c *Context) MustGet(key string) interface{}
```

Retrieves a value or panics if not found.

**Example:**

```go
// In middleware
ctx.Set("user-id", 123)

// In handler
userID := ctx.MustGet("user-id").(int)
```

## Context

### Context

```go
func (c *Context) Context() context.Context
```

Returns the underlying Go context for cancellation and deadlines.

**Example:**

```go
select {
case <-ctx.Context().Done():
    return ctx.String(408, "Request Timeout")
case result := <-process():
    return ctx.JSON(200, result)
}
```

## Headers

### Headers Type

```go
type Headers struct {
    // Internal fields
}
```

### Set

```go
func (h *Headers) Set(key, value string)
```

Sets a header value.

### Get

```go
func (h *Headers) Get(key string) string
```

Gets a header value.

### Del

```go
func (h *Headers) Del(key string)
```

Deletes a header.

### Has

```go
func (h *Headers) Has(key string) bool
```

Checks if a header exists.

### All

```go
func (h *Headers) All() [][2]string
```

Returns all headers.

## Examples

### Reading Request Data

```go
func handler(ctx *celeris.Context) error {
    method := ctx.Method()
    path := ctx.Path()
    userAgent := ctx.Header().Get("user-agent")
    
    body, err := ctx.BodyBytes()
    if err != nil {
        return ctx.String(500, "Error reading body")
    }
    
    // Process request...
    return ctx.JSON(200, map[string]interface{}{
        "method": method,
        "path":   path,
    })
}
```

### Writing Different Response Types

```go
// JSON
func jsonHandler(ctx *celeris.Context) error {
    return ctx.JSON(200, map[string]string{"status": "ok"})
}

// HTML
func htmlHandler(ctx *celeris.Context) error {
    return ctx.HTML(200, "<h1>Hello</h1>")
}

// Plain text
func textHandler(ctx *celeris.Context) error {
    return ctx.String(200, "Hello, World!")
}

// Binary data
func imageHandler(ctx *celeris.Context) error {
    imageData := loadImage()
    return ctx.Data(200, "image/png", imageData)
}
```

### Using Context Values

```go
func authMiddleware(next celeris.Handler) celeris.Handler {
    return celeris.HandlerFunc(func(ctx *celeris.Context) error {
        token := ctx.Header().Get("authorization")
        userID := validateToken(token)
        ctx.Set("user-id", userID)
        return next.ServeHTTP2(ctx)
    })
}

func handler(ctx *celeris.Context) error {
    userID := ctx.MustGet("user-id").(int)
    return ctx.JSON(200, map[string]int{"user_id": userID})
}
```

