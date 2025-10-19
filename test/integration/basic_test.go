package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
	"golang.org/x/net/http2"
)

// TestBasicRequest tests basic HTTP/2 request-response cycle
func TestBasicRequest(t *testing.T) {
	router := celeris.NewRouter()
	router.GET("/test", func(ctx *celeris.Context) error {
		return ctx.JSON(200, map[string]string{
			"status": "ok",
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/test", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// TestRouteParameters tests route parameter extraction
func TestRouteParameters(t *testing.T) {
	router := celeris.NewRouter()

	router.GET("/users/:id", func(ctx *celeris.Context) error {
		id := celeris.Param(ctx, "id")
		return ctx.JSON(200, map[string]string{
			"user_id": id,
		})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/users/123", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("Response: %s", string(body))
}

// TestNotFound tests 404 handling
func TestNotFound(t *testing.T) {
	router := celeris.NewRouter()
	router.GET("/exists", func(ctx *celeris.Context) error {
		return ctx.String(200, "ok")
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	server := celeris.New(config)

	go func() {
		server.ListenAndServe(router)
	}()

	time.Sleep(100 * time.Millisecond)
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	resp, err := client.Get(fmt.Sprintf("http://localhost%s/notfound", config.Addr))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("Expected status 404, got %d", resp.StatusCode)
	}
}

// Helper functions

var testPortCounter uint32

func getTestPort() string {
	// Use atomic counter to ensure unique ports across parallel tests
	port := 20000 + atomic.AddUint32(&testPortCounter, 1)
	return fmt.Sprintf(":%d", port)
}

func createHTTP2Client() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
			// Disable automatic compression so we can test our middleware
			DisableCompression: true,
		},
		Timeout: 5 * time.Second,
	}
}
