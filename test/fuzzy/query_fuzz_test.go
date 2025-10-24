package fuzzy

import (
	"context"
	"testing"

	"github.com/albertbausili/celeris/internal/h2/stream"
	"github.com/albertbausili/celeris/pkg/celeris"
)

// FuzzQueryParsing tests query parameter parsing with random inputs
func FuzzQueryParsing(f *testing.F) {
	// Seed corpus
	f.Add("/search?q=test")
	f.Add("/search?q=test&page=1")
	f.Add("/search?q=test&page=1&enabled=true")
	f.Add("/search?q=hello%20world")
	f.Add("/search?q=")
	f.Add("/search?")
	f.Add("/search")
	f.Add("/?key=value&key2=value2")
	f.Add("/path?a=1&b=2&c=3&d=4&e=5")

	f.Fuzz(func(_ *testing.T, path string) {
		s := stream.NewStream(1)
		s.AddHeader(":path", path)
		s.AddHeader(":method", "GET")

		router := celeris.NewRouter()
		router.GET("/search", func(ctx *celeris.Context) error {
			// Try to parse various query parameters
			_ = ctx.Query("q")
			_, _ = ctx.QueryInt("page")
			_ = ctx.QueryBool("enabled")
			_ = ctx.QueryDefault("limit", "10")

			return ctx.String(200, "ok")
		})

		writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
			return nil
		}

		ctx := newContext(context.Background(), s, writeResponseFunc)

		// Should not panic
		_ = router.ServeHTTP2(ctx)
	})
}

// FuzzCookieParsing tests cookie parsing with random inputs
func FuzzCookieParsing(f *testing.F) {
	// Seed corpus
	f.Add("session=abc123")
	f.Add("session=abc123; user_id=42")
	f.Add("a=1; b=2; c=3")
	f.Add("")
	f.Add("invalid")
	f.Add("=value")
	f.Add("key=")
	f.Add(";;;")
	f.Add("key=value=extra")

	f.Fuzz(func(_ *testing.T, cookieHeader string) {
		s := stream.NewStream(1)
		s.AddHeader("cookie", cookieHeader)
		s.AddHeader(":method", "GET")
		s.AddHeader(":path", "/")

		writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
			return nil
		}

		ctx := newContext(context.Background(), s, writeResponseFunc)

		// Should not panic
		_ = ctx.Cookie("session")
		_ = ctx.Cookie("user_id")
		_ = ctx.Cookie("nonexistent")
	})
}

// FuzzSSEData tests SSE event formatting with random data
func FuzzSSEData(f *testing.F) {
	// Seed corpus
	f.Add("simple data")
	f.Add("data with\nnewlines")
	f.Add("")
	f.Add("line1\nline2\nline3")
	f.Add("data with\r\nCRLF")
	f.Add(string([]byte{0, 1, 2, 3, 4, 5}))

	f.Fuzz(func(_ *testing.T, data string) {
		s := stream.NewStream(1)
		writeResponseFunc := func(_ uint32, _ int, _ [][2]string, _ []byte) error {
			return nil
		}

		ctx := newContext(context.Background(), s, writeResponseFunc)

		event := celeris.SSEEvent{
			ID:    "test",
			Event: "message",
			Data:  data,
		}

		// Should not panic
		_ = ctx.SSE(event)
	})
}

// Helper function to create properly initialized context for fuzzing
func newContext(ctx context.Context, s *stream.Stream, writeFunc func(uint32, int, [][2]string, []byte) error) *celeris.Context {
	// Use the exported test helper from the celeris package
	return celeris.NewTestContext(ctx, s, writeFunc)
}
