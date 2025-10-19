// Package transport provides HTTP/2 server transport implementation using gnet.
package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/albertbausili/celeris/internal/frame"
	"github.com/albertbausili/celeris/internal/stream"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/http2"
)

// verboseLogging controls hot-path logging; keep false for performance runs.
const verboseLogging = false

const (
	// HTTP/2 connection preface
	http2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
)

// Server implements the gnet.EventHandler interface for HTTP/2
type Server struct {
	gnet.BuiltinEventEngine
	handler      stream.Handler
	connections  sync.Map // map[gnet.Conn]*Connection
	ctx          context.Context
	cancel       context.CancelFunc
	addr         string
	multicore    bool
	numEventLoop int
	reusePort    bool
	logger       *log.Logger
	engine       gnet.Engine
	maxStreams   uint32
}

// headersSlicePool reuses small header slices to reduce allocations per response.
var headersSlicePool = sync.Pool{New: func() any {
	s := make([][2]string, 0, 8)
	return &s
}}

// Config holds server configuration
type Config struct {
	Addr                 string
	Multicore            bool
	NumEventLoop         int
	ReusePort            bool
	Logger               *log.Logger
	MaxConcurrentStreams uint32
}

// NewServer creates a new HTTP/2 server with gnet transport
func NewServer(handler stream.Handler, config Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	if config.Logger == nil {
		config.Logger = log.Default()
	}

	return &Server{
		handler:      handler,
		ctx:          ctx,
		cancel:       cancel,
		addr:         config.Addr,
		multicore:    config.Multicore,
		numEventLoop: config.NumEventLoop,
		reusePort:    config.ReusePort,
		logger:       config.Logger,
		maxStreams:   config.MaxConcurrentStreams,
	}
}

// Start starts the gnet server
func (s *Server) Start() error {
	options := []gnet.Option{
		gnet.WithMulticore(s.multicore),
		gnet.WithReusePort(s.reusePort),
	}

	if s.numEventLoop > 0 {
		options = append(options, gnet.WithNumEventLoop(s.numEventLoop))
	}

	s.logger.Printf("Starting HTTP/2 server on %s", s.addr)
	return gnet.Run(s, "tcp://"+s.addr, options...)
}

// Stop gracefully stops the server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Println("Initiating graceful shutdown...")

	// Cancel context to stop accepting new connections
	s.cancel()

	// Send GOAWAY to all active connections and wait for streams to complete
	s.connections.Range(func(_, value interface{}) bool {
		if conn, ok := value.(*Connection); ok {
			_ = conn.Shutdown(ctx)
		}
		return true
	})

	// Give a very brief moment for streams to finish, then force close connections
	time.Sleep(100 * time.Millisecond)

	s.connections.Range(func(key, _ interface{}) bool {
		if gnetConn, ok := key.(gnet.Conn); ok {
			s.logger.Printf("Force closing connection to %s", gnetConn.RemoteAddr().String())
			_ = gnetConn.Close()
		}
		return true
	})

	// Wait briefly for OnClose to be called and connections to be removed
	time.Sleep(100 * time.Millisecond)

	// Stop the gnet engine to prevent new connections
	// Use background context since the original may have expired
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()

	if err := s.engine.Stop(stopCtx); err != nil {
		s.logger.Printf("Error stopping gnet engine: %v", err)
		// Don't return error here, as we've already done cleanup
	}

	s.logger.Println("Server shutdown complete")
	return nil
}

// OnBoot is called when the server is ready to accept connections
func (s *Server) OnBoot(eng gnet.Engine) gnet.Action {
	s.engine = eng
	s.logger.Printf("HTTP/2 server is listening on %s (multicore: %v)",
		s.addr, s.multicore)
	return gnet.None
}

// OnOpen is called when a new connection is opened
func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	conn := NewConnection(c, s.handler, s.logger, s.maxStreams)
	s.connections.Store(c, conn)
	s.logger.Printf("New connection from %s", c.RemoteAddr().String())
	return nil, gnet.None
}

// OnClose is called when a connection is closed
func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	if conn, ok := s.connections.Load(c); ok {
		if httpConn, ok := conn.(*Connection); ok {
			_ = httpConn.Close()
		}
		s.connections.Delete(c)
	}

	if err != nil {
		s.logger.Printf("Connection closed with error: %v", err)
	} else {
		s.logger.Printf("Connection closed from %s", c.RemoteAddr().String())
	}

	return gnet.None
}

// OnTraffic is called when data is received on a connection
func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	connValue, ok := s.connections.Load(c)
	if !ok {
		s.logger.Printf("Connection not found in map")
		return gnet.Close
	}

	conn := connValue.(*Connection)

	// Read all available data
	buf, err := c.Next(-1)
	if err != nil {
		s.logger.Printf("Error reading data: %v", err)
		return gnet.Close
	}

	// Process the data
	if err := conn.HandleData(s.ctx, buf); err != nil {
		s.logger.Printf("Error handling data: %v", err)
		return gnet.Close
	}

	return gnet.None
}

// Connection represents an HTTP/2 connection over gnet
type Connection struct {
	conn            gnet.Conn
	parser          *frame.Parser
	writer          *frame.Writer
	processor       *stream.Processor
	prefaceReceived bool
	buffer          *bytes.Buffer
	writeMu         sync.Mutex
	logger          *log.Logger
	shuttingDown    bool
	shutdownMu      sync.RWMutex
	sentGoAway      atomic.Bool // Track if we sent GOAWAY
	closedStreams   sync.Map    // map[uint32]bool - streams we've reset
	readerBound     bool        // whether parser has been bound to persistent reader
	// gate to prioritize error frames over normal responses
	errPriorityMu sync.Mutex
	headerEncoder *frame.HeaderEncoder // reused encoder under writeMu
}

// NewConnection creates a new HTTP/2 connection
func NewConnection(c gnet.Conn, handler stream.Handler, logger *log.Logger, maxConcurrentStreams uint32) *Connection {
	conn := &Connection{
		conn:          c,
		parser:        frame.NewParser(),
		buffer:        new(bytes.Buffer),
		logger:        logger,
		headerEncoder: frame.NewHeaderEncoder(), // Reused per connection under writeMu
	}

	// Create a writer that writes to the connection
	conn.writer = frame.NewWriter(&connWriter{
		conn:   c,
		mu:     &sync.Mutex{},
		logger: logger,
	})
	conn.processor = stream.NewProcessor(handler, conn.writer, conn)
	if maxConcurrentStreams > 0 {
		conn.processor.GetManager().SetMaxConcurrentStreams(maxConcurrentStreams)
	}

	return conn
}

// HandleData processes incoming data
//
//nolint:gocyclo // Frame validation and parsing requires checking multiple frame types per RFC 7540
func (c *Connection) HandleData(ctx context.Context, data []byte) error {
	if verboseLogging {
		c.logger.Printf("Received %d bytes", len(data))
	}

	// Write data to buffer
	c.buffer.Write(data)

	// Check for HTTP/2 preface if not yet received
	if !c.prefaceReceived {
		if c.buffer.Len() >= len(http2Preface) {
			preface := make([]byte, len(http2Preface))
			_, _ = c.buffer.Read(preface)

			if string(preface) != http2Preface {
				c.logger.Printf("Invalid preface: %q", string(preface))
				return fmt.Errorf("invalid HTTP/2 preface")
			}

			c.prefaceReceived = true
			if verboseLogging {
				c.logger.Printf("HTTP/2 preface received from %s", c.conn.RemoteAddr().String())
			}

			// Send server preface (SETTINGS frame)
			if err := c.sendServerPreface(); err != nil {
				return fmt.Errorf("failed to send server preface: %w", err)
			}

			// IMPORTANT: Return here to let gnet send our SETTINGS before we process client frames
			// The client is waiting for our SETTINGS before sending its frames
			// Next OnTraffic call will process the client's frames
			if verboseLogging {
				c.logger.Printf("Returning from HandleData to allow SETTINGS to be sent")
			}
			return nil
		}
		// Need more data
		if verboseLogging {
			c.logger.Printf("Waiting for complete preface (have %d, need %d)", c.buffer.Len(), len(http2Preface))
		}
		return nil
	}

	// Bind a persistent reader to preserve CONTINUATION state in the framer
	if !c.readerBound {
		c.parser.InitReader(&bufferReader{c: c})
		c.readerBound = true
	}

	// Process HTTP/2 frames
	for c.buffer.Len() >= 9 { // Minimum frame size
		if verboseLogging {
			c.logger.Printf("Buffer has %d bytes, attempting to parse frame", c.buffer.Len())
		}

		// Peek frame header to detect invalid PING length and compute consumed size even on parse error
		// Peek without consuming from buffer
		if c.buffer.Len() < 9 {
			break
		}
		var header [9]byte
		copy(header[:], c.buffer.Bytes()[:9])
		length := uint32(header[0])<<16 | uint32(header[1])<<8 | uint32(header[2])
		ftype := http2.FrameType(header[3])
		// Mask reserved bit when reading stream ID
		streamID := binary.BigEndian.Uint32(header[5:9]) & 0x7fffffff
		flags := http2.Flags(header[4])

		// If we're in the middle of a header block (expecting CONTINUATION), don't
		// hand partial frames to the framer; wait until we have the full header+payload
		// Require full header+payload for ANY frame before parsing to avoid framer partial-read errors
		if c.buffer.Len() < int(9+length) {
			if c.processor.IsExpectingContinuation() {
				expID, _ := c.processor.GetExpectedContinuationStreamID()
				if verboseLogging {
					c.logger.Printf("Waiting for more bytes (continuation expected on %d): have=%d need=%d", expID, c.buffer.Len(), int(9+length))
				}
			} else if verboseLogging {
				c.logger.Printf("Waiting for more bytes: have=%d need=%d (ftype=%v sid=%d flags=0x%x)", c.buffer.Len(), int(9+length), ftype, streamID, flags)
			}
			break
		}

		// If we're expecting a CONTINUATION on a given stream, only CONTINUATION is allowed on that stream
		if c.processor.IsExpectingContinuation() {
			if expID, ok := c.processor.GetExpectedContinuationStreamID(); ok {
				if streamID == expID && ftype != http2.FrameContinuation {
					if verboseLogging {
						c.logger.Printf("Protocol error: received %v on stream %d while expecting CONTINUATION; sending GOAWAY", ftype, streamID)
					}
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("expected CONTINUATION"))
					break
				}
			}
		}

		// Pre-validate frame lengths to produce correct error codes before parsing
		switch ftype {
		case http2.FramePing:
			if length != 8 {
				if verboseLogging {
					c.logger.Printf("Invalid PING length %d", length)
				}
				// Send GOAWAY with FRAME_SIZE_ERROR and skip offending frame bytes, but don't close immediately
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("invalid PING"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				// Reader is persistent; bytes were skipped from c.buffer; continue
				continue
			}
			// PING must have streamID 0
			if streamID != 0 {
				if verboseLogging {
					c.logger.Printf("PING with non-zero stream id %d", streamID)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("PING stream id must be 0"))
				// consume frame bytes
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
		case http2.FramePriority:
			if length != 5 {
				if verboseLogging {
					c.logger.Printf("Invalid PRIORITY length %d", length)
				}
				// Stream error if streamID != 0 else connection error
				if header[5]|header[6]|header[7]|header[8] == 0 { // stream id zero
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("PRIORITY length"))
				} else {
					streamID := binary.BigEndian.Uint32(header[5:9]) & 0x7fffffff
					_ = c.writer.WriteRSTStream(streamID, http2.ErrCodeFrameSize)
				}
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
		case http2.FrameRSTStream:
			if length != 4 {
				if verboseLogging {
					c.logger.Printf("Invalid RST_STREAM length %d", length)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("RST_STREAM length"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
		case http2.FrameWindowUpdate:
			if length != 4 {
				if verboseLogging {
					c.logger.Printf("Invalid WINDOW_UPDATE length %d", length)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("WINDOW_UPDATE length"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
			// Fast-path: if WINDOW_UPDATE increment is 0, immediately send error per RFC 7540 §6.9
			if c.buffer.Len() >= int(9+length) {
				inc := binary.BigEndian.Uint32(c.buffer.Bytes()[9:13]) & 0x7fffffff
				if inc == 0 {
					if streamID == 0 {
						_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("WINDOW_UPDATE increment is 0"))
					} else {
						_ = c.WriteRSTStreamPriority(streamID, http2.ErrCodeProtocol)
					}
					consumed := int(length) + 9
					if c.buffer.Len() >= consumed {
						c.buffer.Next(consumed)
					} else {
						c.buffer.Reset()
					}
					continue
				}
			}

		case http2.FrameSettings:
			// SETTINGS must be on stream 0
			if streamID != 0 {
				if verboseLogging {
					c.logger.Printf("SETTINGS on non-zero stream id %d", streamID)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("SETTINGS stream id must be 0"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
			if (flags&http2.FlagSettingsAck) != 0 && length != 0 {
				if verboseLogging {
					c.logger.Printf("Invalid SETTINGS with ACK and payload length %d", length)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("SETTINGS ACK with payload"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
			// SETTINGS payload length must be a multiple of 6 when ACK not set
			if (flags&http2.FlagSettingsAck) == 0 && (length%6) != 0 {
				if verboseLogging {
					c.logger.Printf("Invalid SETTINGS length %d (not multiple of 6)", length)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("SETTINGS length not multiple of 6"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
		case http2.FrameGoAway:
			// GOAWAY must be on stream 0
			if streamID != 0 {
				if verboseLogging {
					c.logger.Printf("GOAWAY on non-zero stream id %d", streamID)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("GOAWAY stream id must be 0"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				continue
			}
		case http2.FrameHeaders:
			// If padded, ensure pad length is valid before parsing
			// HEADERS frame padding format: pad_length (1 byte) + data + padding
			// FlagHeadersPadded is 0x08
			if (flags & 0x08) != 0 {
				// Need full frame to validate pad
				if c.buffer.Len() < int(9+length) {
					if verboseLogging {
						c.logger.Printf("Waiting for full HEADERS payload to validate padding: have=%d need=%d", c.buffer.Len(), int(9+length))
					}
					break
				}
				// Peek pad length byte at start of payload (byte 9)
				padLen := int(c.buffer.Bytes()[9])
				// For HEADERS, must account for priority (5 bytes) if present (FlagHeadersPriority = 0x20)
				minLen := 1 // pad_length byte itself
				if (flags & 0x20) != 0 {
					minLen += 5 // priority fields
				}
				if padLen > int(length)-minLen {
					if verboseLogging {
						c.logger.Printf("Invalid HEADERS pad length %d for payload length %d", padLen, length)
					}
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("invalid HEADERS pad length"))
					consumed := int(length) + 9
					if c.buffer.Len() >= consumed {
						c.buffer.Next(consumed)
					} else {
						c.buffer.Reset()
					}
					continue
				}
			}
		case http2.FrameData:
			// If padded, ensure pad length is valid before parsing
			if (flags & http2.FlagDataPadded) != 0 {
				// Need full frame to validate pad
				if c.buffer.Len() < int(9+length) {
					if verboseLogging {
						c.logger.Printf("Waiting for full DATA payload to validate padding: have=%d need=%d", c.buffer.Len(), int(9+length))
					}
					break
				}
				// Peek pad length byte at start of payload (byte 9)
				padLen := int(c.buffer.Bytes()[9])
				if padLen > int(length-1) {
					if verboseLogging {
						c.logger.Printf("Invalid DATA pad length %d for payload length %d", padLen, length)
					}
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("invalid DATA pad length"))
					consumed := int(length) + 9
					if c.buffer.Len() >= consumed {
						c.buffer.Next(consumed)
					} else {
						c.buffer.Reset()
					}
					continue
				}
			}
		}
		// Try to parse a frame using the persistent framer (consumes from c.buffer)
		frame, err := c.parser.ReadNextFrame()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// Need more data
				if verboseLogging {
					c.logger.Printf("Need more data for complete frame")
				}
				break
			}
			// Map typed http2 errors appropriately
			if se, ok := err.(http2.StreamError); ok {
				// If we were expecting a CONTINUATION on this stream, escalate to connection error per RFC 7540 §6.10
				if c.processor.IsExpectingContinuation() {
					if expID, ok := c.processor.GetExpectedContinuationStreamID(); ok && expID == se.StreamID {
						if verboseLogging {
							c.logger.Printf("Stream error while expecting CONTINUATION on %d (%v); sending GOAWAY PROTOCOL_ERROR", se.StreamID, se.Code)
						}
						_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("continuation sequence violated"))
						continue
					}
				}
				if verboseLogging {
					c.logger.Printf("Stream parse error on %d: %v", se.StreamID, se.Code)
				}
				_ = c.writer.WriteRSTStream(se.StreamID, se.Code)
				// Do not manually skip bytes; the framer consumed necessary bytes.
				continue
			}
			if ce, ok := err.(http2.ConnectionError); ok {
				if verboseLogging {
					c.logger.Printf("Connection parse error: %v", http2.ErrCode(ce))
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCode(ce), []byte("frame parse error"))
				// Do not manually skip bytes after framer error; let connection drain/close.
				continue
			}
			// Special-case invalid PING length -> send GOAWAY FRAME_SIZE_ERROR and skip offending frame bytes
			if ftype == http2.FramePing && length != 8 {
				if verboseLogging {
					c.logger.Printf("Invalid PING length %d, sending GOAWAY FRAME_SIZE_ERROR", length)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeFrameSize, []byte("invalid PING length"))
				consumed := int(length) + 9
				if c.buffer.Len() >= consumed {
					c.buffer.Next(consumed)
				} else {
					c.buffer.Reset()
				}
				// Reader is persistent; continue
				continue
			}
			// Generic parse error: send PROTOCOL_ERROR GOAWAY and skip offending frame bytes
			if verboseLogging {
				c.logger.Printf("Parse error: %v, sending GOAWAY PROTOCOL_ERROR and skipping frame (ftype=%v len=%d sid=%d flags=0x%x)", err, ftype, length, streamID, flags)
			}
			_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("frame parse error"))
			consumed := int(length) + 9
			if c.buffer.Len() >= consumed {
				c.buffer.Next(consumed)
			} else {
				c.buffer.Reset()
			}
			continue
		}

		if verboseLogging {
			c.logger.Printf("Parsed frame: type=%v, streamID=%d, length=%d, flags=0x%x",
				frame.Header().Type, frame.Header().StreamID, frame.Header().Length, frame.Header().Flags)
		}

		// Note: bytes are already consumed from c.buffer by the persistent framer's reader

		// Process the frame with connection context
		if err := c.processor.ProcessFrameWithConn(ctx, frame, c); err != nil {
			c.logger.Printf("Error processing frame: %v", err)
			// Continue processing other frames
		}
	}

	return nil
}

// sendServerPreface sends the initial SETTINGS frame
func (c *Connection) sendServerPreface() error {
	if verboseLogging {
		c.logger.Printf("Sending server preface (initial SETTINGS)")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	settings := []http2.Setting{
		{ID: http2.SettingHeaderTableSize, Val: 4096}, // Explicit HPACK dynamic table size
		{ID: http2.SettingMaxConcurrentStreams, Val: c.processor.GetManager().GetMaxConcurrentStreams()},
		{ID: http2.SettingMaxFrameSize, Val: 16384},
		{ID: http2.SettingInitialWindowSize, Val: 65535},
	}
	if err := c.writer.WriteSettings(settings...); err != nil {
		if verboseLogging {
			c.logger.Printf("Error writing SETTINGS: %v", err)
		}
		return err
	}
	// Flush to ensure SETTINGS is sent immediately
	if err := c.writer.Flush(); err != nil {
		if verboseLogging {
			c.logger.Printf("Error flushing SETTINGS: %v", err)
		}
		return err
	}
	if verboseLogging {
		c.logger.Printf("Server preface sent successfully")
	}
	return nil
}

// Close closes the connection
func (c *Connection) Close() error {
	// Clean up resources
	return nil
}

// Shutdown initiates graceful shutdown of the connection
func (c *Connection) Shutdown(ctx context.Context) error {
	c.shutdownMu.Lock()
	c.shuttingDown = true
	c.shutdownMu.Unlock()

	// Get last stream ID from processor
	lastStreamID := c.processor.GetManager().GetLastStreamID()

	// Send GOAWAY frame
	debugData := []byte("server shutting down")
	if err := c.processor.SendGoAway(lastStreamID, http2.ErrCodeNo, debugData); err != nil {
		c.logger.Printf("Failed to send GOAWAY: %v", err)
	}

	// Wait for active streams to complete or timeout
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if c.processor.GetManager().StreamCount() == 0 {
				return nil
			}
		}
	}

	return nil
}

// IsShuttingDown returns true if the connection is shutting down
func (c *Connection) IsShuttingDown() bool {
	c.shutdownMu.RLock()
	defer c.shutdownMu.RUnlock()
	return c.shuttingDown
}

// WriteResponse writes an HTTP/2 response
//
//nolint:gocyclo // Flow control and frame ordering requires complex window management per RFC 7540
func (c *Connection) WriteResponse(streamID uint32, status int, headers [][2]string, body []byte) error {
	// Serialize the entire response to preserve frame ordering
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// hot path: avoid logging to reduce allocations

	// Check if we've sent GOAWAY - don't send any more responses
	if c.sentGoAway.Load() {
		// avoid logging in hot path
		return fmt.Errorf("connection closing")
	}

	// Check if this specific stream was closed/reset
	if c.IsStreamClosed(streamID) {
		// avoid logging in hot path
		return fmt.Errorf("stream %d was reset", streamID)
	}

	// Check if stream is still valid (not closed/reset)
	if st, ok := c.processor.GetManager().GetStream(streamID); ok {
		state := st.GetState()
		// StateClosed = 4 (0-indexed: Idle=0, Open=1, HalfClosedLocal=2, HalfClosedRemote=3, Closed=4)
		if state == 4 { // Stream is closed
			// avoid logging in hot path
			return fmt.Errorf("stream %d is closed", streamID)
		}
	}

	// Build headers without append-growth to reduce allocations
	// Obtain a small slice from pool to avoid repeated allocations; grow if needed
	pooled := headersSlicePool.Get().(*[][2]string)
	hdrs := (*pooled)[:0]
	if cap(hdrs) < 1+len(headers) {
		hdrs = make([][2]string, 0, 1+len(headers))
	}
	// status without fmt
	hdrs = append(hdrs, [2]string{":status", strconv.Itoa(status)})
	hdrs = append(hdrs, headers...)
	allHeaders := hdrs

	// Final guard before encoding/writing HEADERS
	if c.sentGoAway.Load() || c.IsStreamClosed(streamID) {
		// avoid logging in hot path
		_ = c.writer.Flush()
		return nil
	}

	// Use the per-connection encoder under write lock (already held via writeMu)
	// to gain dynamic-table compression without cross-stream races.
	headerBlock, err := c.headerEncoder.EncodeBorrow(allHeaders)
	// return the pooled slice
	*pooled = allHeaders[:0]
	headersSlicePool.Put(pooled)
	if err != nil {
		c.logger.Printf("ERROR encoding headers: %v", err)
		return fmt.Errorf("failed to encode headers: %w", err)
	}
	// avoid logging in hot path

	// Write HEADERS (and CONTINUATION) respecting peer MAX_FRAME_SIZE
	// Note: writeMu is already held for the entire WriteResponse function
	endStream := len(body) == 0
	_, _, maxFrame := c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
	if maxFrame == 0 {
		maxFrame = 16384
	}
	// avoid logging in hot path
	if err := c.writer.WriteHeaders(streamID, endStream, headerBlock, maxFrame); err != nil {
		c.logger.Printf("ERROR writing HEADERS: %v", err)
		return fmt.Errorf("failed to write headers: %w", err)
	}

	// If there is a body, try to write the first DATA chunk before flushing so that
	// HEADERS and DATA are sent in a single AsyncWritev batch.
	remaining := body
	if len(remaining) > 0 {
		connWin, streamWin, maxFrame := c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
		if connWin > 0 && streamWin > 0 && maxFrame > 0 {
			allow := int(connWin)
			if int(streamWin) < allow {
				allow = int(streamWin)
			}
			if int(maxFrame) < allow {
				allow = int(maxFrame)
			}
			if allow > len(remaining) {
				allow = len(remaining)
			}
			if allow > 0 {
				chunk := remaining[:allow]
				remaining = remaining[allow:]
				endData := len(remaining) == 0 && safebufLen(c.processor.GetManager(), streamID) == 0
				if err := c.writer.WriteData(streamID, endData, chunk); err != nil {
					c.logger.Printf("ERROR writing first DATA: %v", err)
					return fmt.Errorf("failed to write first data: %w", err)
				}
				//nolint:gosec // G115: safe conversion, chunk size bounded by flow control windows and MAX_FRAME_SIZE
				c.processor.GetManager().ConsumeSendWindow(streamID, int32(len(chunk)))
				// If this ended the stream, update state now
				if endData {
					if st, ok := c.processor.GetManager().GetStream(streamID); ok {
						if st.EndStream {
							st.SetState(stream.StateClosed)
						} else {
							st.SetState(stream.StateHalfClosedLocal)
						}
					}
				}
			}
		}
	}

	// Flush once to send HEADERS and the first DATA (if any) in-order.
	if err := c.writer.Flush(); err != nil {
		return err
	}

	// After flush returns, peer has HEADERS; now mark headers as sent.
	if st, ok := c.processor.GetManager().GetStream(streamID); ok {
		st.HeadersSent = true
		st.SetPhase(stream.PhaseHeadersSent)
	}

	// Update stream state after sending HEADERS with END_STREAM (no body)
	if endStream {
		if st, ok := c.processor.GetManager().GetStream(streamID); ok {
			if st.EndStream {
				st.SetState(stream.StateClosed)
			} else {
				st.SetState(stream.StateHalfClosedLocal)
			}
		}
	}

	// After sending headers, re-check that the stream wasn't reset while we were writing
	if c.sentGoAway.Load() || c.IsStreamClosed(streamID) {
		// avoid logging in hot path
		// Flush any pending writes to ensure the peer receives prior frames
		_ = c.writer.Flush()
		if verboseLogging {
			c.logger.Printf("WriteResponse completed successfully")
		}
		return nil
	}

	// Write remaining DATA frames if there's a body, respecting connection/stream windows and MAX_FRAME_SIZE
	if len(remaining) > 0 {
		for len(remaining) > 0 {
			// Before each chunk, check if the stream was reset/closed
			if c.sentGoAway.Load() || c.IsStreamClosed(streamID) {
				// avoid logging in hot path
				break
			}
			connWin, streamWin, maxFrame := c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
			// Ensure HEADERS were sent for this stream before DATA to avoid protocol error
			if st, ok := c.processor.GetManager().GetStream(streamID); ok && !st.HeadersSent {
				if err := c.writer.Flush(); err != nil {
					return err
				}
				st.HeadersSent = true
				st.SetPhase(stream.PhaseHeadersSent)
			}
			if connWin <= 0 || streamWin <= 0 || maxFrame == 0 {
				// Cannot send now; buffer remaining to stream's OutboundBuffer and return
				if s, ok := c.processor.GetManager().GetStream(streamID); ok {
					_, _ = s.OutboundBuffer.Write(remaining)
					s.OutboundEndStream = true
				}
				// avoid logging in hot path
				break
			}

			// Compute chunk size
			allow := int(connWin)
			if int(streamWin) < allow {
				allow = int(streamWin)
			}
			if int(maxFrame) < allow {
				allow = int(maxFrame)
			}
			if allow <= 0 {
				break
			}
			if allow > len(remaining) {
				allow = len(remaining)
			}

			chunk := remaining[:allow]
			remaining = remaining[allow:]
			endStream := len(remaining) == 0
			if endStream {
				// also include any previously buffered data length
				if s, ok := c.processor.GetManager().GetStream(streamID); ok && s.OutboundBuffer.Len() > 0 {
					endStream = false
				}
			}

			// Only set END_STREAM if no further DATA is pending for this stream
			if err := c.writer.WriteData(streamID, endStream && safebufLen(c.processor.GetManager(), streamID) == 0, chunk); err != nil {
				c.logger.Printf("ERROR writing DATA: %v", err)
				return fmt.Errorf("failed to write data: %w", err)
			}
			// Decrement windows
			//nolint:gosec // G115: safe conversion, chunk size bounded by flow control windows and MAX_FRAME_SIZE
			c.processor.GetManager().ConsumeSendWindow(streamID, int32(len(chunk)))

			// If this was the last chunk, update stream state
			if endStream && safebufLen(c.processor.GetManager(), streamID) == 0 {
				if st, ok := c.processor.GetManager().GetStream(streamID); ok {
					if st.EndStream {
						st.SetState(stream.StateClosed)
					} else {
						st.SetState(stream.StateHalfClosedLocal)
					}
				}
			}
		}

		// If remaining > 0, buffer and mark endStream for later
		if len(remaining) > 0 {
			if s, ok := c.processor.GetManager().GetStream(streamID); ok {
				_, _ = s.OutboundBuffer.Write(remaining)
				s.OutboundEndStream = true
			}
		}
	}

	// avoid logging in hot path
	// Flush to ensure response is sent
	// Force immediate send of queued frames; batching preserves ordering
	if err := c.writer.Flush(); err != nil {
		return err
	}
	// Wake the event loop to ensure kernel send
	_ = c.conn.Wake(nil)
	
	return nil
}

// safebufLen returns the length of the stream's pending outbound buffer.
func safebufLen(m *stream.Manager, streamID uint32) int {
	if s, ok := m.GetStream(streamID); ok {
		return s.OutboundBuffer.Len()
	}
	return 0
}

// connWriter implements io.Writer for gnet.Conn
type connWriter struct {
	conn     gnet.Conn
	mu       *sync.Mutex
	logger   *log.Logger
	pending  [][]byte
	inflight bool
	queued   [][]byte
}

// bufferReader adapts Connection's buffer to an io.Reader that drains as frames are read by http2.Framer.
// http2.Framer reads directly from this reader; we implement Read by draining from c.buffer.
type bufferReader struct {
	c *Connection
}

func (br *bufferReader) Read(p []byte) (int, error) {
	if br.c.buffer.Len() == 0 {
		// Signal that more data is expected; don't terminate header block parsing prematurely
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, br.c.buffer.Bytes())
	br.c.buffer.Next(n)
	return n, nil
}

// Write writes data directly to the connection
// NOTE: Caller MUST hold w.mu lock!
func (w *connWriter) Write(p []byte) (n int, err error) {
	if verboseLogging {
		w.logger.Printf("Writing %d bytes to connection", len(p))
	}

	// Serialize writes across all goroutines to preserve frame ordering
	w.mu.Lock()
	defer w.mu.Unlock()

	// Make a copy of the data since async send happens after return
	data := make([]byte, len(p))
	copy(data, p)
	w.pending = append(w.pending, data)
	return len(p), nil
}

// Flush ensures data is sent by calling gnet's Flush
func (w *connWriter) Flush() error {
	w.mu.Lock()
	if w.inflight {
		// Queue additional data to be sent after current inflight completes
		if len(w.pending) > 0 {
			w.queued = append(w.queued, w.pending...)
			w.pending = nil
		}
		w.mu.Unlock()
		return nil
	}
	batch := w.pending
	w.pending = nil
	if len(batch) == 0 {
		w.mu.Unlock()
		_ = w.conn.Wake(nil)
		return nil
	}
	w.inflight = true
	w.mu.Unlock()

	// Use vectorized async write to minimize syscalls
	return w.conn.AsyncWritev(batch, func(_ gnet.Conn, err error) error {
		if verboseLogging && err != nil {
			w.logger.Printf("AsyncWritev callback error: %v", err)
		}
		// On completion, check if there is queued data, and send it next
		w.mu.Lock()
		next := w.queued
		if len(next) > 0 {
			w.queued = nil
			w.inflight = true
			w.mu.Unlock()
			return w.conn.AsyncWritev(next, func(_ gnet.Conn, err error) error {
				if verboseLogging && err != nil {
					w.logger.Printf("AsyncWritev callback error: %v", err)
				}
				w.mu.Lock()
				w.inflight = false
				w.mu.Unlock()
				return nil
			})
		}
		w.inflight = false
		w.mu.Unlock()
		return nil
	})
}

// SendGoAway sends a GOAWAY frame and marks that we've sent it
// After sending GOAWAY, we continue processing frames but don't send more responses
func (c *Connection) SendGoAway(lastStreamID uint32, code http2.ErrCode, debug []byte) error {
	// Check if already sent
	if c.sentGoAway.Load() {
		return nil
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	// Mark that we're sending GOAWAY
	c.sentGoAway.Store(true)

	// Send the frame
	if err := c.writer.WriteGoAway(lastStreamID, code, debug); err != nil {
		return err
	}

	// Force immediate sending
	_ = c.writer.Flush()
	_ = c.conn.Wake(nil)
	


	
	c.logger.Printf("Sent GOAWAY frame: code=%v, lastStream=%d", code, lastStreamID)

	// Close the connection for connection-level errors after flushing GOAWAY
	switch code {
	case http2.ErrCodeCompression, http2.ErrCodeProtocol, http2.ErrCodeFrameSize, http2.ErrCodeFlowControl:
		_ = c.conn.Close()
	}
	return nil
}

// WriteRSTStreamPriority writes an RST_STREAM immediately and exclusively
// to avoid being interleaved with HEADERS when enforcing concurrency.
func (c *Connection) WriteRSTStreamPriority(streamID uint32, code http2.ErrCode) error {
	c.errPriorityMu.Lock()
	defer c.errPriorityMu.Unlock()
	// Take the normal write lock within the priority gate to serialize actual bytes
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	// Mark the stream closed before writing to prevent any subsequent HEADERS/DATA
	c.MarkStreamClosed(streamID)
	if err := c.writer.WriteRSTStream(streamID, code); err != nil {
		return err
	}
	// Flush immediately so the peer observes the error first
	if err := c.writer.Flush(); err != nil {
		return err
	}
	_ = c.conn.Wake(nil)
	
	return nil
}
// CloseConn closes the underlying TCP connection immediately to ensure peers observe error frames before teardown.
func (c *Connection) CloseConn() error {
	return c.conn.Close()
}

// MarkStreamClosed records the stream as closed to prevent any further writes.
func (c *Connection) MarkStreamClosed(streamID uint32) {
	c.closedStreams.Store(streamID, true)
}



// IsStreamClosed checks if a stream was closed/reset
func (c *Connection) IsStreamClosed(streamID uint32) bool {
	_, closed := c.closedStreams.Load(streamID)
	return closed
}
