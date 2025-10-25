package h1

import (
	"context"
	"log"

	"github.com/albertbausili/celeris/internal/h2/stream"
	"github.com/panjf2000/gnet/v2"
)

// Server implements gnet.EventHandler for HTTP/1.1.
type Server struct {
	handler stream.Handler
	ctx     context.Context
	logger  *log.Logger
}

// NewServer creates a new HTTP/1.1 server.
func NewServer(ctx context.Context, handler stream.Handler, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}

	return &Server{
		handler: handler,
		ctx:     ctx,
		logger:  logger,
	}
}

// OnOpen is called when a new connection is opened.
func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	conn := NewConnection(s.ctx, c, s.handler, s.logger)
	c.SetContext(conn) // Use gnet.Conn.Context() for per-connection storage (best practice)
	s.logger.Printf("HTTP/1.1 connection from %s", c.RemoteAddr().String())
	return nil, gnet.None
}

// OnClose is called when a connection is closed.
func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	if ctx := c.Context(); ctx != nil {
		if httpConn, ok := ctx.(*Connection); ok {
			_ = httpConn.Close()
		}
	}

	if err != nil {
		s.logger.Printf("HTTP/1.1 connection closed with error: %v", err)
	} else {
		s.logger.Printf("HTTP/1.1 connection closed from %s", c.RemoteAddr().String())
	}

	return gnet.None
}

// OnTraffic is called when data is received on a connection.
func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	ctx := c.Context()
	if ctx == nil {
		s.logger.Printf("HTTP/1.1 connection context not found")
		return gnet.Close
	}

	conn, ok := ctx.(*Connection)
	if !ok {
		s.logger.Printf("HTTP/1.1 invalid connection context type")
		return gnet.Close
	}

	// Read all available data
	buf, err := c.Next(-1)
	if err != nil {
		s.logger.Printf("Error reading data: %v", err)
		return gnet.Close
	}

	// Process the data
	if err := conn.HandleData(buf); err != nil {
		s.logger.Printf("Error handling data: %v", err)
		return gnet.Close
	}

	return gnet.None
}

// StoreConnection is kept for compatibility but now uses Conn.Context().
// This is used by the multiplexer to register connections.
func (s *Server) StoreConnection(c gnet.Conn, conn *Connection) {
	c.SetContext(conn)
}
