package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// State represents the state of an HTTP/2 stream
type State int

// HTTP/2 stream states per RFC 7540
const (
	StateIdle State = iota
	StateOpen
	StateHalfClosedLocal
	StateHalfClosedRemote
	StateClosed
)

// Phase represents the response phase for a stream (for write ordering).
type Phase int

const (
	// PhaseInit indicates no response has been sent yet.
	PhaseInit Phase = iota
	// PhaseHeadersSent indicates HEADERS have been flushed.
	PhaseHeadersSent
	// PhaseBody indicates response body is being sent.
	PhaseBody
)

// Stream represents an HTTP/2 stream
type Stream struct {
	ID                     uint32
	State                  State
	manager                *Manager
	Headers                [][2]string
	Trailers               [][2]string
	Data                   *bytes.Buffer
	OutboundBuffer         *bytes.Buffer
	OutboundEndStream      bool
	HeadersSent            bool
	EndStream              bool
	WindowSize             int32
	ReceivedWindowUpd      chan int32
	mu                     sync.RWMutex
	ResponseWriter         ResponseWriter // Connection for writing responses
	ReceivedDataLen        int            // Track total data received for content-length validation
	ReceivedInitialHeaders bool
	ClosedByReset          bool
	ctx                    context.Context
	cancel                 context.CancelFunc
	phase                  Phase
}

var bufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func getBuf() *bytes.Buffer {
	b := bufferPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

// putBuf reserved for future reuse; intentionally left unused in current flow.

// NewStream creates a new stream
func NewStream(id uint32) *Stream {
	ctx, cancel := context.WithCancel(context.Background())
	return &Stream{
		ID:                id,
		State:             StateIdle,
		Data:              getBuf(),
		OutboundBuffer:    getBuf(),
		WindowSize:        65535, // Default window size
		ReceivedWindowUpd: make(chan int32, 1),
		ctx:               ctx,
		cancel:            cancel,
		phase:             PhaseInit,
	}
}

// AddHeader adds a header to the stream
func (s *Stream) AddHeader(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Headers = append(s.Headers, [2]string{name, value})
}

// AddData adds data to the stream buffer
func (s *Stream) AddData(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.Data.Write(data)
	return err
}

// GetData returns the buffered data
func (s *Stream) GetData() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Data.Bytes()
}

// GetHeaders returns a copy of the headers
func (s *Stream) GetHeaders() [][2]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	headers := make([][2]string, len(s.Headers))
	copy(headers, s.Headers)
	return headers
}

// HeadersLen returns the number of headers on the stream.
func (s *Stream) HeadersLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Headers)
}

// ForEachHeader calls fn for each header under a read lock.
func (s *Stream) ForEachHeader(fn func(name, value string)) {
	s.mu.RLock()
	for _, h := range s.Headers {
		fn(h[0], h[1])
	}
	s.mu.RUnlock()
}

// SetState sets the stream state
func (s *Stream) SetState(state State) {
	s.mu.Lock()
	prev := s.State
	s.State = state
	s.mu.Unlock()
	if s.manager != nil {
		s.manager.mu.Lock()
		s.manager.markActiveTransition(prev, state)
		s.manager.mu.Unlock()
	}
}

// GetState returns the current stream state
func (s *Stream) GetState() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State
}

// GetWindowSize returns the current flow control window size
func (s *Stream) GetWindowSize() int32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.WindowSize
}

// SetPhase sets the stream phase
func (s *Stream) SetPhase(p Phase) {
	s.mu.Lock()
	s.phase = p
	s.mu.Unlock()
}

// GetPhase returns the current stream phase
func (s *Stream) GetPhase() Phase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phase
}

// Manager manages multiple HTTP/2 streams
type Manager struct {
	streams           map[uint32]*Stream
	nextStreamID      uint32
	lastClientStream  uint32 // Track last client-initiated stream ID
	mu                sync.RWMutex
	connectionWindow  int32
	maxStreams        uint32
	priorityTree      *PriorityTree
	pushEnabled       bool
	nextPushID        uint32
	maxFrameSize      uint32 // Current MAX_FRAME_SIZE setting
	initialWindowSize uint32 // Current INITIAL_WINDOW_SIZE setting
	activeStreams     uint32 // Count of active streams (Open or Half-Closed)
}

// NewManager creates a new stream manager
func NewManager() *Manager {
	return &Manager{
		streams:           make(map[uint32]*Stream),
		nextStreamID:      1, // Client-initiated streams are odd
		connectionWindow:  65535,
		maxStreams:        100,
		priorityTree:      NewPriorityTree(),
		pushEnabled:       true,
		nextPushID:        2,     // Server-initiated streams are even
		maxFrameSize:      16384, // Default per RFC 7540
		initialWindowSize: 65535, // Default per RFC 7540
		activeStreams:     0,
	}
}

// SetMaxConcurrentStreams sets the maximum number of concurrent peer-initiated streams allowed.
func (m *Manager) SetMaxConcurrentStreams(n uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxStreams = n
}

// GetMaxConcurrentStreams returns the currently configured max concurrent streams value.
func (m *Manager) GetMaxConcurrentStreams() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxStreams
}

// CreateStream creates a new stream with the given ID
func (m *Manager) CreateStream(id uint32) *Stream {
	m.mu.Lock()
	defer m.mu.Unlock()

	stream := NewStream(id)
	stream.manager = m
	// Set initial window size from settings (validated <= 2^31-1 by SETTINGS handler)
	//nolint:gosec // G115: safe conversion, initialWindowSize validated by protocol
	stream.WindowSize = int32(m.initialWindowSize)
	m.streams[id] = stream
	return stream
}

// GetStream returns a stream by ID
func (m *Manager) GetStream(id uint32) (*Stream, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stream, ok := m.streams[id]
	return stream, ok
}

// GetOrCreateStream gets an existing stream or creates a new one
func (m *Manager) GetOrCreateStream(id uint32) *Stream {
	if stream, ok := m.GetStream(id); ok {
		return stream
	}
	return m.CreateStream(id)
}

// TryOpenStream attempts to atomically open a new stream and mark it active.
// Returns the opened stream and true on success; returns false if the
// MAX_CONCURRENT_STREAMS limit would be exceeded.
func (m *Manager) TryOpenStream(id uint32) (*Stream, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeStreams >= m.maxStreams {
		return nil, false
	}
	if s, exists := m.streams[id]; exists {
		return s, true
	}
	s := NewStream(id)
	s.manager = m
	//nolint:gosec // G115: safe conversion, initialWindowSize validated by protocol
	s.WindowSize = int32(m.initialWindowSize)
	s.State = StateOpen
	m.streams[id] = s
	m.activeStreams++
	return s, true
}

// DeleteStream removes a stream
func (m *Manager) DeleteStream(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.streams, id)
	m.priorityTree.RemoveStream(id)
}

// StreamCount returns the number of active streams
func (m *Manager) StreamCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.streams)
}

// GetLastStreamID returns the highest stream ID
func (m *Manager) GetLastStreamID() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var lastID uint32
	for id := range m.streams {
		if id > lastID {
			lastID = id
		}
	}
	return lastID
}

// UpdateConnectionWindow updates the connection-level flow control window
func (m *Manager) UpdateConnectionWindow(delta int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionWindow += delta
}

// GetConnectionWindow returns the current connection window size
func (m *Manager) GetConnectionWindow() int32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connectionWindow
}

// CountActiveStreams returns number of streams considered active for concurrency limits.
// Active streams are those in Open or Half-Closed states (local or remote).
func (m *Manager) CountActiveStreams() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int(m.activeStreams)
}

// markActiveTransition adjusts activeStreams when a stream transitions between active and inactive states.
func (m *Manager) markActiveTransition(prev State, next State) {
	wasActive := prev == StateOpen || prev == StateHalfClosedLocal || prev == StateHalfClosedRemote
	isActive := next == StateOpen || next == StateHalfClosedLocal || next == StateHalfClosedRemote
	if wasActive == isActive {
		return
	}
	if isActive {
		m.activeStreams++
	} else if m.activeStreams > 0 {
		m.activeStreams--
	}
}

// GetSendWindowsAndMaxFrame returns current connection window, stream window, and max frame size.
func (m *Manager) GetSendWindowsAndMaxFrame(streamID uint32) (connWindow int32, streamWindow int32, maxFrame uint32) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	connWindow = m.connectionWindow
	if s, ok := m.streams[streamID]; ok {
		streamWindow = s.WindowSize
	} else {
		//nolint:gosec // G115: safe conversion, initialWindowSize validated by protocol
		streamWindow = int32(m.initialWindowSize)
	}
	maxFrame = m.maxFrameSize
	return
}

// ConsumeSendWindow decrements connection and stream windows after sending DATA.
func (m *Manager) ConsumeSendWindow(streamID uint32, n int32) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	m.connectionWindow -= n
	if s, ok := m.streams[streamID]; ok {
		s.WindowSize -= n
	}
	m.mu.Unlock()
}

// Handler interface for processing streams
type Handler interface {
	HandleStream(ctx context.Context, stream *Stream) error
}

// HandlerFunc is an adapter to use functions as stream handlers
type HandlerFunc func(ctx context.Context, stream *Stream) error

// HandleStream calls the handler function
func (f HandlerFunc) HandleStream(ctx context.Context, stream *Stream) error {
	return f(ctx, stream)
}

// Processor processes incoming HTTP/2 frames and manages streams
type Processor struct {
	manager             *Manager
	handler             Handler
	writer              FrameWriter
	currentConn         ResponseWriter
	connWriter          ResponseWriter // Permanent reference to connection
	hpackDecoder        *hpack.Decoder
	continuationState   *ContinuationState
	continuationStateMu sync.Mutex
}

// ContinuationState tracks the state of CONTINUATION frames
type ContinuationState struct {
	streamID      uint32
	headerBlock   []byte
	endStream     bool
	expectingMore bool
	isTrailers    bool
}

var headerBlockPool = sync.Pool{New: func() any { b := make([]byte, 0, 4096); return &b }}

// headersSlicePoolIn is used for incoming request headers/trailers to reduce allocations during HPACK decode.
var headersSlicePoolIn = sync.Pool{New: func() any { s := make([][2]string, 0, 16); return &s }}

// FrameWriter is an interface for writing HTTP/2 frames
type FrameWriter interface {
	WriteSettings(settings ...http2.Setting) error
	WriteSettingsAck() error
	// WriteHeaders writes HEADERS (and CONTINUATION) frames, fragmenting using maxFrameSize
	WriteHeaders(streamID uint32, endStream bool, headerBlock []byte, maxFrameSize uint32) error
	WriteData(streamID uint32, endStream bool, data []byte) error
	WriteWindowUpdate(streamID uint32, increment uint32) error
	WriteRSTStream(streamID uint32, code http2.ErrCode) error
	WriteGoAway(lastStreamID uint32, code http2.ErrCode, debugData []byte) error
	WritePing(ack bool, data [8]byte) error
	WritePushPromise(streamID uint32, promiseID uint32, endHeaders bool, headerBlock []byte) error
}

// NewProcessor creates a new stream processor
func NewProcessor(handler Handler, writer FrameWriter, conn ResponseWriter) *Processor {
	return &Processor{
		manager:      NewManager(),
		handler:      handler,
		writer:       writer,
		connWriter:   conn,
		hpackDecoder: hpack.NewDecoder(4096, nil), // 4KB header table size
	}
}

// ResponseWriter is an interface for writing responses
type ResponseWriter interface {
	WriteResponse(streamID uint32, status int, headers [][2]string, body []byte) error
	SendGoAway(lastStreamID uint32, code http2.ErrCode, debug []byte) error
	MarkStreamClosed(streamID uint32)
	IsStreamClosed(streamID uint32) bool
	// WriteRSTStreamPriority writes an RST_STREAM immediately with strict ordering
	WriteRSTStreamPriority(streamID uint32, code http2.ErrCode) error
	CloseConn() error
}

// ProcessFrame processes an incoming HTTP/2 frame
func (p *Processor) ProcessFrame(ctx context.Context, frame http2.Frame) error {
	// Check if we're in CONTINUATION state
	p.continuationStateMu.Lock()
	inContinuation := p.continuationState != nil && p.continuationState.expectingMore
	expectingStreamID := uint32(0)
	if inContinuation {
		expectingStreamID = p.continuationState.streamID
	}
	p.continuationStateMu.Unlock()

	if inContinuation {
		header := frame.Header()
		if header.StreamID == expectingStreamID {
			if _, isContinuation := frame.(*http2.ContinuationFrame); !isContinuation {
				_ = p.SendGoAway(0, http2.ErrCodeProtocol, []byte("non-CONTINUATION on stream expecting CONTINUATION"))
				if flusher, ok := p.writer.(interface{ Flush() error }); ok {
					_ = flusher.Flush()
				}
				return fmt.Errorf("received non-CONTINUATION frame on stream %d while expecting CONTINUATION", header.StreamID)
			}
		}
	}

	header := frame.Header()
	p.manager.mu.RLock()
	maxFrameSize := p.manager.maxFrameSize
	p.manager.mu.RUnlock()

	switch frame.(type) {
	case *http2.DataFrame:
		if header.Length > maxFrameSize {
			if header.StreamID == 0 {
				_ = p.SendGoAway(0, http2.ErrCodeFrameSize, []byte("DATA frame too large"))
				if flusher, ok := p.writer.(interface{ Flush() error }); ok {
					_ = flusher.Flush()
				}
				return fmt.Errorf("DATA frame exceeds MAX_FRAME_SIZE: %d > %d", header.Length, maxFrameSize)
			}
			_ = p.writer.WriteRSTStream(header.StreamID, http2.ErrCodeFrameSize)
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("DATA frame exceeds MAX_FRAME_SIZE: %d > %d", header.Length, maxFrameSize)
		}
	case *http2.HeadersFrame:
		if header.Length > maxFrameSize {
			_ = p.SendGoAway(0, http2.ErrCodeFrameSize, []byte("HEADERS frame too large"))
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("HEADERS frame exceeds MAX_FRAME_SIZE: %d > %d", header.Length, maxFrameSize)
		}
	}

	switch f := frame.(type) {
	case *http2.SettingsFrame:
		return p.handleSettings(f)
	case *http2.HeadersFrame:
		return p.handleHeaders(ctx, f)
	case *http2.DataFrame:
		return p.handleData(ctx, f)
	case *http2.WindowUpdateFrame:
		return p.handleWindowUpdate(f)
	case *http2.RSTStreamFrame:
		return p.handleRSTStream(f)
	case *http2.PriorityFrame:
		return p.handlePriority(f)
	case *http2.GoAwayFrame:
		return p.handleGoAway(f)
	case *http2.PingFrame:
		return p.handlePing(f)
	case *http2.ContinuationFrame:
		return p.handleContinuation(ctx, f)
	case *http2.PushPromiseFrame:
		if err := p.SendGoAway(0, http2.ErrCodeProtocol, []byte("client sent PUSH_PROMISE")); err != nil {
			return err
		}
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("client sent PUSH_PROMISE frame")
	default:
		return nil
	}
}

// ProcessFrameWithConn processes a frame with a connection context
func (p *Processor) ProcessFrameWithConn(ctx context.Context, frame http2.Frame, conn ResponseWriter) error {
	p.currentConn = conn
	defer func() { p.currentConn = nil }()

	return p.ProcessFrame(ctx, frame)
}

// handleSettings processes SETTINGS frames
func (p *Processor) handleSettings(f *http2.SettingsFrame) error {
	if f.IsAck() {
		return nil
	}

	// Validate and process settings
	var validationErr error
	_ = f.ForeachSetting(func(s http2.Setting) error {
		switch s.ID {
		case http2.SettingHeaderTableSize:
			// No validation needed
		case http2.SettingEnablePush:
			// Must be 0 or 1
			if s.Val != 0 && s.Val != 1 {
				validationErr = fmt.Errorf("SETTINGS_ENABLE_PUSH must be 0 or 1, got %d", s.Val)
				return validationErr
			}
			// Apply to manager
			p.manager.mu.Lock()
			p.manager.pushEnabled = s.Val == 1
			p.manager.mu.Unlock()
		case http2.SettingMaxConcurrentStreams:
			// Client's MAX_CONCURRENT_STREAMS limits server-initiated streams (push),
			// not client-initiated inbound streams. Do not overwrite our inbound limit here.
			// TODO: track peer's limit for server push if/when push is used.
		case http2.SettingInitialWindowSize:
			// Must not exceed 2^31-1 (RFC 7540 Section 6.5.2)
			if s.Val > 0x7fffffff {
				// Treat as connection error of type FLOW_CONTROL_ERROR
				validationErr = fmt.Errorf("SETTINGS_INITIAL_WINDOW_SIZE too large: %d", s.Val)
				_ = p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeFlowControl, []byte(validationErr.Error()))
				return validationErr
			}
			// Update initial window size and adjust existing streams
			p.manager.mu.Lock()
			//nolint:gosec // G115: safe conversion, values validated <= 2^31-1 above
			oldWindowSize := int32(p.manager.initialWindowSize)
			//nolint:gosec // G115: safe conversion, values validated <= 2^31-1 above
			newWindowSize := int32(s.Val)
			delta := newWindowSize - oldWindowSize
			p.manager.initialWindowSize = s.Val

			// Adjust all existing stream windows (RFC 7540 Section 6.9.2)
			for _, stream := range p.manager.streams {
				stream.mu.Lock()
				stream.WindowSize += delta
				stream.mu.Unlock()
			}
			p.manager.mu.Unlock()
		case http2.SettingMaxFrameSize:
			// Must be between 2^14 (16384) and 2^24-1 (16777215)
			if s.Val < 16384 {
				validationErr = fmt.Errorf("SETTINGS_MAX_FRAME_SIZE too small: %d", s.Val)
				return validationErr
			}
			if s.Val > 16777215 {
				validationErr = fmt.Errorf("SETTINGS_MAX_FRAME_SIZE too large: %d", s.Val)
				return validationErr
			}
			// Update max frame size
			p.manager.mu.Lock()
			p.manager.maxFrameSize = s.Val
			p.manager.mu.Unlock()
		case http2.SettingMaxHeaderListSize:
			// No specific validation
		}
		return nil
	})

	// If validation failed, send GOAWAY
	if validationErr != nil {
		if err := p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeProtocol,
			[]byte(validationErr.Error())); err != nil {
			return err
		}
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return validationErr
	}

	// Send acknowledgment
	if err := p.writer.WriteSettingsAck(); err != nil {
		return err
	}

	// Flush to ensure ACK is sent immediately
	if flusher, ok := p.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}

	return nil
}

// handleHeaders processes HEADERS frames
//
//nolint:gocyclo // HTTP/2 HEADERS frame handling requires complex state machine per RFC 7540
func (p *Processor) handleHeaders(_ context.Context, f *http2.HeadersFrame) error {
	// Check if stream exists before creating (affects stream-id validation rules)
	existingStream, exists := p.manager.GetStream(f.StreamID)
	if !exists {
		// Validate stream ID only for new streams
		p.manager.mu.RLock()
		lastClientStream := p.manager.lastClientStream
		p.manager.mu.RUnlock()

		if err := validateStreamID(f.StreamID, lastClientStream, false); err != nil {
			// Send GOAWAY for protocol error
			if err := p.SendGoAway(lastClientStream, http2.ErrCodeProtocol, []byte(err.Error())); err != nil {
				return err
			}
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return err
		}

		// Update last client stream ID
		p.manager.mu.Lock()
		if f.StreamID > p.manager.lastClientStream {
			p.manager.lastClientStream = f.StreamID
		}
		p.manager.mu.Unlock()
	}

	// Check if stream exists before creating
	if exists {
		// Stream exists, check state
		state := existingStream.GetState()
		switch state {
		case StateClosed:
			// Distinguish: if closed by reset, it's a stream error (RST_STREAM STREAM_CLOSED);
			// otherwise, it's a connection error (GOAWAY STREAM_CLOSED) per test 5.1/12.
			existingStream.mu.RLock()
			closedByReset := existingStream.ClosedByReset
			existingStream.mu.RUnlock()
			if closedByReset {
				_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeStreamClosed)
			} else {
				_ = p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeStreamClosed, []byte("HEADERS on closed stream"))
				if flusher, ok := p.writer.(interface{ Flush() error }); ok {
					_ = flusher.Flush()
				}
			}
			return fmt.Errorf("HEADERS frame on closed stream %d", f.StreamID)
		case StateHalfClosedRemote:
			// HEADERS on half-closed (remote) is invalid (no more frames should be sent by peer)
			_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeStreamClosed)
			return fmt.Errorf("HEADERS frame on half-closed (remote) stream %d", f.StreamID)
		}

		// If initial headers already received, this HEADERS is either trailers or invalid second HEADERS
		if existingStream.ReceivedInitialHeaders {
			// If this is a second HEADERS without END_STREAM, it's invalid (not trailers)
			if !f.StreamEnded() {
				_ = p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeProtocol, []byte("WINDOW_UPDATE with 0 increment"))
				if flusher, ok := p.writer.(interface{ Flush() error }); ok {
					_ = flusher.Flush()
				}
				return fmt.Errorf("second HEADERS without END_STREAM on stream %d", f.StreamID)
			}
			headerBlock := f.HeaderBlockFragment()
			if !f.HeadersEnded() {
				// Copy fragment immediately; underlying framer buffers are reused on next ReadFrame
				frag := make([]byte, len(headerBlock))
				copy(frag, headerBlock)
				p.continuationStateMu.Lock()
				p.continuationState = &ContinuationState{
					streamID:      f.StreamID,
					headerBlock:   frag,
					endStream:     f.StreamEnded(),
					expectingMore: true,
					isTrailers:    true,
				}
				p.continuationStateMu.Unlock()
				return nil
			}

			// END_HEADERS present: decode trailers (use pooled slice)
			pooledTrailers := headersSlicePoolIn.Get().(*[][2]string)
			trailers := (*pooledTrailers)[:0]
			// Always return pooled slice
			defer func() {
				*pooledTrailers = trailers[:0]
				headersSlicePoolIn.Put(pooledTrailers)
			}()
			p.hpackDecoder.SetEmitFunc(func(hf hpack.HeaderField) {
				trailers = append(trailers, [2]string{hf.Name, hf.Value})
			})
			if _, err := p.hpackDecoder.Write(headerBlock); err != nil {
				_ = p.SendGoAway(0, http2.ErrCodeCompression, []byte("HPACK decoding failed"))
				if flusher, ok := p.writer.(interface{ Flush() error }); ok {
					_ = flusher.Flush()
				}
				return fmt.Errorf("failed to decode trailers: %w", err)
			}
			if err := p.hpackDecoder.Close(); err != nil {
				_ = p.SendGoAway(0, http2.ErrCodeCompression, []byte("HPACK decoding failed"))
				if flusher, ok := p.writer.(interface{ Flush() error }); ok {
					_ = flusher.Flush()
				}
				return fmt.Errorf("failed to finalize trailers: %w", err)
			}
			if err := validateTrailerHeaders(trailers); err != nil {
				// If this is a second HEADERS without END_STREAM (non-trailers), treat as PROTOCOL_ERROR
				_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeProtocol)
				return fmt.Errorf("invalid trailers: %w", err)
			}
			for _, h := range trailers {
				existingStream.AddHeader(h[0], h[1])
			}

			if f.StreamEnded() {
				existingStream.EndStream = true
				existingStream.SetState(StateHalfClosedRemote)
				if p.handler != nil {
					sref := existingStream
					go func() {
						select {
						case <-sref.ctx.Done():
							return
						default:
						}
						if err := p.handler.HandleStream(sref.ctx, sref); err != nil {
							select {
							case <-sref.ctx.Done():
								return
							default:
								_ = p.writer.WriteRSTStream(sref.ID, http2.ErrCodeInternal)
							}
						}
					}()
				}
			}
			return nil
		}
	} else {
		// New stream - check concurrent stream limit against active states
		p.manager.mu.RLock()
		maxStreams := p.manager.maxStreams
		p.manager.mu.RUnlock()
		activeCount := p.manager.CountActiveStreams()
		// Consider the incoming stream as pending; enforce strictly
		//nolint:gosec // G115: safe conversion, stream count always positive and < 2^32
		if uint32(activeCount+1) > maxStreams {
			// Exceeds MAX_CONCURRENT_STREAMS
			// Refuse the stream and mark it so we won't write a response later
			_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeRefusedStream)
			// Ensure subsequent frames on this stream are rejected by marking it Closed now
			if s, ok := p.manager.GetStream(f.StreamID); ok {
				s.SetState(StateClosed)
			} else {
				st := p.manager.GetOrCreateStream(f.StreamID)
				st.SetState(StateClosed)
			}
			// Per RFC 7540 §5.1.2 a stream error is sufficient; do NOT send GOAWAY here.
			// Rely on RST_STREAM(REFUSED_STREAM) and marking the stream closed to prevent races.
			return fmt.Errorf("exceeds MAX_CONCURRENT_STREAMS: %d", maxStreams)
		}
	}

	// Atomically try to open the stream under concurrency limits
	stream, ok := p.manager.TryOpenStream(f.StreamID)
	if !ok {
		_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeRefusedStream)
		return fmt.Errorf("exceeds MAX_CONCURRENT_STREAMS")
	}

	// Validate stream state
	if err := validateStreamState(stream.GetState(), http2.FrameHeaders, f.StreamEnded()); err != nil {
		sendStreamError(p.writer, f.StreamID, http2.ErrCodeStreamClosed)
		return err
	}

	// State already set to Open by TryOpenStream

	// Set ResponseWriter immediately when stream opens so handlers can write responses
	if stream.ResponseWriter == nil {
		stream.ResponseWriter = p.connWriter
	}

	// Use the persistent per-connection HPACK decoder to maintain dynamic table state
	// The decoder is initialized once in NewProcessor and reused for all requests on this connection

	// Defer HPACK decode until END_HEADERS; accumulate header block fragments
	headerBlock := f.HeaderBlockFragment()

	// Handle priority information if present
	if dependency, weight, exclusive, hasPriority := ParsePriorityFromHeaders(f); hasPriority {
		// Check for self-dependency (PROTOCOL_ERROR per RFC 7540 Section 5.3.1)
		if dependency == f.StreamID {
			_ = p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeProtocol, []byte("WINDOW_UPDATE with 0 increment"))
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("stream %d depends on itself", f.StreamID)
		}
		p.manager.priorityTree.UpdateFromFrame(f.StreamID, dependency, weight, exclusive)
	}

	// Handle CONTINUATION frames if headers are not complete
	if !f.HeadersEnded() {
		// Copy fragment immediately; underlying framer buffers are reused on next ReadFrame
		pooled := headerBlockPool.Get().(*[]byte)
		frag := (*pooled)[:0]
		frag = append(frag, headerBlock...)
		// Set up continuation state to expect more header fragments
		p.continuationStateMu.Lock()
		p.continuationState = &ContinuationState{
			streamID:      f.StreamID,
			headerBlock:   frag,
			endStream:     f.StreamEnded(),
			expectingMore: true,
			// Initial HEADERS: trailers=false
			isTrailers: false,
		}
		p.continuationStateMu.Unlock()
		return nil // Wait for CONTINUATION frames
	}

	// END_HEADERS received on initial HEADERS: decode now (use pooled slice)
	pooledHeadersIn := headersSlicePoolIn.Get().(*[][2]string)
	headers := (*pooledHeadersIn)[:0]
	p.hpackDecoder.SetEmitFunc(func(hf hpack.HeaderField) {
		headers = append(headers, [2]string{hf.Name, hf.Value})
	})
	if _, err := p.hpackDecoder.Write(headerBlock); err != nil {
		_ = p.SendGoAway(0, http2.ErrCodeCompression, []byte("HPACK decoding failed"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("failed to decode headers: %w", err)
	}
	if err := p.hpackDecoder.Close(); err != nil {
		_ = p.SendGoAway(0, http2.ErrCodeCompression, []byte("HPACK decoding failed"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("failed to finalize headers: %w", err)
	}

	// Validate request headers
	if err := validateRequestHeaders(headers); err != nil {
		sendStreamError(p.writer, f.StreamID, http2.ErrCodeProtocol)
		return fmt.Errorf("invalid headers: %w", err)
	}
	for _, h := range headers {
		stream.AddHeader(h[0], h[1])
	}
	stream.ReceivedInitialHeaders = true

	if f.StreamEnded() {
		stream.EndStream = true
		stream.SetState(StateHalfClosedRemote)

		// Validate content-length (should be 0 if stream ends with headers)
		if err := validateContentLength(stream.Headers, stream.ReceivedDataLen); err != nil {
			sendStreamError(p.writer, f.StreamID, http2.ErrCodeProtocol)
			return fmt.Errorf("content-length mismatch: %w", err)
		}

		// Stream is complete, handle it
		if p.handler != nil {
			go func() {
				// Check if stream was cancelled before starting
				select {
				case <-stream.ctx.Done():
					return
				default:
				}

				if err := p.handler.HandleStream(stream.ctx, stream); err != nil {
					// Check context again before sending RST
					select {
					case <-stream.ctx.Done():
						return
					default:
						_ = p.writer.WriteRSTStream(stream.ID, http2.ErrCodeInternal)
					}
				}
			}()
		}
	}

	return nil
}

// handleData processes DATA frames
func (p *Processor) handleData(_ context.Context, f *http2.DataFrame) error {
	stream, ok := p.manager.GetStream(f.StreamID)
	if !ok {
		// Stream not found - this is an idle stream
		// Per RFC 7540 Section 5.1, DATA on idle stream is PROTOCOL_ERROR
		_ = p.SendGoAway(0, http2.ErrCodeProtocol, []byte("DATA on idle stream"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("DATA frame on idle stream %d", f.StreamID)
	}

	// Check if stream is closed
	state := stream.GetState()
	if state == StateClosed {
		_ = p.writer.WriteRSTStream(f.StreamID, http2.ErrCodeStreamClosed)
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("DATA frame on closed stream %d", f.StreamID)
	}

	// Validate stream state
	if err := validateStreamState(state, http2.FrameData, f.StreamEnded()); err != nil {
		// Send RST_STREAM and mark stream as closed to prevent handler from writing
		_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeStreamClosed)
		return err
	}

	// Track received data length
	dataLen := len(f.Data())
	stream.ReceivedDataLen += dataLen

	// Add data to stream buffer
	if err := stream.AddData(f.Data()); err != nil {
		return err
	}

	// Send window update (dataLen bounded by MAX_FRAME_SIZE <= 2^24-1)
	//nolint:gosec // G115: safe conversion, dataLen is frame payload size
	updateLen := uint32(dataLen)
	if err := p.writer.WriteWindowUpdate(f.StreamID, updateLen); err != nil {
		return err
	}
	if err := p.writer.WriteWindowUpdate(0, updateLen); err != nil {
		return err
	}

	if f.StreamEnded() {
		stream.EndStream = true
		stream.SetState(StateHalfClosedRemote)

		// Validate content-length if present
		if err := validateContentLength(stream.Headers, stream.ReceivedDataLen); err != nil {
			sendStreamError(p.writer, stream.ID, http2.ErrCodeProtocol)
			return fmt.Errorf("content-length mismatch: %w", err)
		}

		// Stream is complete, handle it
		if p.handler != nil {
			go func() {
				// Check if stream was cancelled before starting
				select {
				case <-stream.ctx.Done():
					return
				default:
				}

				if err := p.handler.HandleStream(stream.ctx, stream); err != nil {
					// Check context again before sending RST
					select {
					case <-stream.ctx.Done():
						return
					default:
						_ = p.writer.WriteRSTStream(stream.ID, http2.ErrCodeInternal)
					}
				}
			}()
		}
	}

	return nil
}

// handleWindowUpdate processes WINDOW_UPDATE frames
func (p *Processor) handleWindowUpdate(f *http2.WindowUpdateFrame) error {
	// Validate increment is not 0 (RFC 7540 Section 6.9)
	if f.Increment == 0 {
		if f.StreamID == 0 {
			// Connection-level error
			_ = p.SendGoAway(0, http2.ErrCodeProtocol, []byte("WINDOW_UPDATE increment is 0"))
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("WINDOW_UPDATE with 0 increment")
		}
		// Stream-level WINDOW_UPDATE with zero increment is a protocol error (RFC 7540 §6.9)
		// Send RST_STREAM to reset the stream
		p.connWriter.MarkStreamClosed(f.StreamID)
		if p.connWriter != nil {
			_ = p.connWriter.WriteRSTStreamPriority(f.StreamID, http2.ErrCodeProtocol)
		} else {
			_ = p.writer.WriteRSTStream(f.StreamID, http2.ErrCodeProtocol)
		}
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("WINDOW_UPDATE with 0 increment on stream %d", f.StreamID)
	}

	if f.StreamID == 0 {
		// Connection-level window update
		p.manager.mu.Lock()
		newWindow := int64(p.manager.connectionWindow) + int64(f.Increment)
		if newWindow > 0x7fffffff {
			// Flow control window overflow
			p.manager.mu.Unlock()
			_ = p.SendGoAway(0, http2.ErrCodeFlowControl, []byte("connection window overflow"))
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("connection window overflow: %d + %d > 2^31-1", p.manager.connectionWindow, f.Increment)
		}
		//nolint:gosec // G115: safe conversion, newWindow validated <= 2^31-1 above
		p.manager.connectionWindow = int32(newWindow)
		p.manager.mu.Unlock()
	} else {
		// Stream-level window update
		stream, ok := p.manager.GetStream(f.StreamID)
		if !ok {
			// WINDOW_UPDATE on idle stream is a protocol error
			if err := p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeProtocol,
				[]byte("WINDOW_UPDATE on idle stream")); err != nil {
				return err
			}
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("WINDOW_UPDATE on idle stream %d", f.StreamID)
		}

		stream.mu.Lock()
		newWindow := int64(stream.WindowSize) + int64(f.Increment)
		if newWindow > 0x7fffffff {
			// Flow control window overflow
			stream.mu.Unlock()
			_ = p.writer.WriteRSTStream(f.StreamID, http2.ErrCodeFlowControl)
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			return fmt.Errorf("stream %d window overflow: %d + %d > 2^31-1", f.StreamID, stream.WindowSize, f.Increment)
		}
		//nolint:gosec // G115: safe conversion, newWindow validated <= 2^31-1 above
		stream.WindowSize = int32(newWindow)
		stream.mu.Unlock()

		// Notify waiting writers and attempt to flush any buffered outbound data
		select {
		//nolint:gosec // G115: safe conversion, f.Increment validated not to cause overflow above
		case stream.ReceivedWindowUpd <- int32(f.Increment):
		default:
		}
		if stream.ResponseWriter != nil {
			// Try to send buffered data now that window increased
			p.flushBufferedData(f.StreamID)
		}
	}
	return nil
}

// flushBufferedData attempts to send any buffered outbound data for a stream
func (p *Processor) flushBufferedData(streamID uint32) {
	s, ok := p.manager.GetStream(streamID)
	if !ok || s.OutboundBuffer == nil {
		return
	}
	// Do not send DATA before HEADERS have been sent for this stream
	s.mu.RLock()
	headersSent := s.HeadersSent
	s.mu.RUnlock()
	if !headersSent {
		return
	}
	if s.OutboundBuffer.Len() == 0 {
		return
	}
	connWin, strWin, maxFrame := p.manager.GetSendWindowsAndMaxFrame(streamID)
	if connWin <= 0 || strWin <= 0 || maxFrame == 0 {
		return
	}

	// Compute chunk size
	allow := int(connWin)
	if int(strWin) < allow {
		allow = int(strWin)
	}
	if int(maxFrame) < allow {
		allow = int(maxFrame)
	}
	if allow <= 0 {
		return
	}

	if allow > s.OutboundBuffer.Len() {
		allow = s.OutboundBuffer.Len()
	}

	chunk := make([]byte, allow)
	if _, err := s.OutboundBuffer.Read(chunk); err != nil {
		return
	}
	endStream := s.OutboundBuffer.Len() == 0 && s.OutboundEndStream
	_ = p.writer.WriteData(streamID, endStream, chunk)
	if flusher, ok := p.writer.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	}
	//nolint:gosec // G115: safe conversion, chunk size bounded by flow control windows and MAX_FRAME_SIZE
	p.manager.ConsumeSendWindow(streamID, int32(len(chunk)))
}

// FlushBufferedData exposes flushBufferedData for external callers that need
// to attempt a send (e.g., immediately after HEADERS are flushed).
func (p *Processor) FlushBufferedData(streamID uint32) {
	p.flushBufferedData(streamID)
}

// handleRSTStream processes RST_STREAM frames
func (p *Processor) handleRSTStream(f *http2.RSTStreamFrame) error {
	stream, ok := p.manager.GetStream(f.StreamID)
	if !ok {
		// RST_STREAM on idle stream is a protocol error
		if err := p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeProtocol,
			[]byte("RST_STREAM on idle stream")); err != nil {
			return err
		}
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("RST_STREAM on idle stream %d", f.StreamID)
	}

	// Mark stream as closed by reset but keep it in the manager
	stream.SetState(StateClosed)
	stream.mu.Lock()
	stream.ClosedByReset = true
	stream.mu.Unlock()

	// Note: We keep the stream in the manager to track it as "closed"
	// Future frames on this stream will be rejected properly
	return nil
}

// GetManager returns the stream manager
func (p *Processor) GetManager() *Manager {
	return p.manager
}

// GetCurrentConn returns the current connection
func (p *Processor) GetCurrentConn() ResponseWriter {
	if p.currentConn != nil {
		return p.currentConn
	}
	return p.connWriter
}

// GetConnection returns the permanent connection writer
func (p *Processor) GetConnection() ResponseWriter {
	return p.connWriter
}

// IsExpectingContinuation reports whether the processor is in the middle of
// receiving a header block and is expecting more CONTINUATION frames.
func (p *Processor) IsExpectingContinuation() bool {
	p.continuationStateMu.Lock()
	defer p.continuationStateMu.Unlock()
	return p.continuationState != nil && p.continuationState.expectingMore
}

// GetExpectedContinuationStreamID returns the stream ID we're expecting CONTINUATION frames on, if any.
func (p *Processor) GetExpectedContinuationStreamID() (uint32, bool) {
	p.continuationStateMu.Lock()
	defer p.continuationStateMu.Unlock()
	if p.continuationState != nil && p.continuationState.expectingMore {
		return p.continuationState.streamID, true
	}
	return 0, false
}

// handleGoAway processes GOAWAY frames
func (p *Processor) handleGoAway(f *http2.GoAwayFrame) error {
	// Client sent GOAWAY, close all streams after the last stream ID
	lastStreamID := f.LastStreamID

	// Close streams with ID > lastStreamID
	p.manager.mu.Lock()
	defer p.manager.mu.Unlock()

	for streamID, stream := range p.manager.streams {
		if streamID > lastStreamID {
			stream.SetState(StateClosed)
			delete(p.manager.streams, streamID)
		}
	}

	return nil
}

// handlePing processes PING frames
func (p *Processor) handlePing(f *http2.PingFrame) error {
	// PING frames must have StreamID 0; any non-zero StreamID is a connection error (PROTOCOL_ERROR)
	if f.Header().StreamID != 0 {
		_ = p.SendGoAway(0, http2.ErrCodeProtocol, []byte("PING on non-zero stream"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("PING frame with non-zero stream id: %d", f.Header().StreamID)
	}

	if !f.IsAck() {
		// Send PING ACK
		if err := p.writer.WritePing(true, f.Data); err != nil {
			return err
		}
		// Flush to ensure PING response is sent immediately
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			return flusher.Flush()
		}
	}
	return nil
}

// SendGoAway sends a GOAWAY frame to gracefully close the connection
// Uses the connection-level SendGoAway if available (doesn't close connection immediately)
func (p *Processor) SendGoAway(lastStreamID uint32, code http2.ErrCode, debugData []byte) error {
	// If we have a connection writer with the new SendGoAway method, use it
	if p.connWriter != nil {
		return p.connWriter.SendGoAway(lastStreamID, code, debugData)
	}
	// Fallback to direct writer
	if err := p.writer.WriteGoAway(lastStreamID, code, debugData); err != nil {
		return err
	}
	if flusher, ok := p.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// sendRSTStreamAndMarkClosed sends RST_STREAM and marks the stream as closed
func (p *Processor) sendRSTStreamAndMarkClosed(streamID uint32, code http2.ErrCode) error {
	// Get the stream and cancel its context to stop any running handler
	stream, ok := p.manager.GetStream(streamID)

	if ok && stream.cancel != nil {
		stream.cancel() // Cancel handler immediately
	}

	// Mark stream as closed to prevent handlers from writing to it
	if p.connWriter != nil {
		p.connWriter.MarkStreamClosed(streamID)
	}

	// Always send RST_STREAM to reject frames on closed streams
	// (alreadyReset just means peer initiated the reset, but we still reject new frames)
	if true {
		// Send RST_STREAM with priority to avoid interleaving with HEADERS
		if p.connWriter != nil {
			if err := p.connWriter.WriteRSTStreamPriority(streamID, code); err != nil {
				return err
			}
		} else {
			// Fallback to writer
			if err := p.writer.WriteRSTStream(streamID, code); err != nil {
				return err
			}
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
		}
	}

	// Clear any buffered outbound data to avoid DATA being sent after reset
	if s, ok := p.manager.GetStream(streamID); ok {
		s.mu.Lock()
		if s.OutboundBuffer != nil {
			s.OutboundBuffer.Reset()
		}
		s.OutboundEndStream = false
		s.mu.Unlock()
	}

	return nil
}

// handlePriority processes PRIORITY frames
func (p *Processor) handlePriority(f *http2.PriorityFrame) error {
	// Check for self-dependency (PROTOCOL_ERROR per RFC 7540 Section 5.3.1)
	if f.StreamDep == f.StreamID {
		_ = p.SendGoAway(p.manager.GetLastStreamID(), http2.ErrCodeProtocol, []byte("WINDOW_UPDATE with 0 increment"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("stream %d depends on itself", f.StreamID)
	}

	p.manager.priorityTree.UpdateFromFrame(
		f.StreamID,
		f.StreamDep,
		f.Weight,
		f.Exclusive,
	)
	return nil
}

// GetStreamPriority returns the priority score for a stream
func (p *Processor) GetStreamPriority(streamID uint32) int {
	return p.manager.priorityTree.CalculateStreamPriority(streamID)
}

// PushPromise initiates a server push
func (p *Processor) PushPromise(streamID uint32, _ string, headers [][2]string) error {
	// Check if push is enabled
	if !p.manager.pushEnabled {
		return fmt.Errorf("server push is disabled")
	}

	p.manager.mu.Lock()
	// Get next push stream ID (server-initiated streams are even)
	pushStreamID := p.manager.nextPushID
	p.manager.nextPushID += 2
	p.manager.mu.Unlock()

	// Encode headers
	encoder := NewHeaderEncoder()
	headerBlock, err := encoder.Encode(headers)
	if err != nil {
		return fmt.Errorf("failed to encode push promise headers: %w", err)
	}

	// Write PUSH_PROMISE frame
	if err := p.writer.WritePushPromise(streamID, pushStreamID, true, headerBlock); err != nil {
		return fmt.Errorf("failed to write push promise: %w", err)
	}

	// Create the promised stream
	pushedStream := p.manager.CreateStream(pushStreamID)
	pushedStream.SetState(StateHalfClosedRemote) // Server push is half-closed from client

	for _, h := range headers {
		pushedStream.AddHeader(h[0], h[1])
	}

	return nil
}

// NewHeaderEncoder creates a new HPACK header encoder
func NewHeaderEncoder() *HeaderEncoder {
	buf := new(bytes.Buffer)
	return &HeaderEncoder{
		encoder: hpack.NewEncoder(buf),
		buf:     buf,
	}
}

// HeaderEncoder encodes headers using HPACK
type HeaderEncoder struct {
	encoder *hpack.Encoder
	buf     *bytes.Buffer
}

// Encode encodes headers to HPACK format
func (e *HeaderEncoder) Encode(headers [][2]string) ([]byte, error) {
	e.buf.Reset()
	for _, h := range headers {
		if err := e.encoder.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]}); err != nil {
			return nil, err
		}
	}
	result := make([]byte, e.buf.Len())
	copy(result, e.buf.Bytes())
	return result, nil
}

// handleContinuation processes CONTINUATION frames
func (p *Processor) handleContinuation(_ context.Context, f *http2.ContinuationFrame) error {
	p.continuationStateMu.Lock()
	defer p.continuationStateMu.Unlock()

	// Check if we're expecting a CONTINUATION frame
	if p.continuationState == nil || !p.continuationState.expectingMore {
		// Protocol error: unexpected CONTINUATION
		// Per RFC 7540 Section 6.10, this should be a connection error
		_ = p.SendGoAway(0, http2.ErrCodeProtocol, []byte("unexpected CONTINUATION"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("unexpected CONTINUATION frame on stream %d", f.StreamID)
	}

	// Check if this CONTINUATION is for the expected stream
	if p.continuationState.streamID != f.StreamID {
		// Protocol error: CONTINUATION on wrong stream
		// Per RFC 7540 Section 6.10, this should be a connection error
		_ = p.SendGoAway(0, http2.ErrCodeProtocol, []byte("CONTINUATION on wrong stream"))
		if flusher, ok := p.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		return fmt.Errorf("CONTINUATION frame on wrong stream: expected %d, got %d",
			p.continuationState.streamID, f.StreamID)
	}

	// Append header block fragment
	p.continuationState.headerBlock = append(
		p.continuationState.headerBlock,
		f.HeaderBlockFragment()...,
	)

	// Check if this is the last CONTINUATION frame
	if f.HeadersEnded() {
		// Complete header block received, process it
		stream := p.manager.GetOrCreateStream(f.StreamID)
		stream.SetState(StateOpen)

		// Decode accumulated headers or trailers (use pooled slice)
		pooledHeadersIn := headersSlicePoolIn.Get().(*[][2]string)
		headers := (*pooledHeadersIn)[:0]
		// Always return pooled slice
		defer func() {
			*pooledHeadersIn = headers[:0]
			headersSlicePoolIn.Put(pooledHeadersIn)
		}()
		p.hpackDecoder.SetEmitFunc(func(hf hpack.HeaderField) {
			headers = append(headers, [2]string{hf.Name, hf.Value})
		})

		if _, err := p.hpackDecoder.Write(p.continuationState.headerBlock); err != nil {
			// HPACK decoding error is a COMPRESSION_ERROR (connection error)
			_ = p.SendGoAway(0, http2.ErrCodeCompression, []byte("HPACK decoding failed"))
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			p.continuationState = nil
			return fmt.Errorf("failed to decode headers: %w", err)
		}
		if err := p.hpackDecoder.Close(); err != nil {
			_ = p.SendGoAway(0, http2.ErrCodeCompression, []byte("HPACK decoding failed"))
			if flusher, ok := p.writer.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
			p.continuationState = nil
			return fmt.Errorf("failed to finalize headers: %w", err)
		}

		if p.continuationState.isTrailers {
			// Validate trailers and append to stream
			if err := validateTrailerHeaders(headers); err != nil {
				p.continuationState = nil
				_ = p.sendRSTStreamAndMarkClosed(f.StreamID, http2.ErrCodeProtocol)
				return fmt.Errorf("invalid trailers: %w", err)
			}
			for _, h := range headers {
				stream.AddHeader(h[0], h[1])
			}
		} else {
			// Validate request headers and append
			if err := validateRequestHeaders(headers); err != nil {
				p.continuationState = nil
				// HPACK decode succeeded but headers invalid -> stream error
				sendStreamError(p.writer, f.StreamID, http2.ErrCodeProtocol)
				return fmt.Errorf("invalid headers: %w", err)
			}
			for _, h := range headers {
				stream.AddHeader(h[0], h[1])
			}
			// Mark that initial headers were received for trailers logic
			stream.ReceivedInitialHeaders = true
		}

		// Check if stream ended (from original HEADERS frame)
		if p.continuationState.endStream {
			stream.EndStream = true
			stream.SetState(StateHalfClosedRemote)

			// Stream is complete, handle it
			if p.handler != nil {
				go func() {
					// Check if stream was cancelled before starting
					select {
					case <-stream.ctx.Done():
						return
					default:
					}

					if err := p.handler.HandleStream(stream.ctx, stream); err != nil {
						// Check context again before sending RST
						select {
						case <-stream.ctx.Done():
							return
						default:
							_ = p.writer.WriteRSTStream(stream.ID, http2.ErrCodeInternal)
						}
					}
				}()
			}
		}

		// Clear continuation state and return buffers to pools
		if p.continuationState != nil {
			b := p.continuationState.headerBlock
			pooled := b[:0]
			headerBlockPool.Put(&pooled)
		}
		p.continuationState = nil
	}

	return nil
}

// Reader provides an io.Reader interface for stream data
type Reader struct {
	stream *Stream
	offset int
}

// NewReader creates a new stream reader
func NewReader(stream *Stream) *Reader {
	return &Reader{stream: stream}
}

// Read implements io.Reader
func (r *Reader) Read(p []byte) (n int, err error) {
	data := r.stream.GetData()
	if r.offset >= len(data) {
		if r.stream.EndStream {
			return 0, io.EOF
		}
		return 0, nil
	}

	n = copy(p, data[r.offset:])
	r.offset += n
	return n, nil
}
