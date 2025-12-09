package celeris

import (
	"context"
	"fmt"

	"github.com/FumingPower3925/celeris/internal/date"
	"github.com/FumingPower3925/celeris/internal/h1"
	"github.com/FumingPower3925/celeris/internal/h2/stream"
	h2transport "github.com/FumingPower3925/celeris/internal/h2/transport"
	"github.com/FumingPower3925/celeris/internal/mux"
)

// Server represents a server instance supporting HTTP/1.1 and/or HTTP/2.
type Server struct {
	config    Config
	handler   Handler
	transport interface {
		Start() error
		Stop(context.Context) error
	}
	h1Server       *h1.Server
	h2Server       *h2transport.Server
	muxServer      *mux.Server
	stopDateTicker func()
}

// New creates a new Server with the provided configuration.
func New(config Config) *Server {
	if err := config.Validate(); err != nil {
		panic(err)
	}

	return &Server{
		config: config,
	}
}

// NewWithDefaults creates a new Server with default configuration.
func NewWithDefaults() *Server {
	return New(DefaultConfig())
}

// Handler sets the request handler and returns the server for method chaining.
func (s *Server) Handler(handler Handler) *Server {
	s.handler = handler
	return s
}

// ListenAndServe sets the handler and starts the server.
func (s *Server) ListenAndServe(handler Handler) error {
	s.handler = handler
	return s.Start()
}

// Start begins accepting HTTP/1.1 and/or HTTP/2 connections.
func (s *Server) Start() error {
	if s.handler == nil {
		return fmt.Errorf("handler not set")
	}

	// Start date ticker for optimized Date header generation
	s.stopDateTicker = date.StartTicker()

	streamHandler := &streamHandlerAdapter{
		handler: s.handler,
	}

	ctx := context.Background()

	// When only one protocol is enabled, bypass the mux for better performance
	if s.config.EnableH1 && !s.config.EnableH2 {
		// HTTP/1.1 only - use direct server
		s.h1Server = h1.NewServerWithConfig(ctx, streamHandler, h1.Config{
			Addr:           s.config.Addr,
			Multicore:      s.config.Multicore,
			NumEventLoop:   s.config.NumEventLoop,
			ReusePort:      s.config.ReusePort,
			Logger:         s.config.Logger,
			MaxConnections: s.config.MaxConnections,
		})
		s.transport = s.h1Server
		return s.transport.Start()
	} else if s.config.EnableH2 && !s.config.EnableH1 {
		// HTTP/2 only - use direct server
		s.h2Server = h2transport.NewServer(streamHandler, h2transport.Config{
			Addr:                 s.config.Addr,
			Multicore:            s.config.Multicore,
			NumEventLoop:         s.config.NumEventLoop,
			ReusePort:            s.config.ReusePort,
			Logger:               s.config.Logger,
			MaxConcurrentStreams: s.config.MaxConcurrentStreams,
			MaxConnections:       s.config.MaxConnections,
		})
		s.transport = s.h2Server
		return s.transport.Start()
	}
	// Both protocols enabled - use mux
	s.muxServer = mux.NewServer(streamHandler, mux.Config{
		Addr:                 s.config.Addr,
		Multicore:            s.config.Multicore,
		NumEventLoop:         s.config.NumEventLoop,
		ReusePort:            s.config.ReusePort,
		Logger:               s.config.Logger,
		MaxConcurrentStreams: s.config.MaxConcurrentStreams,
		MaxConnections:       s.config.MaxConnections,
		EnableH1:             s.config.EnableH1,
		EnableH2:             s.config.EnableH2,
	})
	s.transport = s.muxServer
	return s.transport.Start()
}

// Stop gracefully shuts down the server without interrupting active connections.
func (s *Server) Stop(ctx context.Context) error {
	if s.stopDateTicker != nil {
		s.stopDateTicker()
		s.stopDateTicker = nil
	}
	if s.transport != nil {
		return s.transport.Stop(ctx)
	}
	return nil
}

// Addr returns the server address.
func (s *Server) Addr() string {
	return s.config.Addr
}

type streamHandlerAdapter struct {
	handler     Handler
	processor   *stream.Processor
	currentConn stream.ResponseWriter
}

func (a *streamHandlerAdapter) SetProcessor(p *stream.Processor) {
	a.processor = p
}

func (a *streamHandlerAdapter) SetConnection(conn stream.ResponseWriter) {
	a.currentConn = conn
}

func (a *streamHandlerAdapter) HandleStream(ctx context.Context, s *stream.Stream) error {
	writeResponse := func(_ uint32, status int, headers [][2]string, body []byte) error {
		if s.ResponseWriter == nil {
			return fmt.Errorf("no response writer available")
		}

		return s.ResponseWriter.WriteResponse(s, status, headers, body)
	}

	pushPromise := func(streamID uint32, path string, headers [][2]string) error {
		if a.processor != nil {
			return a.processor.PushPromise(streamID, path, headers)
		}
		return fmt.Errorf("no processor available for push promise")
	}

	celerisCtx := newContext(ctx, s, writeResponse)
	celerisCtx.pushPromise = pushPromise

	err := a.handler.ServeHTTP2(celerisCtx)
	celerisCtx.Release()
	return err
}

// HandleH1Fast allows the adapter to serve an HTTP/1.1 request directly using a pre-built Context.
// This avoids constructing an HTTP-2 stream for the HTTP/1.1 path.
func (a *streamHandlerAdapter) HandleH1Fast(ctx context.Context, method, path, authority string, reqHeaders [][2]string, body []byte, write func(status int, headers [][2]string, body []byte) error) error {
	var c *Context
	if len(reqHeaders) == 0 {
		// Use a lighter H1 context when no headers are inspected
		c = NewContextH1NoHeaders(ctx, method, path, authority, body, write)
	} else {
		c = NewContextH1(ctx, method, path, authority, reqHeaders, body, write)
	}
	err := a.handler.ServeHTTP2(c)
	c.Release()
	return err
}
