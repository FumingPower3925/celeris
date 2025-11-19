// Package mux provides protocol multiplexing for HTTP/1.1 and HTTP/2.
// It detects the protocol version from the first bytes of a connection and routes accordingly.
package mux

import (
	"bytes"
	"context"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
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
	// Enough bytes to detect "GET ", "POST", "PRI " etc.
	minDetectBytes = 4
)

// Server is a multiplexing gnet EventHandler that routes connections to HTTP/1.1 or HTTP/2.
// It performs protocol detection on initial connection bytes and delegates to the appropriate handler.
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

	enableH1       bool
	enableH2       bool
	maxConnections uint32
	activeConns    uint32 // Atomic counter for active connections

	// Connection queuing for backpressure
	connectionQueue chan gnet.Conn
	queueSize       int
	queueMu         sync.RWMutex
}

// verboseConnLogging controls per-connection logging to avoid formatting overhead under load.
// When enabled, it provides detailed logs for connection handling but may impact performance.
const verboseConnLogging = false

// silentGnetLogger is a logger that discards all gnet output to hide internal messages
type silentGnetLogger struct{}

func (s silentGnetLogger) Debugf(_ string, _ ...any) {}
func (s silentGnetLogger) Infof(_ string, _ ...any)  {}
func (s silentGnetLogger) Warnf(_ string, _ ...any)  {}
func (s silentGnetLogger) Errorf(_ string, _ ...any) {}
func (s silentGnetLogger) Fatalf(_ string, _ ...any) {}

// connSession tracks per-connection state during protocol detection and handling.
// It maintains the protocol detection buffer and references to the appropriate connection handlers.
type connSession struct {
	buffer   []byte
	detected bool
	isH2     bool
	h1Conn   *h1.Connection
	h2Conn   *h2transport.Connection
}

// Config defines the configuration options for the protocol multiplexer.
// It controls which protocols are enabled and how connections are handled.
type Config struct {
	Addr                 string
	Multicore            bool
	NumEventLoop         int
	ReusePort            bool
	Logger               *log.Logger
	MaxConcurrentStreams uint32
	MaxConnections       uint32 // Maximum concurrent connections
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

	// Set default max connections if not specified
	if config.MaxConnections == 0 {
		config.MaxConnections = 10000 // Default to 10k connections
	}

	s := &Server{
		handler:        handler,
		ctx:            ctx,
		cancel:         cancel,
		addr:           config.Addr,
		multicore:      config.Multicore,
		numEventLoop:   config.NumEventLoop,
		reusePort:      config.ReusePort,
		logger:         config.Logger,
		enableH1:       config.EnableH1,
		enableH2:       config.EnableH2,
		maxConnections: config.MaxConnections,
		activeConns:    0,
		// Initialize connection queue with 10% of max connections as buffer
		connectionQueue: make(chan gnet.Conn, config.MaxConnections/10),
		queueSize:       int(config.MaxConnections / 10),
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
			MaxConnections:       config.MaxConnections,
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
		// Optimize for high throughput
		gnet.WithTCPNoDelay(gnet.TCPNoDelay),
		// Massive buffer sizes for maximum RPS
		gnet.WithSocketRecvBuffer(64 << 20), // 64 MiB for extreme performance
		gnet.WithSocketSendBuffer(64 << 20), // 64 MiB for extreme performance
		// Extended keep-alive for connection reuse
		gnet.WithTCPKeepAlive(time.Minute * 30), // Extended keep-alive
		// Use silent logger to eliminate I/O overhead
		gnet.WithLogger(silentGnetLogger{}),
		// Maximum concurrency settings
		gnet.WithLockOSThread(false), // Allow better load balancing
		// Large buffers for high RPS
		gnet.WithReadBufferCap(1024 << 10),  // 1 MB read buffer
		gnet.WithWriteBufferCap(1024 << 10), // 1 MB write buffer
		// Enable all performance optimizations
		gnet.WithTicker(true),                   // Enable ticker
		gnet.WithLoadBalancing(gnet.RoundRobin), // Optimize load balancing
		gnet.WithNumEventLoop(runtime.NumCPU()), // Use all CPU cores
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
	s.logger.Printf("Server is listening on %s (multicore: %v)", s.addr, s.multicore)

	// Start the server in a goroutine since gnet.Run is blocking
	go func() {
		_ = gnet.Run(s, "tcp://"+s.addr, options...)
	}()

	return nil
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

	// Start connection queue processor
	go s.processConnectionQueue()

	return gnet.None
}

// OnOpen is called when a new connection is opened.
func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	// Check connection limit BEFORE incrementing counter to avoid race condition
	currentConns := atomic.LoadUint32(&s.activeConns)
	if currentConns >= s.maxConnections {
		// Try to queue the connection if we have space
		s.queueMu.RLock()
		queueLen := len(s.connectionQueue)
		s.queueMu.RUnlock()

		if queueLen < s.queueSize {
			// Queue the connection for later processing
			select {
			case s.connectionQueue <- c:
				s.logger.Printf("Connection queued from %s (queue: %d/%d, active: %d/%d)",
					c.RemoteAddr().String(), queueLen+1, s.queueSize, currentConns, s.maxConnections)
				return nil, gnet.None
			default:
				// Queue is full, reject connection
			}
		}

		// Reject connection - send 503 Service Unavailable
		s.logger.Printf("Connection rejected from %s: too many connections (%d/%d, queue: %d/%d)",
			c.RemoteAddr().String(), currentConns, s.maxConnections, queueLen, s.queueSize)

		// Send HTTP 503 response before closing
		response := "HTTP/1.1 503 Service Unavailable\r\n" +
			"Content-Type: text/plain\r\n" +
			"Content-Length: 19\r\n" +
			"Connection: close\r\n" +
			"\r\n" +
			"Service Unavailable"

		return []byte(response), gnet.Close
	}

	// Now safely increment the counter
	atomic.AddUint32(&s.activeConns, 1)

	session := &connSession{
		buffer: make([]byte, 0, minDetectBytes),
	}
	s.connections.Store(c, session)
	if verboseConnLogging {
		s.logger.Printf("New connection from %s (%d/%d active)",
			c.RemoteAddr().String(), currentConns+1, s.maxConnections)
	}
	return nil, gnet.None
}

// processConnectionQueue processes queued connections when capacity becomes available
func (s *Server) processConnectionQueue() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case queuedConn := <-s.connectionQueue:
			// Check if we can accept this connection now
			currentConns := atomic.LoadUint32(&s.activeConns)
			if currentConns < s.maxConnections {
				// Accept the queued connection
				atomic.AddUint32(&s.activeConns, 1)

				session := &connSession{
					buffer: make([]byte, 0, minDetectBytes),
				}
				s.connections.Store(queuedConn, session)

				s.logger.Printf("Queued connection accepted from %s (%d/%d active)",
					queuedConn.RemoteAddr().String(), currentConns+1, s.maxConnections)
			} else {
				// Still no capacity, close the queued connection
				s.logger.Printf("Queued connection rejected from %s: still no capacity (%d/%d)",
					queuedConn.RemoteAddr().String(), currentConns, s.maxConnections)
				_ = queuedConn.Close()
			}
		}
	}
}

// OnClose is called when a connection is closed.
func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	sessionValue, ok := s.connections.Load(c)
	if !ok {
		// Connection wasn't tracked, still decrement counter to be safe
		atomic.AddUint32(&s.activeConns, ^uint32(0)) // Decrement counter
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

	// Decrement active connection counter
	currentConns := atomic.AddUint32(&s.activeConns, ^uint32(0)) // Decrement counter

	if verboseConnLogging {
		if err != nil {
			s.logger.Printf("Connection closed with error: %v (%d/%d active)",
				err, currentConns, s.maxConnections)
		} else {
			s.logger.Printf("Connection closed from %s (%d/%d active)",
				c.RemoteAddr().String(), currentConns, s.maxConnections)
		}
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
		// Send 400 Bad Request instead of closing
		response := "HTTP/1.1 400 Bad Request\r\n" +
			"Content-Type: text/plain\r\n" +
			"Content-Length: 12\r\n" +
			"Connection: close\r\n" +
			"\r\n" +
			"Bad Request"
		_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
			// Close connection after sending response
			_ = c.Close()
			return nil
		})
		return gnet.None
	}

	session := sessionValue.(*connSession)

	if !session.detected {
		// Still detecting protocol - use non-blocking approach
		buf, err := c.Next(-1)
		if err != nil {
			s.logger.Printf("Error reading data: %v", err)
			// Send 400 Bad Request instead of closing
			response := "HTTP/1.1 400 Bad Request\r\n" +
				"Content-Type: text/plain\r\n" +
				"Content-Length: 12\r\n" +
				"Connection: close\r\n" +
				"\r\n" +
				"Bad Request"
			_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
				// Close connection after sending response
				_ = c.Close()
				return nil
			})
			return gnet.None
		}
		if len(buf) == 0 {
			return gnet.None
		}

		session.buffer = append(session.buffer, buf...)

		// Try to detect protocol - optimized for high performance
		if len(session.buffer) >= minDetectBytes {
			// Fast protocol detection using first byte for performance
			firstByte := session.buffer[0]
			var isLikelyH1 bool

			switch firstByte {
			case 'G': // GET
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("GET "))
			case 'P': // POST, PUT, PATCH
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("POST ")) ||
					bytes.HasPrefix(session.buffer, []byte("PUT ")) ||
					bytes.HasPrefix(session.buffer, []byte("PATCH "))
			case 'H': // HEAD
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("HEAD "))
			case 'D': // DELETE
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("DELETE "))
			case 'O': // OPTIONS
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("OPTIONS "))
			case 'T': // TRACE
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("TRACE "))
			case 'C': // CONNECT
				isLikelyH1 = bytes.HasPrefix(session.buffer, []byte("CONNECT "))
			}

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
					// Send 400 Bad Request for invalid HTTP/2 preface
					response := "HTTP/1.1 400 Bad Request\r\n" +
						"Content-Type: text/plain\r\n" +
						"Content-Length: 12\r\n" +
						"Connection: close\r\n" +
						"\r\n" +
						"Bad Request"
					_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
						// Close connection after sending response
						_ = c.Close()
						return nil
					})
					return gnet.None
				}
			}
			isH2 := len(session.buffer) >= len(http2Preface) && bytes.HasPrefix(session.buffer, []byte(http2Preface))

			switch {
			case isH2:
				// HTTP/2 detected
				if !s.enableH2 {
					s.logger.Printf("HTTP/2 connection rejected (H2 disabled)")
					// Send 400 Bad Request instead of closing
					response := "HTTP/1.1 400 Bad Request\r\n" +
						"Content-Type: text/plain\r\n" +
						"Content-Length: 12\r\n" +
						"Connection: close\r\n" +
						"\r\n" +
						"Bad Request"
					_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
						// Close connection after sending response
						_ = c.Close()
						return nil
					})
					return gnet.None
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
					// Send 400 Bad Request instead of closing
					response := "HTTP/1.1 400 Bad Request\r\n" +
						"Content-Type: text/plain\r\n" +
						"Content-Length: 12\r\n" +
						"Connection: close\r\n" +
						"\r\n" +
						"Bad Request"
					_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
						// Close connection after sending response
						_ = c.Close()
						return nil
					})
					return gnet.None
				}

				session.buffer = nil
			case isLikelyH1:
				// HTTP/1.1 detected (valid HTTP method detected)
				if !s.enableH1 {
					s.logger.Printf("HTTP/1.1 connection rejected (H1 disabled)")
					// Send 400 Bad Request instead of closing
					response := "HTTP/1.1 400 Bad Request\r\n" +
						"Content-Type: text/plain\r\n" +
						"Content-Length: 12\r\n" +
						"Connection: close\r\n" +
						"\r\n" +
						"Bad Request"
					_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
						// Close connection after sending response
						_ = c.Close()
						return nil
					})
					return gnet.None
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
					// Send 400 Bad Request instead of closing
					response := "HTTP/1.1 400 Bad Request\r\n" +
						"Content-Type: text/plain\r\n" +
						"Content-Length: 12\r\n" +
						"Connection: close\r\n" +
						"\r\n" +
						"Bad Request"
					_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
						// Close connection after sending response
						_ = c.Close()
						return nil
					})
					return gnet.None
				}

				session.buffer = nil
			case mightBeH2:
				// Looks like it might be HTTP/2 but incomplete, wait for full preface
				if len(session.buffer) >= len(http2Preface) {
					// We have enough bytes but it's not valid HTTP/2
					// Delegate invalid preface handling to H2 transport for compliant GOAWAY
					if !s.enableH2 {
						// Send 400 Bad Request instead of closing
						response := "HTTP/1.1 400 Bad Request\r\n" +
							"Content-Type: text/plain\r\n" +
							"Content-Length: 12\r\n" +
							"Connection: close\r\n" +
							"\r\n" +
							"Bad Request"
						_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
							// Close connection after sending response
							_ = c.Close()
							return nil
						})
						return gnet.None
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
				// Send 400 Bad Request for invalid connection preface
				response := "HTTP/1.1 400 Bad Request\r\n" +
					"Content-Type: text/plain\r\n" +
					"Content-Length: 12\r\n" +
					"Connection: close\r\n" +
					"\r\n" +
					"Bad Request"
				_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
					// Close connection after sending response
					_ = c.Close()
					return nil
				})
				return gnet.None
			}
		} else {
			// Need more data to detect
			return gnet.None
		}
	} else {
		// Protocol already detected, read data and process directly
		// Don't delegate to sub-servers' OnTraffic as it causes connection drops
		buf, err := c.Next(-1)
		if err != nil {
			// Connection error - close silently as connection is likely already dead
			return gnet.Close
		}
		if len(buf) == 0 {
			// No data available yet, wait for more
			return gnet.None
		}

		// Process data directly with proper connection handling
		if session.isH2 && s.h2Server != nil && session.h2Conn != nil {
			// HTTP/2 handles responses internally via frames
			if err := session.h2Conn.HandleData(s.ctx, buf); err != nil {
				s.logger.Printf("Error handling H2 data: %v", err)
			}
		} else if !session.isH2 && s.h1Server != nil && session.h1Conn != nil {
			// HTTP/1.1 - process synchronously to ensure response is queued
			// The response writer uses AsyncWritev, but processing synchronously
			// ensures the write is at least queued before we return
			if err := session.h1Conn.HandleData(buf); err != nil {
				// Check if this is a normal connection close request
				if err.Error() == "connection close requested" {
					// Normal graceful close
					return gnet.Close
				}
				s.logger.Printf("Error handling H1 data: %v", err)
				// Send error response
				response := "HTTP/1.1 400 Bad Request\r\n" +
					"Content-Type: text/plain\r\n" +
					"Content-Length: 12\r\n" +
					"Connection: close\r\n" +
					"\r\n" +
					"Bad Request"
				_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
					_ = c.Close()
					return nil
				})
			}
		}
	}

	return gnet.None
}
