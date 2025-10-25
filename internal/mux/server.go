// Package mux provides protocol multiplexing for HTTP/1.1 and HTTP/2.
package mux

import (
	"bytes"
	"context"
	"log"
	"sync"
	"time"

	"github.com/albertbausili/celeris/internal/h1"
	"github.com/albertbausili/celeris/internal/h2/stream"
	h2transport "github.com/albertbausili/celeris/internal/h2/transport"
	"github.com/panjf2000/gnet/v2"
)

const (
	// HTTP/2 connection preface
	http2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
	// Minimum bytes needed to detect protocol (reduced to detect faster)
	minDetectBytes = 4 // Enough to detect "GET ", "POST", "PRI " etc
)

// Server is a multiplexing gnet EventHandler that routes to HTTP/1.1 or HTTP/2.
type Server struct {
	gnet.BuiltinEventEngine

	h1Server *h1.Server
	h2Server *h2transport.Server

	handler       stream.Handler
	connections   sync.Map // map[gnet.Conn]*connSession
	ctx           context.Context
	cancel        context.CancelFunc
	addr          string
	multicore     bool
	numEventLoop  int
	reusePort     bool
	logger        *log.Logger
	engine        gnet.Engine
	engineStarted bool

	enableH1 bool
	enableH2 bool
}

// connSession tracks per-connection state during protocol detection.
type connSession struct {
	buffer   []byte
	detected bool
	isH2     bool
	h1Conn   *h1.Connection
	h2Conn   *h2transport.Connection
}

// Config holds multiplexer configuration.
type Config struct {
	Addr                 string
	Multicore            bool
	NumEventLoop         int
	ReusePort            bool
	Logger               *log.Logger
	MaxConcurrentStreams uint32
	EnableH1             bool
	EnableH2             bool
}

// NewServer creates a new multiplexing server.
func NewServer(handler stream.Handler, config Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	if config.Logger == nil {
		config.Logger = log.Default()
	}

	// Ensure at least one protocol is enabled
	if !config.EnableH1 && !config.EnableH2 {
		config.EnableH2 = true
	}

	s := &Server{
		handler:      handler,
		ctx:          ctx,
		cancel:       cancel,
		addr:         config.Addr,
		multicore:    config.Multicore,
		numEventLoop: config.NumEventLoop,
		reusePort:    config.ReusePort,
		logger:       config.Logger,
		enableH1:     config.EnableH1,
		enableH2:     config.EnableH2,
	}

	// Create HTTP/2 server if enabled
	if config.EnableH2 {
		s.h2Server = h2transport.NewServer(handler, h2transport.Config{
			Addr:                 config.Addr,
			Multicore:            config.Multicore,
			NumEventLoop:         config.NumEventLoop,
			ReusePort:            config.ReusePort,
			Logger:               config.Logger,
			MaxConcurrentStreams: config.MaxConcurrentStreams,
		})
	}

	// Create HTTP/1.1 server if enabled
	if config.EnableH1 {
		s.h1Server = h1.NewServer(ctx, handler, config.Logger)
	}

	return s
}

// Start starts the multiplexing server.
func (s *Server) Start() error {
	options := []gnet.Option{
		gnet.WithMulticore(s.multicore),
		gnet.WithReusePort(s.reusePort),
	}

	if s.numEventLoop > 0 {
		options = append(options, gnet.WithNumEventLoop(s.numEventLoop))
	}

	var protocols string
	switch {
	case s.enableH1 && s.enableH2:
		protocols = "HTTP/1.1 and HTTP/2"
	case s.enableH1:
		protocols = "HTTP/1.1"
	default:
		protocols = "HTTP/2"
	}

	s.logger.Printf("Starting server on %s (%s)", s.addr, protocols)
	return gnet.Run(s, "tcp://"+s.addr, options...)
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Println("Initiating graceful shutdown...")
	s.cancel()

	// Close all connections first
	s.connections.Range(func(key, _ interface{}) bool {
		if gnetConn, ok := key.(gnet.Conn); ok {
			_ = gnetConn.Close()
		}
		return true
	})

	// Stop the gnet engine if it was started
	// gnet.Engine.Stop() is blocking and waits for all event loops to finish
	if s.engineStarted {
		if err := s.engine.Stop(ctx); err != nil {
			s.logger.Printf("Error stopping gnet engine: %v", err)
			return err
		}
	}

	s.logger.Println("Server shutdown complete")
	return nil
}

// OnBoot is called when the server is ready to accept connections.
func (s *Server) OnBoot(eng gnet.Engine) gnet.Action {
	s.engine = eng
	s.engineStarted = true

	// Initialize sub-servers if needed
	if s.h2Server != nil {
		s.h2Server.OnBoot(eng)
	}

	s.logger.Printf("Server is listening on %s (multicore: %v)", s.addr, s.multicore)
	return gnet.None
}

// OnOpen is called when a new connection is opened.
func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	session := &connSession{
		buffer: make([]byte, 0, minDetectBytes),
	}
	s.connections.Store(c, session)
	s.logger.Printf("New connection from %s", c.RemoteAddr().String())
	return nil, gnet.None
}

// OnClose is called when a connection is closed.
func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	sessionValue, ok := s.connections.Load(c)
	if !ok {
		return gnet.None
	}

	session := sessionValue.(*connSession)

	// Delegate to appropriate handler
	if session.detected {
		if session.isH2 && s.h2Server != nil {
			s.h2Server.OnClose(c, err)
		} else if !session.isH2 && s.h1Server != nil {
			s.h1Server.OnClose(c, err)
		}
	}

	s.connections.Delete(c)

	if err != nil {
		s.logger.Printf("Connection closed with error: %v", err)
	} else {
		s.logger.Printf("Connection closed from %s", c.RemoteAddr().String())
	}

	return gnet.None
}

// OnTraffic is called when data is received on a connection.
// OnTraffic is called when data is received on a connection.
//
//nolint:gocyclo // Protocol detection/delegation needs multiple branches for correctness and clarity.
func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	sessionValue, ok := s.connections.Load(c)
	if !ok {
		s.logger.Printf("Connection not found in map")
		return gnet.Close
	}

	session := sessionValue.(*connSession)

	if !session.detected {
		// Still detecting protocol
		buf, err := c.Next(-1)
		if err != nil {
			s.logger.Printf("Error reading data: %v", err)
			return gnet.Close
		}

		session.buffer = append(session.buffer, buf...)

		// Try to detect protocol
		if len(session.buffer) >= minDetectBytes {
			// Check if this starts with valid HTTP/1.1 request line (GET, POST, etc)
			isLikelyH1 := bytes.HasPrefix(session.buffer, []byte("GET ")) ||
				bytes.HasPrefix(session.buffer, []byte("POST ")) ||
				bytes.HasPrefix(session.buffer, []byte("PUT ")) ||
				bytes.HasPrefix(session.buffer, []byte("DELETE ")) ||
				bytes.HasPrefix(session.buffer, []byte("HEAD ")) ||
				bytes.HasPrefix(session.buffer, []byte("OPTIONS ")) ||
				bytes.HasPrefix(session.buffer, []byte("PATCH ")) ||
				bytes.HasPrefix(session.buffer, []byte("TRACE ")) ||
				bytes.HasPrefix(session.buffer, []byte("CONNECT "))

			// Check for HTTP/2 preface - might be starting but incomplete
			mightBeH2 := bytes.HasPrefix(session.buffer, []byte("PRI "))
			// If it starts like H2 but already deviates from the expected preface, treat as invalid preface
			if mightBeH2 {
				// If current buffer is not a prefix of the expected preface, it's invalid
				if len(session.buffer) <= len(http2Preface) && !bytes.HasPrefix([]byte(http2Preface), session.buffer) {
					if s.enableH2 {
						maxStreams := uint32(0)
						if s.h2Server != nil {
							maxStreams = s.h2Server.GetMaxConcurrentStreams()
						}
						session.h2Conn = h2transport.NewConnection(c, s.handler, s.logger, maxStreams)
						s.h2Server.StoreConnection(c, session.h2Conn)
						_ = session.h2Conn.SendGoAway(0, 1, []byte("invalid connection preface"))
						// allow GOAWAY to flush; also schedule close here as a fallback
						time.AfterFunc(5*time.Millisecond, func() { _ = c.Close() })
						return gnet.None
					}
					return gnet.Close
				}
			}
			isH2 := len(session.buffer) >= len(http2Preface) && bytes.HasPrefix(session.buffer, []byte(http2Preface))

			switch {
			case isH2:
				// HTTP/2 detected
				if !s.enableH2 {
					s.logger.Printf("HTTP/2 connection rejected (H2 disabled)")
					return gnet.Close
				}

				session.detected = true
				session.isH2 = true
				s.logger.Printf("Detected HTTP/2 from %s", c.RemoteAddr().String())

				// Store the connection in h2Server's connections map first
				// Ensure the connection inherits the configured MAX_CONCURRENT_STREAMS
				maxStreams := uint32(0)
				if s.h2Server != nil {
					maxStreams = s.h2Server.GetMaxConcurrentStreams()
				}
				session.h2Conn = h2transport.NewConnection(c, s.handler, s.logger, maxStreams)
				// Store in h2Server's internal connections map
				s.h2Server.StoreConnection(c, session.h2Conn)

				// Process buffered data
				if err := session.h2Conn.HandleData(s.ctx, session.buffer); err != nil {
					s.logger.Printf("Error handling H2 data: %v", err)
					return gnet.Close
				}

				session.buffer = nil
			case isLikelyH1:
				// HTTP/1.1 detected (valid HTTP method detected)
				if !s.enableH1 {
					s.logger.Printf("HTTP/1.1 connection rejected (H1 disabled)")
					return gnet.Close
				}

				session.detected = true
				session.isH2 = false
				s.logger.Printf("Detected HTTP/1.1 from %s", c.RemoteAddr().String())

				// Create HTTP/1.1 connection and hand off buffered data
				session.h1Conn = h1.NewConnection(s.ctx, c, s.handler, s.logger)
				// Store in h1Server's internal connections map
				s.h1Server.StoreConnection(c, session.h1Conn)

				// Process buffered data
				if err := session.h1Conn.HandleData(session.buffer); err != nil {
					s.logger.Printf("Error handling H1 data: %v", err)
					return gnet.Close
				}

				session.buffer = nil
			case mightBeH2:
				// Looks like it might be HTTP/2 but incomplete, wait for full preface
				if len(session.buffer) >= len(http2Preface) {
					// We have enough bytes but it's not valid HTTP/2
					// Delegate invalid preface handling to H2 transport for compliant GOAWAY
					if !s.enableH2 {
						return gnet.Close
					}
					session.detected = true
					session.isH2 = true
					maxStreams := uint32(0)
					if s.h2Server != nil {
						maxStreams = s.h2Server.GetMaxConcurrentStreams()
					}
					session.h2Conn = h2transport.NewConnection(c, s.handler, s.logger, maxStreams)
					s.h2Server.StoreConnection(c, session.h2Conn)
					if err := session.h2Conn.HandleData(s.ctx, session.buffer); err != nil {
						s.logger.Printf("Error handling H2 data: %v", err)
					}
					session.buffer = nil
					// let transport close after sending GOAWAY; also fallback schedule
					time.AfterFunc(5*time.Millisecond, func() { _ = c.Close() })
					return gnet.None
				}
				// Wait for more data
				return gnet.None
			default:
				// Neither valid HTTP/2 preface nor valid HTTP/1.1 request line
				s.logger.Printf("Invalid connection preface from %s", c.RemoteAddr().String())
				if s.enableH2 {
					// Try treating it as H2 invalid preface to emit GOAWAY per spec
					session.detected = true
					session.isH2 = true
					maxStreams := uint32(0)
					if s.h2Server != nil {
						maxStreams = s.h2Server.GetMaxConcurrentStreams()
					}
					session.h2Conn = h2transport.NewConnection(c, s.handler, s.logger, maxStreams)
					s.h2Server.StoreConnection(c, session.h2Conn)
					if err := session.h2Conn.HandleData(s.ctx, session.buffer); err != nil {
						s.logger.Printf("Error handling H2 data: %v", err)
					}
					session.buffer = nil
					// allow GOAWAY to flush; schedule close
					time.AfterFunc(5*time.Millisecond, func() { _ = c.Close() })
					return gnet.None
				}
				return gnet.Close
			}
		} else {
			// Need more data to detect
			return gnet.None
		}
	} else {
		// Protocol already detected, delegate to appropriate handler
		if session.isH2 && s.h2Server != nil {
			return s.h2Server.OnTraffic(c)
		} else if !session.isH2 && s.h1Server != nil {
			return s.h1Server.OnTraffic(c)
		}
	}

	return gnet.None
}
