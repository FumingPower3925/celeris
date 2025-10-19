package celeris

// Handler defines the interface for HTTP/2 request handlers.
type Handler interface {
	ServeHTTP2(ctx *Context) error
}

// HandlerFunc is an adapter to allow ordinary functions to be used as HTTP/2 handlers.
type HandlerFunc func(ctx *Context) error

// ServeHTTP2 calls f(ctx).
func (f HandlerFunc) ServeHTTP2(ctx *Context) error {
	return f(ctx)
}

// Middleware is a function that wraps a Handler with additional functionality.
type Middleware func(Handler) Handler

// MiddlewareFunc is a function-based middleware that receives the context and next handler.
type MiddlewareFunc func(ctx *Context, next Handler) error

// ToMiddleware converts a MiddlewareFunc to a Middleware.
func (m MiddlewareFunc) ToMiddleware() Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			return m(ctx, next)
		})
	}
}

// Chain combines multiple middlewares into a single middleware.
func Chain(middlewares ...Middleware) Middleware {
	return func(final Handler) Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
