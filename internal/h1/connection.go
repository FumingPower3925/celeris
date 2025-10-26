package h1

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/albertbausili/celeris/internal/h2/stream"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/http2"
)

// Connection represents an HTTP/1.1 connection over gnet.
type Connection struct {
	conn    gnet.Conn
	parser  *Parser
	writer  *ResponseWriter
	handler stream.Handler
	buffer  *bytes.Buffer
	logger  *log.Logger
	ctx     context.Context
	req     Request
}

// NewConnection creates a new HTTP/1.1 connection.
func NewConnection(ctx context.Context, c gnet.Conn, handler stream.Handler, logger *log.Logger) *Connection {
	return &Connection{
		conn:    c,
		parser:  NewParser(),
		writer:  NewResponseWriter(c, logger, true),
		handler: handler,
		buffer:  new(bytes.Buffer),
		logger:  logger,
		ctx:     ctx,
	}
}

// HandleData processes incoming HTTP/1.1 data.
func (c *Connection) HandleData(data []byte) error {
	// Fast-path: if there is no pending leftover, parse directly from incoming buffer to avoid copy
	if c.buffer.Len() == 0 {
		// Support multiple pipelined requests in the same incoming buffer
		offset := 0
		for offset < len(data) {
			c.parser.Reset(data[offset:])
			c.req.Reset()
			req := &c.req
			consumed, err := c.parser.ParseRequest(req)
			if err != nil {
				c.logger.Printf("Parse error: %v", err)
				return c.sendError(400, "Bad Request")
			}

			if consumed == 0 {
				// Incomplete headers, copy the remainder for next OnTraffic
				c.buffer.Write(data[offset:])
				return nil
			}

			// Determine if a body is required; if so, fall back to buffered path
			bodyNeeded := int64(0)
			if req.ChunkedEncoding {
				bodyNeeded = -1
			} else if req.ContentLength > 0 {
				bodyNeeded = req.ContentLength
			}

			if bodyNeeded > 0 || bodyNeeded == -1 {
				// Copy the remainder (including already parsed headers) to buffer and use standard path
				c.buffer.Write(data[offset:])
				break
			}

			// No body: handle request directly without touching the buffer
			s := c.requestToStream(req, nil)
			c.writer.Reset(req.KeepAlive)
			if err := c.handler.HandleStream(c.ctx, s); err != nil {
				c.logger.Printf("Handler error: %v", err)
				return c.sendError(500, "Internal Server Error")
			}
			if !req.KeepAlive {
				return fmt.Errorf("connection close requested")
			}

			// Advance to parse any subsequent pipelined request
			offset += consumed
			if offset >= len(data) {
				return nil
			}
		}
		// If we broke due to body or incomplete header, continue with buffered parse below
	} else {
		// There is pending leftover: append and parse from buffer
		c.buffer.Write(data)
	}

	// Buffered path: parse from accumulated buffer
	for c.buffer.Len() > 0 {
		c.parser.Reset(c.buffer.Bytes())
		c.req.Reset()
		req := &c.req
		consumed, err := c.parser.ParseRequest(req)
		if err != nil {
			c.logger.Printf("Parse error: %v", err)
			return c.sendError(400, "Bad Request")
		}

		if consumed == 0 {
			// Need more data
			break
		}

		if err := c.handleRequest(req, consumed); err != nil {
			return err
		}
	}

	return nil
}

// handleRequest processes a complete HTTP/1.1 request.
func (c *Connection) handleRequest(req *Request, headerBytes int) error {
	// Calculate how much body we need
	bodyNeeded := int64(0)
	if req.ChunkedEncoding {
		// For chunked, we'll read chunks as they come
		bodyNeeded = -1
	} else if req.ContentLength > 0 {
		bodyNeeded = req.ContentLength
	}

	var bodyData []byte

	switch {
	case bodyNeeded > 0:
		// Fixed content-length body
		available := int64(c.buffer.Len() - headerBytes)
		if available < bodyNeeded {
			// Need more data, return and wait
			return nil
		}

		// Consume headers and extract body
		c.buffer.Next(headerBytes)
		bodyData = make([]byte, bodyNeeded)
		_, _ = c.buffer.Read(bodyData)
	case bodyNeeded == -1:
		// Chunked encoding - read all chunks
		c.buffer.Next(headerBytes)
		chunks := &bytes.Buffer{}

		for {
			c.parser.Reset(c.buffer.Bytes())
			chunk, consumed, err := c.parser.ParseChunkedBody()
			if err != nil {
				return c.sendError(400, "Invalid chunked encoding")
			}

			if consumed == 0 {
				// Need more data
				return nil
			}

			c.buffer.Next(consumed)

			if chunk == nil {
				// Last chunk (size 0)
				break
			}

			chunks.Write(chunk)
		}

		bodyData = chunks.Bytes()
	default:
		// No body
		c.buffer.Next(headerBytes)
	}

	// Convert HTTP/1.1 request to stream format for handler
	s := c.requestToStream(req, bodyData)

	// Reset writer for new response
	c.writer.Reset(req.KeepAlive)

	// Call handler
	if err := c.handler.HandleStream(c.ctx, s); err != nil {
		c.logger.Printf("Handler error: %v", err)
		return c.sendError(500, "Internal Server Error")
	}

	// If not keep-alive, close connection
	if !req.KeepAlive {
		return fmt.Errorf("connection close requested")
	}

	return nil
}

// requestToStream converts HTTP/1.1 request to stream.Stream for handler.
func (c *Connection) requestToStream(req *Request, body []byte) *stream.Stream {
	s := stream.NewStream(1) // Use stream ID 1 for HTTP/1.1

	// Add pseudo-headers (HTTP/2 style)
	s.AddHeader(":method", req.Method)
	s.AddHeader(":path", req.Path)
	s.AddHeader(":scheme", "http")
	s.AddHeader(":authority", req.Host)

	// Add regular headers
	for _, h := range req.Headers {
		s.AddHeader(h[0], h[1])
	}

	// Add body data
	if len(body) > 0 {
		_ = s.AddData(body)
	}

	s.EndStream = true
	s.SetState(stream.StateHalfClosedRemote)

	// Set response writer
	s.ResponseWriter = &h1ResponseWriter{
		writer: c.writer,
	}

	return s
}

// sendError sends an HTTP error response.
func (c *Connection) sendError(status int, message string) error {
	body := []byte(message)
	headers := [][2]string{
		{"content-type", "text/plain; charset=utf-8"},
		{"content-length", fmt.Sprintf("%d", len(body))},
	}

	return c.writer.WriteResponse(status, headers, body, true)
}

// Close closes the connection.
func (c *Connection) Close() error {
	return c.conn.Close()
}

// h1ResponseWriter adapts HTTP/1.1 ResponseWriter to stream.ResponseWriter interface.
type h1ResponseWriter struct {
	writer *ResponseWriter
}

func (w *h1ResponseWriter) WriteResponse(_ uint32, status int, headers [][2]string, body []byte) error {
	// For H1, end the response on each call to avoid unsolicited extra writes
	endResponse := true
	return w.writer.WriteResponse(status, headers, body, endResponse)
}

func (w *h1ResponseWriter) SendGoAway(_ uint32, _ http2.ErrCode, _ []byte) error {
	// HTTP/1.1 doesn't have GOAWAY, just close connection
	return nil
}

func (w *h1ResponseWriter) MarkStreamClosed(_ uint32) {
	// No-op for HTTP/1.1
}

func (w *h1ResponseWriter) IsStreamClosed(_ uint32) bool {
	return false
}

func (w *h1ResponseWriter) WriteRSTStreamPriority(_ uint32, _ http2.ErrCode) error {
	// HTTP/1.1 doesn't have RST_STREAM
	return nil
}

func (w *h1ResponseWriter) CloseConn() error {
	// Handled at connection level
	return nil
}
