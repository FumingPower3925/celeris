package celeris

// Handler defines the interface for HTTP request handlers.
type Handler interface {
	ServeHTTP2(ctx *Context) error
}

// HandlerFunc is an adapter that allows ordinary functions to be used as HTTP handlers.
type HandlerFunc func(ctx *Context) error

// ServeHTTP2 calls f(ctx).
func (f HandlerFunc) ServeHTTP2(ctx *Context) error {
	return f(ctx)
}

// Middleware is a function that wraps a Handler to provide additional functionality.
type Middleware func(Handler) Handler

// MiddlewareFunc is a function-based middleware that receives the context and next handler.
type MiddlewareFunc func(ctx *Context, next Handler) error

// ToMiddleware converts a MiddlewareFunc to a Middleware wrapper.
func (m MiddlewareFunc) ToMiddleware() Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			return m(ctx, next)
		})
	}
}

// Chain combines multiple middlewares into a single middleware in the specified order.
func Chain(middlewares ...Middleware) Middleware {
	return func(final Handler) Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
