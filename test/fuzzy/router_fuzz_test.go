package fuzzy

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

// FuzzRouterPaths tests router path matching with random inputs.
// It verifies that the router handles various path formats safely without panicking.
func FuzzRouterPaths(f *testing.F) {
	// Seed corpus with interesting test cases
	f.Add("/")
	f.Add("/test")
	f.Add("/users/123")
	f.Add("/api/v1/users/123/posts/456")
	f.Add("//double//slash")
	f.Add("/trailing/")
	f.Add("/with%20spaces")
	f.Add("/unicode/café")
	f.Add("/symbols/!@#$%^&*()")
	f.Add("/very/long/" + strings.Repeat("segment/", 50))
	f.Add("/with/../dots")
	f.Add("/with/./dot")
	f.Add("")
	f.Add("no-leading-slash")
	f.Add("/with\nnewline")
	f.Add("/with\ttab")

	router := celeris.NewRouter()
	router.GET("/", func(ctx *celeris.Context) error {
		return ctx.String(200, "root")
	})
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "test")
	})
	router.GET("/users/:id", func(ctx *celeris.Context) error {
		return ctx.String(200, "user")
	})
	router.GET("/api/v1/users/:userId/posts/:postId", func(ctx *celeris.Context) error {
		return ctx.String(200, "post")
	})
	router.GET("/files/*path", func(ctx *celeris.Context) error {
		return ctx.String(200, "files")
	})

	f.Fuzz(func(t *testing.T, path string) {
		// Skip invalid UTF-8
		if !utf8.ValidString(path) {
			t.Skip("invalid UTF-8")
		}

		// Should not panic when routing
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Router panicked with path %q: %v", path, r)
			}
		}()

		_, _ = router.FindRoute("GET", path)
	})
}

// FuzzRouterMethods tests router with random HTTP methods
func FuzzRouterMethods(f *testing.F) {
	// Seed with common methods and edge cases
	f.Add("GET")
	f.Add("POST")
	f.Add("PUT")
	f.Add("DELETE")
	f.Add("PATCH")
	f.Add("HEAD")
	f.Add("OPTIONS")
	f.Add("get")
	f.Add("")
	f.Add("INVALID")

	router := celeris.NewRouter()
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "GET")
	})
	router.POST("/test", func(ctx *celeris.Context) error {
		return ctx.String(200, "POST")
	})

	f.Fuzz(func(t *testing.T, method string) {
		if !utf8.ValidString(method) {
			t.Skip("invalid UTF-8")
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Router panicked with method %q: %v", method, r)
			}
		}()

		_, _ = router.FindRoute(method, "/test")
	})
}

// FuzzRouteParameters tests parameter extraction
func FuzzRouteParameters(f *testing.F) {
	// Seed with various parameter values
	f.Add("123")
	f.Add("abc")
	f.Add("user-name")
	f.Add("")
	f.Add(strings.Repeat("a", 1000))
	f.Add("../../../etc/passwd")

	router := celeris.NewRouter()
	router.GET("/users/:id", func(ctx *celeris.Context) error {
		return ctx.String(200, "%s", celeris.Param(ctx, "id"))
	})

	f.Fuzz(func(t *testing.T, paramValue string) {
		if !utf8.ValidString(paramValue) {
			t.Skip("invalid UTF-8")
		}

		path := "/users/" + paramValue

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Router panicked with param %q: %v", paramValue, r)
			}
		}()

		_, _ = router.FindRoute("GET", path)
	})
}

// FuzzRouteDefinition tests route registration with random patterns
func FuzzRouteDefinition(f *testing.F) {
	f.Add("/test")
	f.Add("/users/:id")
	f.Add("/files/*path")
	f.Add("/:param1/:param2")
	f.Add("")
	f.Add("no-slash")

	f.Fuzz(func(t *testing.T, routePattern string) {
		if !utf8.ValidString(routePattern) {
			t.Skip("invalid UTF-8")
		}

		if len(routePattern) > 1000 {
			t.Skip("route pattern too long")
		}

		defer func() {
			if r := recover(); r != nil {
				// Panics on invalid route patterns are acceptable
				t.Logf("Router panicked registering route %q: %v", routePattern, r)
			}
		}()

		router := celeris.NewRouter()
		router.GET(routePattern, func(ctx *celeris.Context) error {
			return ctx.String(200, "ok")
		})
	})
}
