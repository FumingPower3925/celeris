package celeris

import (
	"context"
	"strings"
	"testing"

	"github.com/albertbausili/celeris/internal/h2/stream"
)

func TestContext_Method(t *testing.T) {
	s := stream.NewStream(1)
	s.AddHeader(":method", "GET")

	ctx := newContext(context.Background(), s, nil)

	if ctx.Method() != "GET" {
		t.Errorf("Expected method GET, got %s", ctx.Method())
	}
}

func TestContext_Path(t *testing.T) {
	s := stream.NewStream(1)
	s.AddHeader(":path", "/test")

	ctx := newContext(context.Background(), s, nil)

	if ctx.Path() != "/test" {
		t.Errorf("Expected path /test, got %s", ctx.Path())
	}
}

func TestContext_JSON(t *testing.T) {
	s := stream.NewStream(1)

	// Add mock write response function that captures response data
	var capturedStatus int
	var capturedHeaders [][2]string
	var capturedBody []byte
	writeResponseFunc := func(_ uint32, status int, headers [][2]string, body []byte) error {
		capturedStatus = status
		capturedHeaders = headers
		capturedBody = body
		return nil
	}

	ctx := newContext(context.Background(), s, writeResponseFunc)

	data := map[string]string{"key": "value"}
	err := ctx.JSON(200, data)

	if err != nil {
		t.Errorf("JSON() error = %v", err)
	}

	if capturedStatus != 200 {
		t.Errorf("Expected status 200, got %d", capturedStatus)
	}

	expected := `{"key":"value"}`
	if string(capturedBody) != expected {
		t.Errorf("Expected body %s, got %s", expected, string(capturedBody))
	}

	// Check headers
	contentType := ""
	for _, header := range capturedHeaders {
		if header[0] == "content-type" {
			contentType = header[1]
			break
		}
	}
	if contentType != "application/json" {
		t.Errorf("Expected content-type application/json, got %s", contentType)
	}
}

func TestContext_String(t *testing.T) {
	s := stream.NewStream(1)

	// Add mock write response function that captures response data
	var capturedStatus int
	var capturedBody []byte
	writeResponseFunc := func(_ uint32, status int, _ [][2]string, body []byte) error {
		capturedStatus = status
		capturedBody = body
		return nil
	}

	ctx := newContext(context.Background(), s, writeResponseFunc)

	err := ctx.String(200, "Hello, %s!", "World")

	if err != nil {
		t.Errorf("String() error = %v", err)
	}

	if capturedStatus != 200 {
		t.Errorf("Expected status 200, got %d", capturedStatus)
	}

	expected := "Hello, World!"
	if string(capturedBody) != expected {
		t.Errorf("Expected body %s, got %s", expected, string(capturedBody))
	}
}

func TestContext_SetGetValue(t *testing.T) {
	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	ctx.Set("key", "value")

	val, ok := ctx.Get("key")
	if !ok {
		t.Error("Expected to find key")
	}

	if val != "value" {
		t.Errorf("Expected value 'value', got %v", val)
	}
}

func TestContext_BindJSON(t *testing.T) {
	t.Skip("BindJSON test requires proper stream state management - tested in integration tests")

	s := stream.NewStream(1)
	_ = s.AddData([]byte(`{"name":"test"}`))

	ctx := newContext(context.Background(), s, nil)

	var result struct {
		Name string `json:"name"`
	}

	err := ctx.BindJSON(&result)
	if err != nil {
		t.Errorf("BindJSON() error = %v", err)
	}

	if result.Name != "test" {
		t.Errorf("Expected name 'test', got %s", result.Name)
	}
}

func TestHeaders_SetGet(t *testing.T) {
	h := NewHeaders()

	h.Set("key", "value")

	if h.Get("key") != "value" {
		t.Errorf("Expected 'value', got %s", h.Get("key"))
	}
}

func TestHeaders_Del(t *testing.T) {
	h := NewHeaders()

	h.Set("key", "value")
	h.Del("key")

	if h.Has("key") {
		t.Error("Expected key to be deleted")
	}
}

func TestHeaders_All(t *testing.T) {
	h := NewHeaders()

	h.Set("key1", "value1")
	h.Set("key2", "value2")

	all := h.All()

	if len(all) != 2 {
		t.Errorf("Expected 2 headers, got %d", len(all))
	}
}

// Tests for new context methods

func TestContext_Query(t *testing.T) {
	s := stream.NewStream(1)
	s.AddHeader(":path", "/search?q=test&page=2&enabled=true")
	ctx := newContext(context.Background(), s, nil)

	// Test Query
	if ctx.Query("q") != "test" {
		t.Errorf("Expected query 'test', got %s", ctx.Query("q"))
	}

	// Test QueryInt
	page, err := ctx.QueryInt("page")
	if err != nil {
		t.Errorf("QueryInt error: %v", err)
	}
	if page != 2 {
		t.Errorf("Expected page 2, got %d", page)
	}

	// Test QueryBool
	if !ctx.QueryBool("enabled") {
		t.Error("Expected enabled to be true")
	}

	// Test QueryDefault
	limit := ctx.QueryDefault("limit", "10")
	if limit != "10" {
		t.Errorf("Expected default limit '10', got %s", limit)
	}
}

func TestContext_Cookie(t *testing.T) {
	t.Skip("Cookie parsing requires full stream setup - tested in integration tests")
	s := stream.NewStream(1)
	s.AddHeader("cookie", "session=abc123; user_id=42")
	ctx := newContext(context.Background(), s, nil)

	session := ctx.Cookie("session")
	if session != "abc123" {
		t.Errorf("Expected session 'abc123', got %s", session)
	}

	userID := ctx.Cookie("user_id")
	if userID != "42" {
		t.Errorf("Expected user_id '42', got %s", userID)
	}
}

func TestContext_Param(t *testing.T) {
	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	ctx.params = []RouteParam{{Key: "id", Value: "123"}}

	if ctx.Param("id") != "123" {
		t.Errorf("Expected param '123', got %s", ctx.Param("id"))
	}
}

func TestContext_SSE(t *testing.T) {
	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	event := SSEEvent{
		ID:    "123",
		Event: "message",
		Data:  "Test data",
		Retry: 3000,
	}

	err := ctx.SSE(event)
	if err != nil {
		t.Errorf("SSE error: %v", err)
	}

	body := ctx.responseBody.String()

	if !strings.Contains(body, "id: 123") {
		t.Error("Expected SSE to contain id")
	}
}

func TestContext_Writer(t *testing.T) {
	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	writer := ctx.Writer()
	if writer == nil {
		t.Error("Expected non-nil writer")
	}
}
