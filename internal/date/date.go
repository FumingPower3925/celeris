// Package date provides a cached, thread-safe RFC1123 date string.
package date

import (
	"sync/atomic"
	"time"
	"unsafe"
)

// date stores the cached RFC1123 date string to avoid time.Now().Format() every request.
// We use atomic.Pointer (or unsafe.Pointer) for lock-free read access.
var currentDate unsafe.Pointer

// StartTicker starts a ticker that updates the date string every 500ms.
// It returns a stop function.
func StartTicker() func() {
	// Initialize immediately
	update()

	ticker := time.NewTicker(500 * time.Millisecond)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				update()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()

	return func() {
		close(done)
	}
}

// update updates the cached date string.
func update() {
	// RFC1123 is the standard HTTP date format
	s := time.Now().UTC().Format(time.RFC1123)
	// Cache the bytes to avoid conversion during write
	b := []byte(s)
	//nolint:gosec // G103: Atomic store of unsafe.Pointer to []byte is safe here as we don't modify the slice
	atomic.StorePointer(&currentDate, unsafe.Pointer(&b))
}

// Current returns the current cached date header bytes.
func Current() []byte {
	p := atomic.LoadPointer(&currentDate)
	if p == nil {
		// Fallback if not started yet (shouldn't happen if StartTicker is called)
		return []byte(time.Now().UTC().Format(time.RFC1123))
	}
	return *(*[]byte)(p)
}
