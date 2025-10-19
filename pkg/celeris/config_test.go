package celeris

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Addr != ":8080" {
		t.Errorf("Expected default addr :8080, got %s", config.Addr)
	}

	if !config.Multicore {
		t.Error("Expected multicore to be true by default")
	}

	if !config.ReusePort {
		t.Error("Expected ReusePort to be true by default")
	}

	if config.ReadTimeout != 30*time.Second {
		t.Errorf("Expected ReadTimeout 30s, got %v", config.ReadTimeout)
	}

	if config.WriteTimeout != 30*time.Second {
		t.Errorf("Expected WriteTimeout 30s, got %v", config.WriteTimeout)
	}

	if config.IdleTimeout != 60*time.Second {
		t.Errorf("Expected IdleTimeout 60s, got %v", config.IdleTimeout)
	}

	if config.MaxHeaderBytes != 1<<20 {
		t.Errorf("Expected MaxHeaderBytes 1MB, got %d", config.MaxHeaderBytes)
	}

	if config.MaxConcurrentStreams != 100 {
		t.Errorf("Expected MaxConcurrentStreams 100, got %d", config.MaxConcurrentStreams)
	}

	if config.MaxFrameSize != 16384 {
		t.Errorf("Expected MaxFrameSize 16384, got %d", config.MaxFrameSize)
	}

	if config.InitialWindowSize != 65535 {
		t.Errorf("Expected InitialWindowSize 65535, got %d", config.InitialWindowSize)
	}

	if config.Logger == nil {
		t.Error("Expected default logger to be set")
	}

	if config.DisableKeepAlive {
		t.Error("Expected DisableKeepAlive to be false by default")
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		validate func(*testing.T, Config)
	}{
		{
			name: "empty addr gets default",
			config: Config{
				Addr: "",
			},
			validate: func(t *testing.T, c Config) {
				if c.Addr != ":8080" {
					t.Errorf("Expected addr :8080, got %s", c.Addr)
				}
			},
		},
		{
			name: "small MaxFrameSize gets adjusted",
			config: Config{
				MaxFrameSize: 100,
			},
			validate: func(t *testing.T, c Config) {
				if c.MaxFrameSize != 16384 {
					t.Errorf("Expected MaxFrameSize 16384, got %d", c.MaxFrameSize)
				}
			},
		},
		{
			name: "large MaxFrameSize gets capped",
			config: Config{
				MaxFrameSize: 1 << 25,
			},
			validate: func(t *testing.T, c Config) {
				expected := uint32((1 << 24) - 1)
				if c.MaxFrameSize != expected {
					t.Errorf("Expected MaxFrameSize %d, got %d", expected, c.MaxFrameSize)
				}
			},
		},
		{
			name: "zero InitialWindowSize gets default",
			config: Config{
				InitialWindowSize: 0,
			},
			validate: func(t *testing.T, c Config) {
				if c.InitialWindowSize != 65535 {
					t.Errorf("Expected InitialWindowSize 65535, got %d", c.InitialWindowSize)
				}
			},
		},
		{
			name: "zero MaxConcurrentStreams gets default",
			config: Config{
				MaxConcurrentStreams: 0,
			},
			validate: func(t *testing.T, c Config) {
				if c.MaxConcurrentStreams != 100 {
					t.Errorf("Expected MaxConcurrentStreams 100, got %d", c.MaxConcurrentStreams)
				}
			},
		},
		{
			name: "nil Logger gets default",
			config: Config{
				Logger: nil,
			},
			validate: func(t *testing.T, c Config) {
				if c.Logger == nil {
					t.Error("Expected Logger to be set")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if err != nil {
				t.Errorf("Validate() error = %v", err)
			}
			tt.validate(t, tt.config)
		})
	}
}

func TestConfig_CustomValues(t *testing.T) {
	config := Config{
		Addr:                 ":9090",
		Multicore:            false,
		NumEventLoop:         4,
		ReusePort:            false,
		ReadTimeout:          10 * time.Second,
		WriteTimeout:         10 * time.Second,
		IdleTimeout:          20 * time.Second,
		MaxHeaderBytes:       1 << 21,
		MaxConcurrentStreams: 200,
		MaxFrameSize:         32768,
		InitialWindowSize:    131070,
		DisableKeepAlive:     true,
	}

	err := config.Validate()
	if err != nil {
		t.Errorf("Validate() error = %v", err)
	}

	if config.Addr != ":9090" {
		t.Errorf("Expected addr :9090, got %s", config.Addr)
	}

	if config.Multicore {
		t.Error("Expected multicore to be false")
	}

	if config.NumEventLoop != 4 {
		t.Errorf("Expected NumEventLoop 4, got %d", config.NumEventLoop)
	}

	if config.MaxConcurrentStreams != 200 {
		t.Errorf("Expected MaxConcurrentStreams 200, got %d", config.MaxConcurrentStreams)
	}
}

