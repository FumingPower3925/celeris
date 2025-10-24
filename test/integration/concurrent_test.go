package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertbausili/celeris/pkg/celeris"
)

// TestConcurrentRequests tests handling multiple concurrent requests
func TestConcurrentRequests(t *testing.T) {
	var counter int32

	router := celeris.NewRouter()
	router.GET("/counter", func(ctx *celeris.Context) error {
		atomic.AddInt32(&counter, 1)
		time.Sleep(10 * time.Millisecond)
		return ctx.JSON(200, map[string]int32{"count": atomic.LoadInt32(&counter)})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	config.Multicore = true
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	// Make 20 concurrent requests
	const numRequests = 20
	var wg sync.WaitGroup
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			resp, err := client.Get(fmt.Sprintf("http://localhost%s/counter", config.Addr))
			if err != nil {
				errors <- fmt.Errorf("request %d failed: %v", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				errors <- fmt.Errorf("request %d: expected 200, got %d", id, resp.StatusCode)
				return
			}

			errors <- nil
		}(i)
	}

	wg.Wait()
	close(errors)

	errorCount := 0
	for err := range errors {
		if err != nil {
			t.Error(err)
			errorCount++
		}
	}

	if errorCount > 0 {
		t.Errorf("%d out of %d concurrent requests failed", errorCount, numRequests)
	}

	finalCount := atomic.LoadInt32(&counter)
	if finalCount != numRequests {
		t.Errorf("Expected counter to be %d, got %d", numRequests, finalCount)
	}
}

// TestRaceConditions tests for race conditions in handlers
func TestRaceConditions(t *testing.T) {
	sharedMap := make(map[string]int)
	var mu sync.Mutex

	router := celeris.NewRouter()
	router.GET("/increment/:key", func(ctx *celeris.Context) error {
		key := celeris.Param(ctx, "key")

		mu.Lock()
		sharedMap[key]++
		value := sharedMap[key]
		mu.Unlock()

		return ctx.JSON(200, map[string]int{"value": value})
	})

	config := celeris.DefaultConfig()
	config.Addr = getTestPort()
	config.Multicore = true
	server := celeris.New(config)

	go func() { _ = server.ListenAndServe(router) }()
	if err := waitForServer(config.Addr, 2*time.Second); err != nil {
		t.Fatalf("Server error: %v", err)
	}
	defer server.Stop(context.Background())

	client := createHTTP2Client()

	keys := []string{"a", "b"}
	const requestsPerKey = 10
	var wg sync.WaitGroup

	for _, key := range keys {
		for i := 0; i < requestsPerKey; i++ {
			wg.Add(1)
			go func(k string) {
				defer wg.Done()

				resp, err := client.Get(fmt.Sprintf("http://localhost%s/increment/%s", config.Addr, k))
				if err != nil {
					t.Errorf("Failed to increment %s: %v", k, err)
					return
				}
				resp.Body.Close()
			}(key)
		}
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	for _, key := range keys {
		if sharedMap[key] != requestsPerKey {
			t.Errorf("Key %s: expected %d increments, got %d", key, requestsPerKey, sharedMap[key])
		}
	}
}
