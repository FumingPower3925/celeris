// Package celeris provides a high-performance HTTP/2 server implementation for Go.
package celeris

import (
	"io"
	"log"
	"runtime"
	"time"
)

// Config holds the server configuration options for both HTTP/1.1 and HTTP/2.
type Config struct {
	Addr                 string        // Server address to bind to
	Multicore            bool          // Enable multicore mode for better performance
	NumEventLoop         int           // Number of event loops (0 for auto-detect)
	ReusePort            bool          // Enable SO_REUSEPORT for load balancing
	ReadTimeout          time.Duration // Maximum duration for reading requests
	WriteTimeout         time.Duration // Maximum duration for writing responses
	IdleTimeout          time.Duration // Maximum idle time before connection close
	MaxHeaderBytes       int           // Maximum header size in bytes
	MaxConcurrentStreams uint32        // Maximum concurrent HTTP/2 streams
	MaxConnections       uint32        // Maximum concurrent connections
	MaxFrameSize         uint32        // Maximum HTTP/2 frame size
	InitialWindowSize    uint32        // Initial HTTP/2 flow control window size
	Logger               *log.Logger   // Logger for server events
	DisableKeepAlive     bool          // Disable HTTP keep-alive
	EnableH1             bool          // Enable HTTP/1.1 support (default true)
	EnableH2             bool          // Enable HTTP/2 support (default true)
}

// newSilentLogger creates a silent logger that discards all output
func newSilentLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() Config {
	// Calculate adaptive limits based on system resources
	maxConnections := calculateOptimalConnections()
	maxStreams := maxConnections * 2 // Allow 2 streams per connection

	return Config{
		Addr:                 ":8080",
		Multicore:            true,
		NumEventLoop:         0, // Auto-detect
		ReusePort:            true,
		ReadTimeout:          300 * time.Second, // Increased for high RPS stability
		WriteTimeout:         300 * time.Second, // Increased for high RPS stability
		IdleTimeout:          600 * time.Second, // Increased for better connection reuse
		MaxHeaderBytes:       16 << 20,          // 16 MB for high RPS
		MaxConcurrentStreams: maxStreams,        // Adaptive based on system resources
		MaxConnections:       maxConnections,    // Adaptive based on system resources
		MaxFrameSize:         262144,            // 256 KB for high throughput
		InitialWindowSize:    1048576,           // 1 MB for better flow control
		Logger:               newSilentLogger(),
		DisableKeepAlive:     false,
		EnableH1:             true, // Enable HTTP/1.1 by default
		EnableH2:             true, // Enable HTTP/2 by default
	}
}

// Validate checks and normalizes the configuration values.
func (c *Config) Validate() error {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.MaxFrameSize < 16384 {
		c.MaxFrameSize = 16384
	}
	if c.MaxFrameSize > (1<<24)-1 {
		c.MaxFrameSize = (1 << 24) - 1
	}
	if c.InitialWindowSize == 0 {
		c.InitialWindowSize = 65535
	}
	if c.MaxConcurrentStreams == 0 {
		c.MaxConcurrentStreams = 20000 // Increased default
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 20000 // Increased default
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
	// At least one protocol must be enabled
	if !c.EnableH1 && !c.EnableH2 {
		c.EnableH2 = true // Default to HTTP/2 if both disabled
	}
	return nil
}

// calculateOptimalConnections calculates the optimal number of connections based on system resources
func calculateOptimalConnections() uint32 {
	// Get system information
	numCPU := runtime.NumCPU()

	// Base calculation: 10000 connections per CPU core for high RPS
	// Convert to uint32 first to avoid potential overflow in multiplication
	//nolint:gosec // NumCPU() returns reasonable values (< 65536), so uint32 conversion is safe
	baseConnections := uint32(numCPU) * 10000

	// Cap at very high limits for 150k-200k RPS capability
	maxConnections := uint32(500000) // Maximum 500k connections for high RPS

	if baseConnections > maxConnections {
		baseConnections = maxConnections
	}

	// Ensure minimum connections for high performance
	minConnections := uint32(100000) // Minimum 100k connections
	if baseConnections < minConnections {
		baseConnections = minConnections
	}

	return baseConnections
}
