package fuzzy

import (
	"strings"
	"testing"

	"github.com/FumingPower3925/celeris/internal/h1"
)

// FuzzH1RequestLine fuzzes HTTP/1.1 request line parsing with random inputs.
// It verifies that the HTTP/1.1 parser handles various request line formats safely.
func FuzzH1RequestLine(f *testing.F) {
	// Seed with valid request lines
	f.Add([]byte("GET / HTTP/1.1\r\n"))
	f.Add([]byte("POST /api HTTP/1.1\r\n"))
	f.Add([]byte("PUT /resource HTTP/1.1\r\n"))
	f.Add([]byte("DELETE /item/123 HTTP/1.1\r\n"))
	f.Add([]byte("HEAD /info HTTP/1.1\r\n"))
	f.Add([]byte("OPTIONS * HTTP/1.1\r\n"))
	f.Add([]byte("PATCH /update HTTP/1.1\r\n"))
	f.Add([]byte("GET /path?query=value HTTP/1.1\r\n"))
	f.Add([]byte("POST /api/users?id=123&name=test HTTP/1.1\r\n"))

	// Seed with some invalid inputs
	f.Add([]byte("GET /path\r\n"))
	f.Add([]byte("INVALID\r\n"))
	f.Add([]byte("\r\n"))
	f.Add([]byte("GET"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		parser := h1.NewParser()
		parser.Reset(data)

		req := &h1.Request{}

		// Should never panic
		_, _ = parser.ParseRequest(req)

		// Basic sanity checks if parsing succeeded
		if req.Method != "" {
			// Method should be reasonable length
			if len(req.Method) > 100 {
				t.Errorf("Method too long: %d", len(req.Method))
			}

			// Path should not be empty if method is set
			if req.Path == "" {
				t.Errorf("Path empty but method set: %s", req.Method)
			}
		}

		// If headers are complete, basic validation
		if req.HeadersComplete {
			// Should have reasonable version
			if req.Version != "" && !strings.HasPrefix(req.Version, "HTTP/") {
				t.Errorf("Invalid version format: %s", req.Version)
			}
		}
	})
}

// FuzzH1Headers fuzzes HTTP/1.1 header parsing with random inputs.
// It verifies that the HTTP/1.1 parser handles various header formats safely.
func FuzzH1Headers(f *testing.F) {
	// Seed with valid headers
	f.Add([]byte("GET / HTTP/1.1\r\nHost: example.com\r\nUser-Agent: test\r\n\r\n"))
	f.Add([]byte("POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte("GET / HTTP/1.1\r\nHost: test.com\r\nConnection: keep-alive\r\nAccept: */*\r\n\r\n"))
	f.Add([]byte("PUT /data HTTP/1.1\r\nHost: api.example.com\r\nTransfer-Encoding: chunked\r\n\r\n"))

	// Seed with edge cases
	f.Add([]byte("GET / HTTP/1.1\r\nHost:example.com\r\n\r\n"))     // No space after colon
	f.Add([]byte("GET / HTTP/1.1\r\nHost:  example.com  \r\n\r\n")) // Extra spaces
	f.Add([]byte("GET / HTTP/1.1\r\n\r\n"))                         // No headers
	f.Add([]byte("GET / HTTP/1.1\r\nX-Custom-Header: value with spaces\r\n\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		parser := h1.NewParser()
		parser.Reset(data)

		req := &h1.Request{}

		// Should never panic
		_, err := parser.ParseRequest(req)

		// If parsing succeeded, validate headers
		if err == nil && req.HeadersComplete {
			// Check that headers are well-formed
			for _, h := range req.Headers {
				// Header name shouldn't be empty
				if h[0] == "" {
					t.Error("Empty header name")
				}

				// Header name shouldn't contain invalid characters
				if strings.ContainsAny(h[0], "\r\n\x00") {
					t.Errorf("Invalid characters in header name: %q", h[0])
				}

				// Header value shouldn't contain CRLF
				if strings.ContainsAny(h[1], "\r\n\x00") {
					t.Errorf("Invalid characters in header value: %q", h[1])
				}

				// Reasonable header length
				if len(h[0]) > 1000 || len(h[1]) > 10000 {
					t.Errorf("Header too long: %s: %d bytes", h[0], len(h[1]))
				}
			}

			// Content-Length should be non-negative
			if req.ContentLength < -1 {
				t.Errorf("Invalid content-length: %d", req.ContentLength)
			}
		}
	})
}

// FuzzH1RequestFull fuzzes complete HTTP/1.1 request parsing including body content.
// It verifies that the HTTP/1.1 parser handles full requests with various body formats safely.
func FuzzH1RequestFull(f *testing.F) {
	// Seed with complete requests
	f.Add([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	f.Add([]byte("POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Length: 11\r\n\r\nhello world"))
	f.Add([]byte("POST /data HTTP/1.1\r\nHost: test.com\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"))
	f.Add([]byte("PUT /resource HTTP/1.1\r\nHost: api.com\r\nContent-Length: 4\r\n\r\ntest"))

	// Seed with partial/invalid requests
	f.Add([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n"))                               // Missing final CRLF
	f.Add([]byte("POST / HTTP/1.1\r\nHost: test.com\r\nContent-Length: 100\r\n\r\nshort")) // Body shorter than Content-Length

	f.Fuzz(func(t *testing.T, data []byte) {
		parser := h1.NewParser()
		parser.Reset(data)

		req := &h1.Request{}

		// Should never panic
		consumed, err := parser.ParseRequest(req)

		// Consumed bytes should never exceed input
		if consumed > len(data) {
			t.Errorf("Consumed %d bytes but only had %d", consumed, len(data))
		}

		// Consumed should be non-negative
		if consumed < 0 {
			t.Errorf("Negative consumed bytes: %d", consumed)
		}

		// If parsing succeeded with headers complete, validate structure
		if err == nil && req.HeadersComplete {
			// Method should be non-empty
			if req.Method == "" {
				t.Error("Method empty after successful parse")
			}

			// Path should be non-empty
			if req.Path == "" {
				t.Error("Path empty after successful parse")
			}

			// Version should be valid
			if req.Version != "HTTP/1.1" && req.Version != "HTTP/1.0" {
				t.Errorf("Invalid version: %s", req.Version)
			}

			// Body read shouldn't exceed content length
			if req.ContentLength >= 0 && req.BodyRead > req.ContentLength {
				t.Errorf("Body read %d exceeds content-length %d", req.BodyRead, req.ContentLength)
			}
		}
	})
}

// FuzzH1QueryParsing fuzzes query string parsing in HTTP/1.1 request paths.
// It verifies that query parameter parsing handles various URL formats safely.
func FuzzH1QueryParsing(f *testing.F) {
	// Seed with various query patterns
	f.Add("/?key=value")
	f.Add("/api?id=123&name=test")
	f.Add("/search?q=hello%20world")
	f.Add("/path?a=1&b=2&c=3&d=4&e=5")
	f.Add("/?empty=&key=value")
	f.Add("/test?")
	f.Add("/?&&&&")
	f.Add("/api?key=value&key=value2") // Duplicate keys
	f.Add("/?%20=%20")
	f.Add("/?a=b=c=d")

	f.Fuzz(func(t *testing.T, path string) {
		// Parse request with this path
		reqLine := "GET " + path + " HTTP/1.1\r\nHost: test.com\r\n\r\n"

		parser := h1.NewParser()
		parser.Reset([]byte(reqLine))

		req := &h1.Request{}

		// Should never panic
		_, err := parser.ParseRequest(req)

		if err == nil && req.HeadersComplete {
			// Path should match what we sent
			if req.Path != path {
				t.Errorf("Path mismatch: got %q, want %q", req.Path, path)
			}

			// Path shouldn't contain control characters
			if strings.ContainsAny(req.Path, "\r\n\x00") {
				t.Errorf("Path contains invalid characters: %q", req.Path)
			}

			// Reasonable path length
			if len(req.Path) > 8192 {
				t.Errorf("Path too long: %d bytes", len(req.Path))
			}
		}
	})
}
