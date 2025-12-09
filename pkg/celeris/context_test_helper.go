package celeris

import (
	"context"

	"github.com/FumingPower3925/celeris/internal/h2/stream"
)

// NewTestContext creates a properly initialized Context for testing purposes.
// This is exported to allow test packages to create valid contexts without
// dealing with private fields.
func NewTestContext(ctx context.Context, s *stream.Stream, writeResponse func(uint32, int, [][2]string, []byte) error) *Context {
	return newContext(ctx, s, writeResponse)
}
