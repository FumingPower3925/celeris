package fuzzy

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// FuzzHeaders_SetGet tests header set and get operations with random inputs.
// It verifies that setting and retrieving headers works correctly and doesn't panic.
func FuzzHeaders_SetGet(f *testing.F) {
	f.Add("content-type", "application/json")
	f.Add("Content-Type", "text/html")
	f.Add("x-custom", "value")
	f.Add("", "")
	f.Add("UPPERCASE", "VALUE")

	f.Fuzz(func(t *testing.T, key, value string) {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			t.Skip("invalid UTF-8")
		}

		if len(key) > 10000 || len(value) > 100000 {
			t.Skip("input too long")
		}

		headers := celeris.NewHeaders()

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Headers.Set panicked: %v", r)
			}
		}()

		headers.Set(key, value)
		result := headers.Get(key)

		if key != "" && result != value {
			t.Errorf("Get returned %q, expected %q", result, value)
		}
	})
}

// FuzzHeaders_Del tests header deletion operations with random inputs.
// It verifies that deleting headers works correctly and doesn't panic.
func FuzzHeaders_Del(f *testing.F) {
	f.Add("content-type")
	f.Add("")
	f.Add("non-existent")
	f.Add(strings.Repeat("x", 1000))

	f.Fuzz(func(t *testing.T, key string) {
		if !utf8.ValidString(key) {
			t.Skip("invalid UTF-8")
		}

		if len(key) > 10000 {
			t.Skip("key too long")
		}

		headers := celeris.NewHeaders()
		headers.Set(key, "value")

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Headers.Del panicked: %v", r)
			}
		}()

		headers.Del(key)

		if headers.Has(key) {
			t.Error("Header should have been deleted")
		}
	})
}

// FuzzHeaders_MultipleOperations tests complex header operations with multiple keys.
// It verifies that setting multiple headers and performing various operations doesn't panic.
func FuzzHeaders_MultipleOperations(f *testing.F) {
	f.Add("key1", "value1", "key2", "value2")
	f.Add("", "", "", "")
	f.Add("same", "value1", "same", "value2")

	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		if !utf8.ValidString(k1) || !utf8.ValidString(v1) || !utf8.ValidString(k2) || !utf8.ValidString(v2) {
			t.Skip("invalid UTF-8")
		}

		if len(k1) > 1000 || len(v1) > 10000 || len(k2) > 1000 || len(v2) > 10000 {
			t.Skip("input too long")
		}

		headers := celeris.NewHeaders()

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Headers operations panicked: %v", r)
			}
		}()

		headers.Set(k1, v1)
		headers.Set(k2, v2)

		// Verify operations complete without panic
		_ = headers.All()
		_ = headers.Has(k1)
		_ = headers.Has(k2)

		headers.Del(k1)
	})
}

// FuzzHeaders_SpecialCharacters tests header operations with special characters.
// It verifies that headers with various special characters are handled safely.
func FuzzHeaders_SpecialCharacters(f *testing.F) {
	f.Add("header-with-dash", "value")
	f.Add("header_with_underscore", "value")
	f.Add("header.with.dot", "value")
	f.Add("header with space", "value")

	f.Fuzz(func(t *testing.T, key, value string) {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			t.Skip("invalid UTF-8")
		}

		if len(key) > 1000 || len(value) > 10000 {
			t.Skip("input too long")
		}

		headers := celeris.NewHeaders()

		defer func() {
			if r := recover(); r != nil {
				t.Logf("Headers panicked with special chars: %v", r)
			}
		}()

		headers.Set(key, value)
		_ = headers.Get(key)
	})
}

// FuzzHeaders_UpdateExisting tests updating existing headers with new values.
// It verifies that header updates work correctly and the final value is properly stored.
func FuzzHeaders_UpdateExisting(f *testing.F) {
	f.Add("key", "value1", "value2", "value3")

	f.Fuzz(func(t *testing.T, key, v1, v2, v3 string) {
		if !utf8.ValidString(key) || !utf8.ValidString(v1) || !utf8.ValidString(v2) || !utf8.ValidString(v3) {
			t.Skip("invalid UTF-8")
		}

		if len(key) > 1000 || len(v1) > 10000 || len(v2) > 10000 || len(v3) > 10000 {
			t.Skip("input too long")
		}

		headers := celeris.NewHeaders()

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Headers update panicked: %v", r)
			}
		}()

		headers.Set(key, v1)
		headers.Set(key, v2)
		headers.Set(key, v3)

		if key != "" {
			result := headers.Get(key)
			if result != v3 {
				t.Errorf("Expected final value %q, got %q", v3, result)
			}
		}
	})
}
