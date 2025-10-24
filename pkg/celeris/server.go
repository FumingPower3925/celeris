package celeris

import (
	"context"
	"fmt"

	"github.com/albertbausili/celeris/internal/h2/stream"
	"github.com/albertbausili/celeris/internal/mux"
)

// Server represents a server instance supporting HTTP/1.1 and/or HTTP/2.
type Server struct {
	config    Config
	handler   Handler
	transport *mux.Server
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

	streamHandler := &streamHandlerAdapter{
		handler: s.handler,
	}

	s.transport = mux.NewServer(streamHandler, mux.Config{
		Addr:                 s.config.Addr,
		Multicore:            s.config.Multicore,
		NumEventLoop:         s.config.NumEventLoop,
		ReusePort:            s.config.ReusePort,
		Logger:               s.config.Logger,
		MaxConcurrentStreams: s.config.MaxConcurrentStreams,
		EnableH1:             s.config.EnableH1,
		EnableH2:             s.config.EnableH2,
	})

	return s.transport.Start()
}

// Stop gracefully shuts down the server without interrupting active connections.
func (s *Server) Stop(ctx context.Context) error {
	if s.transport != nil {
		return s.transport.Stop(ctx)
	}
	return nil
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
	writeResponse := func(streamID uint32, status int, headers [][2]string, body []byte) error {
		if s.ResponseWriter == nil {
			return fmt.Errorf("no response writer available")
		}

		return s.ResponseWriter.WriteResponse(streamID, status, headers, body)
	}

	pushPromise := func(streamID uint32, path string, headers [][2]string) error {
		if a.processor != nil {
			return a.processor.PushPromise(streamID, path, headers)
		}
		return fmt.Errorf("no processor available for push promise")
	}

	celerisCtx := newContext(ctx, s, writeResponse)
	celerisCtx.pushPromise = pushPromise

	return a.handler.ServeHTTP2(celerisCtx)
}
