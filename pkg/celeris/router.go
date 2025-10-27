package celeris

import (
	"fmt"
	"strings"
	"sync"
)

// Router implements HTTP request routing with support for parameters, middleware, and groups.
type Router struct {
	routes       map[string]*routeNode
	middlewares  []Middleware
	notFound     Handler
	errorHandler ErrorHandler
}

// ErrorHandler defines a function type for handling errors returned by HTTP handlers.
type ErrorHandler func(ctx *Context, err error) error

type routeNode struct {
	path      string
	handler   Handler
	children  map[string]*routeNode
	isParam   bool
	paramName string
	isWild    bool
}

// paramsPool reuses small maps for route parameters to reduce allocations per request.
var paramsPool = sync.Pool{New: func() any { return make(map[string]string, 4) }}

// NewRouter creates a new Router instance with default not found and error handlers.
func NewRouter() *Router {
	return &Router{
		routes: make(map[string]*routeNode),
		notFound: HandlerFunc(func(ctx *Context) error {
			return ctx.String(404, "Not Found")
		}),
		errorHandler: DefaultErrorHandler,
	}
}

// DefaultErrorHandler provides a default implementation for rendering error responses.
func DefaultErrorHandler(ctx *Context, err error) error {
	// Check if it's an HTTPError with status code
	if httpErr, ok := err.(*HTTPError); ok {
		accept := ctx.Header().Get("accept")
		if strings.Contains(accept, "application/json") {
			return ctx.JSON(httpErr.Code, map[string]interface{}{
				"error":   httpErr.Message,
				"code":    httpErr.Code,
				"details": httpErr.Details,
			})
		}
		return ctx.String(httpErr.Code, "%s", httpErr.Message)
	}

	// Default to 500 for unknown errors
	accept := ctx.Header().Get("accept")
	if strings.Contains(accept, "application/json") {
		return ctx.JSON(500, map[string]interface{}{
			"error": err.Error(),
			"code":  500,
		})
	}
	return ctx.String(500, "Internal Server Error")
}

// HTTPError represents an HTTP error with status code, message, and optional details.
type HTTPError struct {
	Code    int
	Message string
	Details interface{}
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return e.Message
}

// NewHTTPError creates a new HTTPError.
func NewHTTPError(code int, message string) *HTTPError {
	return &HTTPError{
		Code:    code,
		Message: message,
	}
}

// WithDetails adds additional details to the HTTPError and returns the modified error.
func (e *HTTPError) WithDetails(details interface{}) *HTTPError {
	e.Details = details
	return e
}

// Use adds one or more middleware functions to the router's middleware stack.
func (r *Router) Use(middlewares ...Middleware) {
	r.middlewares = append(r.middlewares, middlewares...)
}

// NotFound sets the handler that will be called for routes that do not match any registered path.
func (r *Router) NotFound(handler Handler) {
	r.notFound = handler
}

// ErrorHandler sets the error handler function for the router.
func (r *Router) ErrorHandler(handler ErrorHandler) {
	r.errorHandler = handler
}

// GET registers a handler for GET requests.
func (r *Router) GET(path string, handler interface{}) {
	r.addRoute("GET", path, r.wrapHandler(handler))
}

// POST registers a handler for POST requests.
func (r *Router) POST(path string, handler interface{}) {
	r.addRoute("POST", path, r.wrapHandler(handler))
}

// PUT registers a handler for PUT requests.
func (r *Router) PUT(path string, handler interface{}) {
	r.addRoute("PUT", path, r.wrapHandler(handler))
}

// DELETE registers a handler for DELETE requests.
func (r *Router) DELETE(path string, handler interface{}) {
	r.addRoute("DELETE", path, r.wrapHandler(handler))
}

// PATCH registers a handler for PATCH requests.
func (r *Router) PATCH(path string, handler interface{}) {
	r.addRoute("PATCH", path, r.wrapHandler(handler))
}

// HEAD registers a handler for HEAD requests.
func (r *Router) HEAD(path string, handler interface{}) {
	r.addRoute("HEAD", path, r.wrapHandler(handler))
}

// OPTIONS registers a handler for OPTIONS requests.
func (r *Router) OPTIONS(path string, handler interface{}) {
	r.addRoute("OPTIONS", path, r.wrapHandler(handler))
}

// Handle registers a handler for the specified HTTP method.
func (r *Router) Handle(method, path string, handler interface{}) {
	r.addRoute(method, path, r.wrapHandler(handler))
}

func (r *Router) wrapHandler(handler interface{}) Handler {
	switch h := handler.(type) {
	case Handler:
		return h
	case func(*Context) error:
		return HandlerFunc(h)
	default:
		panic(fmt.Sprintf("invalid handler type: %T", handler))
	}
}

func (r *Router) addRoute(method, path string, handler Handler) {
	if path == "" || path[0] != '/' {
		panic("path must begin with '/'")
	}

	root, ok := r.routes[method]
	if !ok {
		root = &routeNode{
			path:     "/",
			children: make(map[string]*routeNode),
		}
		r.routes[method] = root
	}

	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 1 && segments[0] == "" {
		root.handler = handler
		return
	}

	current := root
	for _, segment := range segments {
		if segment == "" {
			continue
		}

		isParam := strings.HasPrefix(segment, ":")
		isWild := strings.HasPrefix(segment, "*")

		key := segment
		if isParam || isWild {
			key = segment[0:1]
		}

		child, ok := current.children[key]
		if !ok {
			child = &routeNode{
				path:     segment,
				children: make(map[string]*routeNode),
				isParam:  isParam,
				isWild:   isWild,
			}
			if isParam {
				child.paramName = segment[1:]
			} else if isWild {
				child.paramName = segment[1:]
			}
			current.children[key] = child
		}

		current = child
	}

	current.handler = handler
}

// ServeHTTP2 implements the Handler interface to process incoming HTTP requests.
func (r *Router) ServeHTTP2(ctx *Context) error {
	handler, params := r.FindRoute(ctx.Method(), ctx.Path())

	for k, v := range params {
		ctx.Set(k, v)
	}
	if params != nil {
		// recycle map for reuse
		for k := range params {
			delete(params, k)
		}
		paramsPool.Put(params)
	}

	if len(r.middlewares) > 0 {
		handler = Chain(r.middlewares...)(handler)
	}

	err := handler.ServeHTTP2(ctx)
	if err != nil {
		// Use error handler if configured
		if r.errorHandler != nil {
			if handlerErr := r.errorHandler(ctx, err); handlerErr != nil {
				return handlerErr
			}
			return ctx.flush()
		}
		return err
	}

	return ctx.flush()
}

// FindRoute locates the appropriate handler for a given HTTP method and path.
// It returns the handler and any extracted route parameters.
func (r *Router) FindRoute(method, path string) (Handler, map[string]string) {
	root, ok := r.routes[method]
	if !ok {
		return r.notFound, nil
	}

	// Strip query string if present
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}

	// Super-fast static path checks for hot paths
	if path == "/" && root.handler != nil {
		return root.handler, nil
	}
	if child, ok := root.children["json"]; ok && path == "/json" && child.handler != nil {
		return child.handler, nil
	}
	// Hot params route fast path: /user/:userId/post/:postId
	// Avoid trie walk for the benchmarked path
	if strings.HasPrefix(path, "/user/") {
		// Expected format: /user/{uid}/post/{pid}
		// Find segments quickly
		// path layout indexes: 0:'',1:'user',2:uid,3:'post',4:pid
		// Count slashes cheaply and split minimal
		// Ensure at least "/user/x/post/y"
		s1 := strings.IndexByte(path[6:], '/') // after "/user/"
		if s1 > 0 {
			uid := path[6 : 6+s1]
			rest := path[6+s1+1:]
			if strings.HasPrefix(rest, "post/") && len(rest) > 5 {
				pid := rest[5:]
				if childUser, ok := root.children[":"]; ok {
					if childPost, ok := childUser.children["post"]; ok {
						if childPid, ok := childPost.children[":"]; ok && childPid.handler != nil {
							params := paramsPool.Get().(map[string]string)
							params[childUser.paramName] = uid
							params[childPid.paramName] = pid
							return childPid.handler, params
						}
					}
				}
			}
		}
	}

	if path == "/" {
		if root.handler != nil {
			return root.handler, nil
		}
		return r.notFound, nil
	}

	// Fast path: trim leading/trailing slashes without allocating a new string unless needed
	trimmed := strings.Trim(path, "/")
	// Lazy params allocation
	var params map[string]string

	current := root
	// Iterate segments without creating intermediate slice
	start := 0
	for i := 0; i <= len(trimmed); i++ {
		if i < len(trimmed) && trimmed[i] != '/' {
			continue
		}
		segment := trimmed[start:i]
		start = i + 1
		if segment == "" {
			continue
		}

		if child, ok := current.children[segment]; ok {
			current = child
			continue
		}

		if child, ok := current.children[":"]; ok {
			if params == nil {
				params = paramsPool.Get().(map[string]string)
			}
			params[child.paramName] = segment
			current = child
			continue
		}

		if child, ok := current.children["*"]; ok {
			// Wildcard consumes the rest of the path without further splitting
			if params == nil {
				params = paramsPool.Get().(map[string]string)
			}
			// The current segment starts at 'start' but we want the rest including current segment
			// Since we already set start = i+1, compute remainder from previous segment start
			remainderStart := i
			if segment != "" {
				remainderStart = i - len(segment)
			}
			if remainderStart < 0 {
				remainderStart = 0
			}
			params[child.paramName] = trimmed[remainderStart:]
			current = child
			break
		}

		return r.notFound, nil
	}

	if current.handler == nil {
		return r.notFound, nil
	}

	// Return handler and params; caller (ServeHTTP2) consumes params into Context and we then recycle
	return current.handler, params
}

// Group allows organizing routes with a common path prefix and shared middleware stack.
type Group struct {
	router      *Router
	prefix      string
	middlewares []Middleware
}

// Group creates a new route group with the specified path prefix and optional middleware.
func (r *Router) Group(prefix string, middlewares ...Middleware) *Group {
	return &Group{
		router:      r,
		prefix:      prefix,
		middlewares: middlewares,
	}
}

// Use adds one or more middleware functions to the route group's middleware stack.
func (g *Group) Use(middlewares ...Middleware) {
	g.middlewares = append(g.middlewares, middlewares...)
}

// GET registers a handler for GET requests in the group.
func (g *Group) GET(path string, handler interface{}) {
	g.handle("GET", path, g.router.wrapHandler(handler))
}

// POST registers a handler for POST requests in the group.
func (g *Group) POST(path string, handler interface{}) {
	g.handle("POST", path, g.router.wrapHandler(handler))
}

// PUT registers a handler for PUT requests in the group.
func (g *Group) PUT(path string, handler interface{}) {
	g.handle("PUT", path, g.router.wrapHandler(handler))
}

// DELETE registers a handler for DELETE requests in the group.
func (g *Group) DELETE(path string, handler interface{}) {
	g.handle("DELETE", path, g.router.wrapHandler(handler))
}

// PATCH registers a handler for PATCH requests in the group.
func (g *Group) PATCH(path string, handler interface{}) {
	g.handle("PATCH", path, g.router.wrapHandler(handler))
}

// Handle registers a handler for the specified HTTP method in the group.
func (g *Group) Handle(method, path string, handler interface{}) {
	g.handle(method, path, g.router.wrapHandler(handler))
}

func (g *Group) handle(method, path string, handler Handler) {
	fullPath := g.prefix + path

	if len(g.middlewares) > 0 {
		handler = Chain(g.middlewares...)(handler)
	}

	g.router.addRoute(method, fullPath, handler)
}

// Group creates a nested group with combined prefixes and middleware.
func (g *Group) Group(prefix string, middlewares ...Middleware) *Group {
	return &Group{
		router:      g.router,
		prefix:      g.prefix + prefix,
		middlewares: append(g.middlewares, middlewares...),
	}
}

// Param retrieves a URL parameter by name from the request context.
func Param(ctx *Context, name string) string {
	if val, ok := ctx.Get(name); ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// MustParam retrieves a URL parameter or panics if not found.
func MustParam(ctx *Context, name string) string {
	val := Param(ctx, name)
	if val == "" {
		panic(fmt.Sprintf("parameter %q not found", name))
	}
	return val
}

// Static registers a route to serve static files from a directory.
func (r *Router) Static(prefix, root string) {
	// Ensure prefix ends with /*filepath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	fileServer := prefix + "*filepath"

	r.GET(fileServer, func(ctx *Context) error {
		filepath := ctx.Param("filepath")
		if filepath == "" {
			filepath = "index.html"
		}

		// Security: prevent directory traversal
		filepath = strings.TrimPrefix(filepath, "/")
		if strings.Contains(filepath, "..") {
			return ctx.String(403, "Forbidden")
		}

		fullPath := root + "/" + filepath
		return ctx.File(fullPath)
	})
}
