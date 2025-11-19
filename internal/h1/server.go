package h1

import (
	"context"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/albertbausili/celeris/internal/h2/stream"
	"github.com/panjf2000/gnet/v2"
)

// verboseLogging controls per-connection log verbosity for HTTP/1.1
const verboseLogging = false

// Config defines the configuration options for the HTTP/1.1 server.
type Config struct {
	Addr           string
	Multicore      bool
	NumEventLoop   int
	ReusePort      bool
	Logger         *log.Logger
	MaxConnections uint32
}

// Server implements gnet.EventHandler for HTTP/1.1.
type Server struct {
	gnet.BuiltinEventEngine
	handler        stream.Handler
	ctx            context.Context
	cancel         context.CancelFunc
	logger         *log.Logger
	addr           string
	multicore      bool
	numEventLoop   int
	reusePort      bool
	maxConnections uint32
	activeConns    uint32 // Atomic counter for active connections
	engine         gnet.Engine
	engineStarted  bool
}

// NewServer creates a new HTTP/1.1 server.
func NewServer(ctx context.Context, handler stream.Handler, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}

	serverCtx, cancel := context.WithCancel(ctx)
	return &Server{
		handler: handler,
		ctx:     serverCtx,
		cancel:  cancel,
		logger:  logger,
	}
}

// NewServerWithConfig creates a new HTTP/1.1 server with full configuration.
func NewServerWithConfig(ctx context.Context, handler stream.Handler, config Config) *Server {
	if config.Logger == nil {
		config.Logger = log.Default()
	}

	serverCtx, cancel := context.WithCancel(ctx)
	return &Server{
		handler:        handler,
		ctx:            serverCtx,
		cancel:         cancel,
		logger:         config.Logger,
		addr:           config.Addr,
		multicore:      config.Multicore,
		numEventLoop:   config.NumEventLoop,
		reusePort:      config.ReusePort,
		maxConnections: config.MaxConnections,
	}
}

// Start starts the HTTP/1.1 server.
func (s *Server) Start() error {
	options := []gnet.Option{
		gnet.WithMulticore(s.multicore),
		gnet.WithReusePort(s.reusePort),
		gnet.WithTCPNoDelay(gnet.TCPNoDelay),
		// Massive buffers for maximum RPS
		gnet.WithSocketRecvBuffer(64 << 20), // 64 MiB for extreme performance
		gnet.WithSocketSendBuffer(64 << 20), // 64 MiB for extreme performance
		// Extended keep-alive for connection reuse
		gnet.WithTCPKeepAlive(time.Minute * 30),
		// Use silent logger to eliminate I/O overhead
		gnet.WithLogger(silentGnetLogger{}),
		// Maximum concurrency settings
		gnet.WithLockOSThread(false),
		// Large buffers for high RPS
		gnet.WithReadBufferCap(1024 << 10),  // 1 MB
		gnet.WithWriteBufferCap(1024 << 10), // 1 MB
		// Enable performance optimizations
		gnet.WithTicker(true),
		gnet.WithLoadBalancing(gnet.RoundRobin),
		gnet.WithNumEventLoop(runtime.NumCPU()),
	}

	if s.numEventLoop > 0 {
		options = append(options, gnet.WithNumEventLoop(s.numEventLoop))
	}

	s.logger.Printf("Starting HTTP/1.1 server on %s", s.addr)
	s.logger.Printf("HTTP/1.1 server is listening on %s (multicore: %v)", s.addr, s.multicore)

	// Start the server in a goroutine since gnet.Run is blocking
	go func() {
		_ = gnet.Run(s, "tcp://"+s.addr, options...)
	}()

	// Mark that the engine has started
	s.engineStarted = true

	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Println("Initiating graceful shutdown...")
	s.cancel()

	if s.engineStarted {
		stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
		defer stopCancel()
		if err := s.engine.Stop(stopCtx); err != nil {
			s.logger.Printf("Error stopping gnet engine: %v", err)
			return err
		}
	}

	s.logger.Println("HTTP/1.1 server shutdown complete")
	return nil
}

// OnBoot is called when the server is ready to accept connections.
func (s *Server) OnBoot(eng gnet.Engine) gnet.Action {
	s.engine = eng
	s.engineStarted = true
	s.logger.Printf("HTTP/1.1 server is listening on %s (multicore: %v)", s.addr, s.multicore)
	return gnet.None
}

// OnShutdown is called when the server is shutting down.
func (s *Server) OnShutdown(_ gnet.Engine) {
	s.engineStarted = false
}

// silentGnetLogger is a logger that discards all gnet output
type silentGnetLogger struct{}

func (s silentGnetLogger) Debugf(_ string, _ ...any) {}
func (s silentGnetLogger) Infof(_ string, _ ...any)  {}
func (s silentGnetLogger) Warnf(_ string, _ ...any)  {}
func (s silentGnetLogger) Errorf(_ string, _ ...any) {}
func (s silentGnetLogger) Fatalf(_ string, _ ...any) {}

// OnOpen is called when a new connection is opened.
func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	// Check connection limit BEFORE incrementing counter to avoid race condition
	if s.maxConnections > 0 {
		currentConns := atomic.LoadUint32(&s.activeConns)
		if currentConns >= s.maxConnections {
			// Reject connection - send 503 Service Unavailable
			s.logger.Printf("Connection rejected from %s: too many connections (%d/%d)",
				c.RemoteAddr().String(), currentConns, s.maxConnections)

			// Send HTTP 503 response before closing
			response := "HTTP/1.1 503 Service Unavailable\r\n" +
				"Content-Type: text/plain\r\n" +
				"Content-Length: 19\r\n" +
				"Connection: close\r\n" +
				"\r\n" +
				"Service Unavailable"

			_ = c.AsyncWrite([]byte(response), func(_ gnet.Conn, _ error) error {
				// Close connection after sending response
				_ = c.Close()
				return nil
			})
			return nil, gnet.None
		}
	}

	// Now safely increment the counter
	atomic.AddUint32(&s.activeConns, 1)

	conn := NewConnection(s.ctx, c, s.handler, s.logger)
	c.SetContext(conn) // Use gnet.Conn.Context() for per-connection storage (best practice)
	if verboseLogging {
		s.logger.Printf("HTTP/1.1 connection from %s", c.RemoteAddr().String())
	}
	return nil, gnet.None
}

// OnClose is called when a connection is closed.
func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	// Decrement connection counter
	atomic.AddUint32(&s.activeConns, ^uint32(0)) // Decrement by 1

	remoteAddr := c.RemoteAddr().String()
	if err != nil {
		s.logger.Printf("[OnClose] Connection closed with error from %s: %v", remoteAddr, err)
	} else {
		s.logger.Printf("[OnClose] Connection closed normally from %s", remoteAddr)
	}

	if ctx := c.Context(); ctx != nil {
		if httpConn, ok := ctx.(*Connection); ok {
			_ = httpConn.Close()
		}
	} else {
		s.logger.Printf("[OnClose] Connection %s had no context", remoteAddr)
	}

	return gnet.None
}

// OnTraffic is called when data is received on a connection.
func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	ctx := c.Context()
	if ctx == nil {
		s.logger.Printf("HTTP/1.1 connection context not found")
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

	conn, ok := ctx.(*Connection)
	if !ok {
		s.logger.Printf("HTTP/1.1 invalid connection context type")
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

	// Get all available data at once for maximum performance
	buf, err := c.Next(-1)
	if err != nil {
		s.logger.Printf("Error reading data: %v", err)
		// Send error response before closing
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

	// Process data synchronously to ensure response is queued before returning
	// HandleData queues the response via AsyncWritev, which is non-blocking,
	// so this shouldn't significantly block the event loop
	if err := conn.HandleData(buf); err != nil {
		// Check if this is a normal connection close request
		if err.Error() == "connection close requested" {
			// This is normal - just close the connection
			return gnet.Close
		}

		s.logger.Printf("Error handling data: %v", err)
		// Send error response and close connection
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
	}

	return gnet.None
}

// StoreConnection is kept for compatibility but now uses Conn.Context().
// This is used by the multiplexer to register connections.
func (s *Server) StoreConnection(c gnet.Conn, conn *Connection) {
	c.SetContext(conn)
}
