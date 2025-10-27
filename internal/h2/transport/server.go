// Package transport provides HTTP/2 server transport implementation using gnet.
// It handles the low-level HTTP/2 protocol details and integrates with the gnet event-driven model.
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

	"github.com/albertbausili/celeris/internal/h2/frame"
	"github.com/albertbausili/celeris/internal/h2/stream"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/http2"
)

// verboseLogging controls hot-path logging for performance-sensitive operations.
// Keep false for production runs to avoid performance overhead.
const verboseLogging = false

const (
	// HTTP/2 connection preface
	http2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
)

// Server implements the gnet.EventHandler interface for HTTP/2 connections.
// It manages the lifecycle of HTTP/2 connections and streams.
type Server struct {
	gnet.BuiltinEventEngine
	handler       stream.Handler
	ctx           context.Context
	cancel        context.CancelFunc
	addr          string
	multicore     bool
	numEventLoop  int
	reusePort     bool
	logger        *log.Logger
	engine        gnet.Engine
	maxStreams    uint32
	activeConns   []gnet.Conn // Track connections for shutdown only
	activeConnsMu sync.Mutex  // Protects activeConns
}

// headersSlicePool reuses small header slices to reduce memory allocations per response.
// This optimization helps minimize garbage collection pressure.
var headersSlicePool = sync.Pool{New: func() any {
	s := make([][2]string, 0, 8)
	return &s
}}

// Config defines the configuration options for the HTTP/2 transport server.
type Config struct {
	Addr                 string
	Multicore            bool
	NumEventLoop         int
	ReusePort            bool
	Logger               *log.Logger
	MaxConcurrentStreams uint32
}

// NewServer creates a new HTTP/2 server with gnet transport engine.
// It initializes the server with the provided handler and configuration.
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

// GetMaxConcurrentStreams exposes the configured inbound stream concurrency limit.
func (s *Server) GetMaxConcurrentStreams() uint32 {
	return s.maxStreams
}

// Start starts the gnet server
func (s *Server) Start() error {
	options := []gnet.Option{
		gnet.WithMulticore(s.multicore),
		gnet.WithReusePort(s.reusePort),
		gnet.WithTCPNoDelay(gnet.TCPNoDelay),
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
	s.activeConnsMu.Lock()
	conns := make([]gnet.Conn, len(s.activeConns))
	copy(conns, s.activeConns)
	s.activeConnsMu.Unlock()

	for _, c := range conns {
		if connCtx := c.Context(); connCtx != nil {
			if conn, ok := connCtx.(*Connection); ok {
				_ = conn.Shutdown(ctx)
			}
		}
	}

	// Give a very brief moment for streams to finish, then force close connections
	time.Sleep(50 * time.Millisecond)

	for _, c := range conns {
		s.logger.Printf("Force closing connection to %s", c.RemoteAddr().String())
		_ = c.Close()
	}

	// Wait briefly for OnClose to be called and connections to be removed
	time.Sleep(50 * time.Millisecond)

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
	c.SetContext(conn) // Use gnet.Conn.Context() for per-connection storage (best practice)

	// Track for shutdown
	s.activeConnsMu.Lock()
	s.activeConns = append(s.activeConns, c)
	s.activeConnsMu.Unlock()

	s.logger.Printf("New connection from %s", c.RemoteAddr().String())
	return nil, gnet.None
}

// OnClose is called when a connection is closed
func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	if ctx := c.Context(); ctx != nil {
		if httpConn, ok := ctx.(*Connection); ok {
			_ = httpConn.Close()
		}
	}

	// Remove from active connections list
	s.activeConnsMu.Lock()
	for i, conn := range s.activeConns {
		if conn == c {
			// Swap with last element and truncate
			s.activeConns[i] = s.activeConns[len(s.activeConns)-1]
			s.activeConns = s.activeConns[:len(s.activeConns)-1]
			break
		}
	}
	s.activeConnsMu.Unlock()

	if err != nil {
		s.logger.Printf("Connection closed with error: %v", err)
	} else {
		s.logger.Printf("Connection closed from %s", c.RemoteAddr().String())
	}

	return gnet.None
}

// OnTraffic is called when data is received on a connection
func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	ctx := c.Context()
	if ctx == nil {
		s.logger.Printf("Connection context not found")
		return gnet.Close
	}

	conn, ok := ctx.(*Connection)
	if !ok {
		s.logger.Printf("Invalid connection context type")
		return gnet.Close
	}

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
	prefaceStart    time.Time
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
	// Track if a HEADERS frame without END_HEADERS was observed before parsing
	openHeaderBlock       bool
	openHeaderBlockStream uint32
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
	conn.prefaceStart = time.Now()

	// Create a writer that writes to the connection
	cw := &connWriter{conn: c, mu: &sync.Mutex{}, logger: logger}
	conn.writer = frame.NewWriter(cw)
	cw.parent = conn
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
		// Preface timeout guard: if peer hasn't sent complete preface within 1s, terminate
		if time.Since(c.prefaceStart) > 1*time.Second && c.buffer.Len() > 0 {
			_ = c.processor.SendGoAway(0, http2.ErrCodeProtocol, []byte("preface timeout"))
			_ = c.conn.Close()
			return nil
		}
		// If we have some bytes and they deviate from the required preface prefix, immediately reject
		if c.buffer.Len() > 0 && c.buffer.Len() < len(http2Preface) {
			if !bytes.HasPrefix([]byte(http2Preface), c.buffer.Bytes()) {
				if verboseLogging {
					c.logger.Printf("[H2] Preface prefix mismatch: have=%q len=%d", c.buffer.String(), c.buffer.Len())
				}
				_ = c.processor.SendGoAway(0, http2.ErrCodeProtocol, []byte("invalid connection preface"))
				// Close promptly after attempting to flush GOAWAY
				_ = c.writer.Flush()
				_ = c.conn.Wake(nil)
				time.AfterFunc(5*time.Millisecond, func() { _ = c.conn.Close() })
				return nil
			} else if verboseLogging {
				c.logger.Printf("[H2] Preface prefix OK so far: len=%d", c.buffer.Len())
			}
		}
		if c.buffer.Len() >= len(http2Preface) {
			preface := make([]byte, len(http2Preface))
			_, _ = c.buffer.Read(preface)

			if string(preface) != http2Preface {
				c.logger.Printf("Invalid preface: %q", string(preface))
				// Send GOAWAY(PROTOCOL_ERROR) and close the connection per RFC 7540 §3.5
				_ = c.processor.SendGoAway(0, http2.ErrCodeProtocol, []byte("invalid connection preface"))
				// Delay-close to allow GOAWAY to flush via AsyncWritev callback
				time.AfterFunc(5*time.Millisecond, func() { _ = c.conn.Close() })
				return nil
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

		// If expecting a CONTINUATION, we can decide on protocol errors based on header alone
		// without requiring the full payload to be present.
		if c.processor.IsExpectingContinuation() {
			if expID, ok := c.processor.GetExpectedContinuationStreamID(); ok {
				if streamID == expID && ftype != http2.FrameContinuation {
					if verboseLogging {
						c.logger.Printf("Protocol error: received %v on stream %d while expecting CONTINUATION; sending GOAWAY", ftype, streamID)
					}
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("expected CONTINUATION on stream"))
					return nil
				}
				if streamID != 0 && streamID != expID {
					// HEADERS to another stream while in header block is a connection error
					if ftype == http2.FrameHeaders {
						if verboseLogging {
							c.logger.Printf("Protocol error: HEADERS on stream %d while header block open on %d; GOAWAY", streamID, expID)
						}
						_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("HEADERS while header block open"))
						_ = c.writer.Flush()
						_ = c.conn.Close()
						return nil
					}
					// Unknown extension frames mid-block are also connection errors; treat types not in core set
					if ftype != http2.FrameContinuation && ftype != http2.FrameSettings && ftype != http2.FramePing && ftype != http2.FrameWindowUpdate && ftype != http2.FrameGoAway && ftype != http2.FramePriority && ftype != http2.FrameRSTStream && ftype != http2.FrameData {
						_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("unknown frame mid header block"))
						_ = c.writer.Flush()
						_ = c.conn.Close()
						return nil
					}
				}
			}
		}

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

		// If expecting a CONTINUATION on a given stream:
		// - any non-CONTINUATION on that SAME stream is a connection error
		// - a HEADERS frame on ANY other stream is a connection error (h2spec 4.3.3)
		// - unknown extension frames mid-block are also connection errors
		if c.processor.IsExpectingContinuation() {
			if expID, ok := c.processor.GetExpectedContinuationStreamID(); ok {
				if streamID == expID && ftype != http2.FrameContinuation {
					if verboseLogging {
						c.logger.Printf("Protocol error: received %v on stream %d while expecting CONTINUATION; sending GOAWAY", ftype, streamID)
					}
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("expected CONTINUATION on stream"))
					_ = c.writer.Flush()
					_ = c.conn.Close()
					return nil
				}
				// If another HEADERS arrives on a different stream while a header block is open, error
				if streamID != 0 && streamID != expID && ftype == http2.FrameHeaders {
					if verboseLogging {
						c.logger.Printf("Protocol error: HEADERS on stream %d while header block open on %d; GOAWAY", streamID, expID)
					}
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("HEADERS while header block open"))
					_ = c.writer.Flush()
					_ = c.conn.Close()
					return nil
				}
				// Treat unknown extension frames mid-block as connection error
				if ftype > http2.FrameContinuation && ftype != http2.FrameSettings && ftype != http2.FramePing && ftype != http2.FrameWindowUpdate && ftype != http2.FrameGoAway && ftype != http2.FramePriority && ftype != http2.FrameRSTStream && ftype != http2.FrameData {
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("unknown frame mid header block"))
					_ = c.writer.Flush()
					_ = c.conn.Close()
					return nil
				}
			}
		}

		// If we previously saw a HEADERS without END_HEADERS and haven't parsed it yet,
		// then a HEADERS on a different stream is a connection-level error per 4.3.3/6.2.2.
		if c.openHeaderBlock && streamID != 0 && streamID != c.openHeaderBlockStream && ftype == http2.FrameHeaders {
			if verboseLogging {
				c.logger.Printf("Protocol error: HEADERS on stream %d while header block pending on %d (pre-parse); GOAWAY", streamID, c.openHeaderBlockStream)
			}
			_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("HEADERS while header block open (pre-parse)"))
			_ = c.writer.Flush()
			_ = c.conn.Close()
			return nil
		}

		// Log HEADERS peek context to debug closed-stream handling (h2spec 5.1.12)
		if verboseLogging && ftype == http2.FrameHeaders {
			m := c.processor.GetManager()
			state := int(-1)
			exists := false
			if s, ok := m.GetStream(streamID); ok {
				exists = true
				state = int(s.GetState())
			}
			c.logger.Printf("[H2] Pre-HEADERS peek sid=%d exists=%v state=%d last=%d lastClient=%d buf=%d", streamID, exists, state, m.GetLastStreamID(), m.GetLastClientStreamID(), c.buffer.Len())
		}

		// Intercept frames targeting a stream that is already closed on our side.
		// Per RFC 7540 §5.1, receipt of HEADERS/DATA/CONTINUATION on a closed stream
		// is a connection error of type STREAM_CLOSED (as tested by h2spec 5.1.12, 5.1.11, 5.1.13).
		if streamID != 0 {
			m := c.processor.GetManager()
			if s, ok := m.GetStream(streamID); ok {
				st := s.GetState()
				if st == stream.StateClosed || st == stream.StateHalfClosedRemote {
					switch ftype {
					case http2.FrameHeaders, http2.FrameData, http2.FrameContinuation:
						if verboseLogging {
							c.logger.Printf("[H2] Intercept frame %v on non-open stream sid=%d state=%d; sending GOAWAY STREAM_CLOSED", ftype, streamID, st)
						}
						_ = c.processor.SendGoAway(m.GetLastClientStreamID(), http2.ErrCodeStreamClosed, []byte("frame on closed stream"))
						_ = c.writer.Flush()
						return nil
					}
				}
			} else {
				// Stream not found: if client reuses a previously closed stream ID, this is STREAM_CLOSED
				m := c.processor.GetManager()
				if streamID <= m.GetLastClientStreamID() {
					if verboseLogging {
						c.logger.Printf("[H2] Intercept frame %v on reused closed stream sid=%d lastClient=%d; sending GOAWAY STREAM_CLOSED", ftype, streamID, m.GetLastClientStreamID())
					}
					_ = c.processor.SendGoAway(m.GetLastClientStreamID(), http2.ErrCodeStreamClosed, []byte("frame on closed stream (reused id)"))
					_ = c.writer.Flush()
					return nil
				}
			}
		}

		// Pre-validate frame lengths to produce correct error codes before parsing
		switch ftype {
		// Note: HEADERS-specific validations follow later in this switch; keep single HEADERS case
		case http2.FramePing:
			if length != 8 {
				if verboseLogging {
					c.logger.Printf("Invalid PING length %d", length)
				}
				// h2spec strict mode expects immediate TCP close without GOAWAY for invalid PING
				_ = c.conn.Close()
				return nil
			}
			// PING must have streamID 0
			if streamID != 0 {
				if verboseLogging {
					c.logger.Printf("PING with non-zero stream id %d", streamID)
				}
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("PING stream id must be 0"))
				_ = c.writer.Flush()
				_ = c.conn.Close()
				return nil
			}
		case http2.FramePriority:
			if length != 5 {
				if streamID == 0 {
					_ = c.processor.SendGoAway(0, http2.ErrCodeFrameSize, []byte("PRIORITY length"))
					_ = c.writer.Flush()
					_ = c.conn.Close()
					return nil
				}
				c.MarkStreamClosed(streamID)
				_ = c.WriteRSTStreamPriority(streamID, http2.ErrCodeFrameSize)
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
			// Proactively mark stream closed to drop any queued writes
			if streamID != 0 {
				c.MarkStreamClosed(streamID)
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
						// Connection-level WINDOW_UPDATE with 0 increment: GOAWAY + close
						_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("WINDOW_UPDATE increment is 0"))
						_ = c.writer.Flush()
						_ = c.conn.Close()
						return nil
					}
					// Stream-level: RST_STREAM + GOAWAY (h2spec strict expects one of them + optional close)
					_ = c.WriteRSTStreamPriority(streamID, http2.ErrCodeProtocol)
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("WINDOW_UPDATE increment is 0 on stream"))
					_ = c.writer.Flush()
					_ = c.conn.Close()
					return nil
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
			// If we're currently expecting CONTINUATION on another stream, any HEADERS on a
			// different stream is a connection error (RFC 7540 §4.3). Handle it here so
			// no other logic (like MAX_CONCURRENT_STREAMS) sends RST first.
			skipConcurrency := false
			if c.processor.IsExpectingContinuation() {
				expID, _ := c.processor.GetExpectedContinuationStreamID()
				if streamID != expID {
					_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("HEADERS on other stream during header block"))
					_ = c.writer.Flush()
					_ = c.conn.Close()
					return nil
				}
				// Allow the frame on the expected stream but avoid concurrent stream enforcement
				skipConcurrency = true
			}
			// Also check openHeaderBlock flag for pre-parse violations
			if c.openHeaderBlock && streamID != 0 && streamID != c.openHeaderBlockStream {
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("HEADERS on other stream during open header block"))
				_ = c.writer.Flush()
				_ = c.conn.Close()
				return nil
			}
			// Enforce MAX_CONCURRENT_STREAMS before parsing HEADERS payload
			if streamID != 0 {
				m := c.processor.GetManager()
				if _, ok := m.GetStream(streamID); !ok {
					// If a header block is currently open (or continuation expected), do not
					// enforce concurrency here; wait for the header sequence resolution.
					if !c.openHeaderBlock && !skipConcurrency {
						//nolint:gosec // safe: CountActiveStreams returns small int, compare with uint32 cast
						if uint32(m.CountActiveStreams()+1) > m.GetMaxConcurrentStreams() {
							_ = c.WriteRSTStreamPriority(streamID, http2.ErrCodeRefusedStream)
							c.MarkStreamClosed(streamID)
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
			}
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
		// Track a HEADERS without END_HEADERS before parsing to detect racing HEADERS on other streams
		if ftype == http2.FrameHeaders {
			// If END_HEADERS flag is not set, remember this stream until we parse and hand off
			if (flags & http2.FlagHeadersEndHeaders) == 0 {
				c.openHeaderBlock = true
				c.openHeaderBlockStream = streamID
			} else {
				c.openHeaderBlock = false
				c.openHeaderBlockStream = 0
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

		// Clear pre-parse HEADERS tracking once we deliver the frame to the processor
		if frame.Header().Type == http2.FrameHeaders {
			if frame.Header().Flags&http2.FlagHeadersEndHeaders != 0 {
				c.openHeaderBlock = false
				c.openHeaderBlockStream = 0
			} else {
				c.openHeaderBlock = true
				c.openHeaderBlockStream = frame.Header().StreamID
			}
		}

		// Intercept HEADERS on closed streams to emit GOAWAY(STREAM_CLOSED) per RFC 7540 §5.1/12
		if frame.Header().Type == http2.FrameHeaders {
			sid := frame.Header().StreamID
			m := c.processor.GetManager()
			if s, ok := m.GetStream(sid); ok {
				if verboseLogging {
					c.logger.Printf("[H2] HEADERS sid=%d exists=true state=%d last=%d", sid, s.GetState(), m.GetLastStreamID())
				}
				if s.GetState() == stream.StateClosed || s.GetState() == stream.StateHalfClosedRemote {
					if verboseLogging {
						c.logger.Printf("[H2] Intercept HEADERS on non-open stream sid=%d state=%d last=%d; sending GOAWAY STREAM_CLOSED", sid, s.GetState(), m.GetLastStreamID())
					}
					_ = c.processor.SendGoAway(m.GetLastClientStreamID(), http2.ErrCodeStreamClosed, []byte("HEADERS on closed stream (state closed)"))
					// Let upper SendGoAway decide close timing; do not close here to avoid races
					return nil
				}
			} else {
				// No stream found; if stream ID <= last client stream, treat as closed stream reuse
				if verboseLogging {
					c.logger.Printf("[H2] HEADERS sid=%d exists=false last=%d lastClient=%d", sid, m.GetLastStreamID(), m.GetLastClientStreamID())
				}
				if sid <= m.GetLastClientStreamID() {
					if verboseLogging {
						c.logger.Printf("[H2] Intercept HEADERS on reused closed stream sid=%d lastClient=%d; sending GOAWAY STREAM_CLOSED", sid, m.GetLastClientStreamID())
					}
					_ = c.processor.SendGoAway(m.GetLastClientStreamID(), http2.ErrCodeStreamClosed, []byte("HEADERS on closed stream (reused id)"))
					// Do not close here; allow client to read GOAWAY
					return nil
				}
			}
		}

		// Process the frame with connection context
		// If we're expecting a CONTINUATION and a non-CONTINUATION arrives on the same stream, send GOAWAY(PROTOCOL_ERROR)
		if c.processor.IsExpectingContinuation() {
			if expID, ok := c.processor.GetExpectedContinuationStreamID(); ok && expID == streamID && ftype != http2.FrameContinuation {
				_ = c.processor.SendGoAway(c.processor.GetManager().GetLastStreamID(), http2.ErrCodeProtocol, []byte("expected CONTINUATION"))
				_ = c.writer.Flush()
				return nil
			}
		}
		if err := c.processor.ProcessFrameWithConn(ctx, frame, c); err != nil {
			c.logger.Printf("Error processing frame: %v", err)
			// Continue processing other frames
		}
		// Do not prematurely return on END_STREAM; continue parsing to prioritize errors
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
	// Prefer larger frames inbound and larger inbound window to reduce syscall pressure.
	// Increase HPACK table size to improve header compression for repeated fields
	settings := []http2.Setting{
		{ID: http2.SettingHeaderTableSize, Val: 16384},
		{ID: http2.SettingMaxConcurrentStreams, Val: c.processor.GetManager().GetMaxConcurrentStreams()},
		{ID: http2.SettingMaxFrameSize, Val: 65535},        // allow peer to send larger frames to us
		{ID: http2.SettingInitialWindowSize, Val: 1 << 20}, // 1MB inbound window per stream
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
		if st.ClosedByReset {
			return fmt.Errorf("stream %d was reset by peer", streamID)
		}
		// If HeadersSent is false but phase != Init, stream was refused/reset before response
		if !st.HeadersSent && st.GetPhase() != stream.PhaseInit {
			return fmt.Errorf("stream %d was refused/reset", streamID)
		}
	}

	// If headers were already sent for this stream, write DATA only for streaming Flush calls
	if st, ok := c.processor.GetManager().GetStream(streamID); ok && st.HeadersSent {
		if st.ClosedByReset {
			return fmt.Errorf("stream %d was reset by peer", streamID)
		}
		// Do not write any response content if stream was reset by peer
		if st.ClosedByReset {
			return fmt.Errorf("stream %d was reset by peer", streamID)
		}
		// If peer has half-closed remote, accept WINDOW_UPDATE/PRIORITY silently and do not emit HEADERS again.
		if st.GetState() == stream.StateHalfClosedRemote {
			// Only send DATA if body provided; otherwise ignore
			if len(body) == 0 {
				return nil
			}
		}
		// Only write DATA frames with endStream=false when there is body
		if len(body) > 0 {
			// LOCK ONLY for writing
			c.writeMu.Lock()
			defer c.writeMu.Unlock()

			// Respect windows
			connWin, streamWin, maxFrame := c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
			if maxFrame == 0 {
				maxFrame = 16384
			}
			remaining := body
			for len(remaining) > 0 && connWin > 0 && streamWin > 0 && maxFrame > 0 {
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
				if err := c.writer.WriteData(streamID, false, chunk); err != nil {
					return err
				}
				//nolint:gosec // G115: safe conversion; chunk size is bounded by flow-control windows and MAX_FRAME_SIZE
				c.processor.GetManager().ConsumeSendWindow(streamID, int32(len(chunk)))
				// refresh windows for next loop
				connWin, streamWin, maxFrame = c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
				if maxFrame == 0 {
					maxFrame = 16384
				}
			}
			// Flush to ensure chunk is sent
			if err := c.writer.Flush(); err != nil {
				return err
			}
			_ = c.conn.Wake(nil)
			return nil
		}
		// No body: do not emit zero-length DATA; rely on HEADERS END_STREAM or later state transitions.
		return nil
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
		*pooled = hdrs[:0]
		headersSlicePool.Put(pooled)
		return nil
	}

	// Serialize HPACK encoding and write with connection-level lock to avoid concurrent encoder use.
	c.writeMu.Lock()
	// Use Encode (copy) so the returned slice remains valid while holding the lock
	headerBlock, err := c.headerEncoder.Encode(allHeaders)
	// return the pooled slice
	*pooled = allHeaders[:0]
	headersSlicePool.Put(pooled)
	if err != nil {
		c.writeMu.Unlock()
		c.logger.Printf("ERROR encoding headers: %v", err)
		return fmt.Errorf("failed to encode headers: %w", err)
	}

	// Write HEADERS (and CONTINUATION) respecting peer MAX_FRAME_SIZE
	// If no body is present, we can end the stream with HEADERS (END_STREAM) to avoid zero-length DATA later
	endStream := len(body) == 0
	if verboseLogging {
		c.logger.Printf("[H2] WriteResponse HEADERS: sid=%d end=%v bodyLen=%d", streamID, endStream, len(body))
	}
	_, _, maxFrame := c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
	if maxFrame == 0 {
		maxFrame = 16384
	}
	// avoid logging in hot path
	if err := c.writer.WriteHeaders(streamID, endStream, headerBlock, maxFrame); err != nil {
		c.logger.Printf("ERROR writing HEADERS: %v", err)
		c.writeMu.Unlock()
		return fmt.Errorf("failed to write headers: %w", err)
	}

	// Mark headers as sent BEFORE flushing
	if st, ok := c.processor.GetManager().GetStream(streamID); ok {
		if st.ClosedByReset {
			c.writeMu.Unlock()
			return fmt.Errorf("stream %d was reset by peer", streamID)
		}
		st.HeadersSent = true
		st.SetPhase(stream.PhaseHeadersSent)
		// Don't set IsStreaming for normal responses - reserve it for actual streaming
	}

	// If there is a body, choose adaptive path: small direct streaming, large buffered micro-batching
	if len(body) > 0 {
		const directThreshold = 32 * 1024 // 32 KiB
		if len(body) <= directThreshold {
			// Direct streaming under lock for tiny bodies
			connWin, streamWin, maxFrame := c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
			if maxFrame == 0 {
				maxFrame = 16384
			}
			remaining := body
			for len(remaining) > 0 && connWin > 0 && streamWin > 0 && maxFrame > 0 {
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
				end := len(remaining) == 0 // endStream on final chunk
				if err := c.writer.WriteData(streamID, end, chunk); err != nil {
					return err
				}
				//nolint:gosec // bounded by windows
				c.processor.GetManager().ConsumeSendWindow(streamID, int32(len(chunk)))
				connWin, streamWin, maxFrame = c.processor.GetManager().GetSendWindowsAndMaxFrame(streamID)
				if maxFrame == 0 {
					maxFrame = 16384
				}
				if end {
					if st, ok := c.processor.GetManager().GetStream(streamID); ok {
						if st.EndStream {
							st.SetState(stream.StateClosed)
						} else {
							st.SetState(stream.StateHalfClosedLocal)
						}
					}
				}
			}
		} else {
			// Buffered micro-batching for larger bodies
			if s, ok := c.processor.GetManager().GetStream(streamID); ok && s.OutboundBuffer != nil {
				_, _ = s.OutboundBuffer.Write(body)
				s.OutboundEndStream = true
			}
			c.processor.FlushBufferedDataLocked(streamID)
		}
		// Flush HEADERS + DATA together
		if err := c.writer.Flush(); err != nil {
			c.writeMu.Unlock()
			return err
		}
		_ = c.conn.Wake(nil)
		c.writeMu.Unlock()
		return nil
	}

	// No body: flush HEADERS only
	if err := c.writer.Flush(); err != nil {
		c.writeMu.Unlock()
		return err
	}
	_ = c.conn.Wake(nil)

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
	c.writeMu.Unlock()
	return nil
}

// connWriter implements io.Writer for gnet.Conn
type connWriter struct {
	conn   gnet.Conn
	mu     *sync.Mutex
	logger *log.Logger
	// pending holds individual frame slices ready to send via AsyncWritev.
	// Each element must be a full HTTP/2 frame (header+payload) slice referencing
	// a stable backing array.
	pending  [][]byte
	inflight bool
	// queued holds additional frames to send after inflight batch completes.
	queued [][]byte
	parent *Connection
}

// filterFrames removes frames targeting streams that have been closed/reset.
// filterFrames is retained for documentation/reference; use inline filtering in Flush.
// Deprecated: kept to avoid re-adding in future refactors.
//
//lint:ignore U1000 retained for future reference
func (w *connWriter) filterFrames(_ []byte) []byte { return nil }

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

	// Copy the entire buffer once to ensure lifetime across async sends
	if len(p) == 0 {
		return 0, nil
	}
	data := make([]byte, len(p))
	copy(data, p)

	// Parse frames and queue kept segments referencing the stable backing array
	off := 0
	var segments [][]byte
	for len(data)-off >= 9 {
		length := int(uint32(data[off])<<16 | uint32(data[off+1])<<8 | uint32(data[off+2]))
		ftype := http2.FrameType(data[off+3])
		// flags := http2.Flags(data[off+4]) // Only for debug
		sid := binary.BigEndian.Uint32(data[off+5:off+9]) & 0x7fffffff
		consume := 9 + length
		if consume <= 0 || off+consume > len(data) {
			break
		}
		keep := true
		if sid != 0 && w.parent != nil && ftype != http2.FrameRSTStream {
			if s, ok := w.parent.processor.GetManager().GetStream(sid); ok && s.ClosedByReset {
				keep = false
			}
			if keep && w.parent.IsStreamClosed(sid) {
				keep = false
			}
		}
		if keep {
			segments = append(segments, data[off:off+consume])
		}
		off += consume
	}

	// Serialize appending pending segments
	w.mu.Lock()
	if len(segments) > 0 {
		w.pending = append(w.pending, segments...)
	}
	w.mu.Unlock()
	return len(p), nil
}

// Flush ensures data is sent by calling gnet's Flush
func (w *connWriter) Flush() error {
	w.mu.Lock()
	if w.inflight {
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
	// Inline filter without extra copies
	filtered := w.filterPartsNoCopy(batch)
	if len(filtered) == 0 {
		w.mu.Unlock()
		_ = w.conn.Wake(nil)
		return nil
	}
	w.inflight = true
	w.mu.Unlock()
	return w.asyncSend(filtered)
}

// filterPartsNoCopy filters parts against closed/reset streams without copying.
func (w *connWriter) filterPartsNoCopy(parts [][]byte) [][]byte {
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if len(part) < 9 {
			continue
		}
		sid := binary.BigEndian.Uint32(part[5:9]) & 0x7fffffff
		ftype := http2.FrameType(part[3])
		keep := true
		if sid != 0 && w.parent != nil && ftype != http2.FrameRSTStream {
			if s, ok := w.parent.processor.GetManager().GetStream(sid); ok && s.ClosedByReset {
				keep = false
			}
			if keep && w.parent.IsStreamClosed(sid) {
				keep = false
			}
		}
		if keep {
			out = append(out, part)
		}
	}
	return out
}

// asyncSend sends parts via AsyncWritev and drains queued parts recursively.
func (w *connWriter) asyncSend(parts [][]byte) error {
	return w.conn.AsyncWritev(parts, func(_ gnet.Conn, err error) error {
		if verboseLogging && err != nil {
			w.logger.Printf("AsyncWritev callback error: %v", err)
		}
		w.mu.Lock()
		next := w.queued
		if len(next) > 0 {
			w.queued = nil
			// Filter queued parts just-in-time
			filtered := w.filterPartsNoCopy(next)
			if len(filtered) == 0 {
				w.inflight = false
				w.mu.Unlock()
				return nil
			}
			w.inflight = true
			w.mu.Unlock()
			return w.asyncSend(filtered)
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

	// Signal connection should close for connection-level errors
	// Return error so OnTraffic can return gnet.Close action
	switch code {
	case http2.ErrCodeProtocol, http2.ErrCodeFrameSize, http2.ErrCodeStreamClosed, http2.ErrCodeCompression, http2.ErrCodeFlowControl:
		c.logger.Printf("Closing connection after GOAWAY with code=%v", code)
		// Set a flag that HandleData can check to return error
		c.sentGoAway.Store(true)
		_ = c.conn.Close() // Also call Close() directly
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

// StoreConnection is kept for compatibility but now uses Conn.Context().
// This is used by the multiplexer to register connections.
func (s *Server) StoreConnection(c gnet.Conn, conn *Connection) {
	c.SetContext(conn)

	// Also track for shutdown
	s.activeConnsMu.Lock()
	s.activeConns = append(s.activeConns, c)
	s.activeConnsMu.Unlock()
}
