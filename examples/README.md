# Celeris Examples

This directory contains examples demonstrating various features of the Celeris HTTP/2 framework.

## Examples

### Middleware Examples

- **[logger](logger/)** - Structured logging with JSON output and custom fields
- **[recovery](recovery/)** - Panic recovery middleware for server stability
- **[request-id](request-id/)** - Request tracing with unique identifiers
- **[compression](compression/)** - Automatic response compression (gzip/brotli)
- **[rate-limiting](rate-limiting/)** - Token bucket rate limiting
- **[health-check](health-check/)** - Health check endpoint middleware
- **[auto-docs](auto-docs/)** - Automatic API documentation generation
- **[cors](cors/)** - Cross-Origin Resource Sharing middleware

### Core Features

- **[basic-routing](basic-routing/)** - Route parameters, groups, and query parameters
- **[streaming](streaming/)** - Server-Sent Events and streaming responses
- **[server-push](server-push/)** - HTTP/2 server push for critical resources

## Running Examples

Each example can be run independently:

```bash
cd examples/logger
go run main.go
```

## Testing Examples

All examples are tested to ensure they work correctly:

```bash
# Test all examples
make test-examples

# Lint all examples
make lint-examples
```

## Features Demonstrated

### Middleware
- **Logger**: Structured logging with configurable formats and custom fields
- **Recovery**: Panic handling to prevent server crashes
- **RequestID**: Unique request identifiers for tracing and debugging
- **Compression**: Automatic response compression with configurable settings
- **Rate Limiting**: Token bucket algorithm with burst support
- **Health Check**: Configurable health endpoints for monitoring
- **Auto Docs**: Automatic OpenAPI documentation generation
- **CORS**: Cross-origin request handling

### Core Features
- **Routing**: Parameter extraction, route groups, and query handling
- **Streaming**: Real-time data with Server-Sent Events
- **Server Push**: HTTP/2 resource pushing for performance
- **Static Files**: File serving and downloads
- **Form Handling**: Form data parsing and validation
- **Cookie Management**: Cookie setting and retrieval

## Integration

These examples can be combined to build complete applications. Each example focuses on a specific feature while maintaining simplicity and clarity.
