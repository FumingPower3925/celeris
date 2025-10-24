// Package frame provides HTTP/2 frame type definitions and constants.
package frame

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Type represents HTTP/2 frame types
type Type uint8

// HTTP/2 frame type constants
const (
	FrameData         Type = 0x0
	FrameHeaders      Type = 0x1
	FramePriority     Type = 0x2
	FrameRSTStream    Type = 0x3
	FrameSettings     Type = 0x4
	FrameWindowUpdate Type = 0x8
	FrameContinuation Type = 0x9
)

// Flags represents HTTP/2 frame flags
type Flags uint8

// HTTP/2 frame flag constants
const (
	FlagEndStream  Flags = 0x1
	FlagEndHeaders Flags = 0x4
	FlagPadded     Flags = 0x8
	FlagPriority   Flags = 0x20
)

// Frame represents a generic HTTP/2 frame
type Frame struct {
	Type     Type
	Flags    Flags
	StreamID uint32
	Payload  []byte
}

// Parser handles HTTP/2 frame parsing
type Parser struct {
	framer *http2.Framer
	buf    *bytes.Buffer
}

// NewParser creates a new frame parser
func NewParser() *Parser {
	buf := new(bytes.Buffer)
	return &Parser{
		framer: nil,
		buf:    buf,
	}
}

// InitReader binds the parser to a persistent reader. This allows the framer
// to preserve CONTINUATION expectations across frames and read progressively as
// more data arrives.
func (p *Parser) InitReader(r io.Reader) {
	p.framer = http2.NewFramer(p.buf, r)
	p.framer.SetMaxReadFrameSize(1 << 20)
}

// Parse reads and parses an HTTP/2 frame from the reader
func (p *Parser) Parse(r io.Reader) (*Frame, error) {
	p.buf.Reset()

	// Read frame header (9 bytes)
	header := make([]byte, 9)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	// Parse frame header
	length := uint32(header[0])<<16 | uint32(header[1])<<8 | uint32(header[2])
	frameType := Type(header[3])
	flags := Flags(header[4])
	streamID := binary.BigEndian.Uint32(header[5:9]) & 0x7fffffff

	// Read frame payload
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}

	return &Frame{
		Type:     frameType,
		Flags:    flags,
		StreamID: streamID,
		Payload:  payload,
	}, nil
}

// ParseWithFramer uses the http2.Framer to parse frames
func (p *Parser) ParseWithFramer(r io.Reader) (http2.Frame, error) {
	// Backward-compatible helper for one-off reads; prefer InitReader + ReadNextFrame.
	if p.framer == nil {
		p.InitReader(r)
	}
	return p.framer.ReadFrame()
}

// BindReader binds a persistent reader to the underlying http2.Framer.
// This preserves internal CONTINUATION state across frames.
func (p *Parser) BindReader(r io.Reader) { p.InitReader(r) }

// ReadNextFrame reads the next frame using the bound reader.
func (p *Parser) ReadNextFrame() (http2.Frame, error) {
	if p.framer == nil {
		return nil, fmt.Errorf("parser not initialized; call InitReader")
	}
	return p.framer.ReadFrame()
}

// Writer handles HTTP/2 frame writing
type Writer struct {
	framer *http2.Framer
	writer io.Writer
	mu     sync.Mutex
}

// NewWriter creates a new frame writer
func NewWriter(w io.Writer) *Writer {
	framer := http2.NewFramer(w, nil)
	return &Writer{
		framer: framer,
		writer: w,
	}
}

// Flush flushes any buffered data
func (w *Writer) Flush() error {
	// If the writer implements Flush, call it
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	// Otherwise, no-op
	return nil
}

// WriteFrame writes a generic frame
func (w *Writer) WriteFrame(f *Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Use WriteRawFrame from http2.Framer
	flags := http2.Flags(f.Flags)
	return w.framer.WriteRawFrame(http2.FrameType(f.Type), flags, f.StreamID, f.Payload)
}

// WriteSettings writes a SETTINGS frame
func (w *Writer) WriteSettings(settings ...http2.Setting) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.framer.WriteSettings(settings...)
}

// WriteSettingsAck writes a SETTINGS acknowledgment frame
func (w *Writer) WriteSettingsAck() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.framer.WriteSettingsAck()
}

// WriteHeaders writes HEADERS (and CONTINUATION) frames, fragmenting by maxFrameSize
func (w *Writer) WriteHeaders(streamID uint32, endStream bool, headerBlock []byte, maxFrameSize uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if maxFrameSize == 0 {
		maxFrameSize = 16384 // RFC 7540 default
	}

	// First fragment is a HEADERS frame
	remaining := headerBlock
	first := true
	for len(remaining) > 0 {
		chunkLen := int(maxFrameSize)
		if len(remaining) < chunkLen {
			chunkLen = len(remaining)
		}
		frag := remaining[:chunkLen]
		remaining = remaining[chunkLen:]

		if first {
			// HEADERS
			var flags http2.Flags
			if endStream {
				flags |= http2.FlagHeadersEndStream
			}
			if len(remaining) == 0 {
				flags |= http2.FlagHeadersEndHeaders
			}
			if err := w.framer.WriteRawFrame(http2.FrameHeaders, flags, streamID, frag); err != nil {
				return err
			}
			first = false
		} else {
			// CONTINUATION frames until header block is done
			var flags http2.Flags
			if len(remaining) == 0 {
				flags |= http2.FlagContinuationEndHeaders
			}
			if err := w.framer.WriteRawFrame(http2.FrameContinuation, flags, streamID, frag); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteData writes a DATA frame
func (w *Writer) WriteData(streamID uint32, endStream bool, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Avoid emitting zero-length DATA frames without END_STREAM, which can trip h2spec
	if len(data) == 0 && !endStream {
		return nil
	}
	return w.framer.WriteData(streamID, endStream, data)
}

// WriteWindowUpdate writes a WINDOW_UPDATE frame
func (w *Writer) WriteWindowUpdate(streamID uint32, increment uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.framer.WriteWindowUpdate(streamID, increment)
}

// WriteRSTStream writes a RST_STREAM frame
func (w *Writer) WriteRSTStream(streamID uint32, code http2.ErrCode) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.framer.WriteRSTStream(streamID, code)
}

// WriteGoAway writes a GOAWAY frame
func (w *Writer) WriteGoAway(lastStreamID uint32, code http2.ErrCode, debugData []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.framer.WriteGoAway(lastStreamID, code, debugData)
}

// WritePing writes a PING frame
func (w *Writer) WritePing(ack bool, data [8]byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.framer.WritePing(ack, data)
}

// WritePushPromise writes a PUSH_PROMISE frame
func (w *Writer) WritePushPromise(streamID uint32, promiseID uint32, endHeaders bool, headerBlock []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var flags http2.Flags
	if endHeaders {
		flags = http2.FlagPushPromiseEndHeaders
	}
	return w.framer.WriteRawFrame(http2.FramePushPromise, flags, streamID, append(
		[]byte{
			byte(promiseID >> 24),
			byte(promiseID >> 16),
			byte(promiseID >> 8),
			byte(promiseID),
		},
		headerBlock...,
	))
}

// HeaderEncoder encodes HTTP headers using HPACK
type HeaderEncoder struct {
	encoder *hpack.Encoder
	buf     *bytes.Buffer
}

// headerBufPool reuses temporary buffers used during HPACK encoding to reduce allocations.
var headerBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// NewHeaderEncoder creates a new header encoder
func NewHeaderEncoder() *HeaderEncoder {
	// Obtain a buffer from pool to minimize allocations; the encoder writes to this buffer.
	bufAny := headerBufPool.Get()
	var buf *bytes.Buffer
	if b, ok := bufAny.(*bytes.Buffer); ok {
		b.Reset()
		buf = b
	} else {
		buf = new(bytes.Buffer)
	}
	return &HeaderEncoder{
		encoder: hpack.NewEncoder(buf),
		buf:     buf,
	}
}

// Encode encodes headers to HPACK format
func (e *HeaderEncoder) Encode(headers [][2]string) ([]byte, error) {
	e.buf.Reset()
	for _, h := range headers {
		if err := e.encoder.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]}); err != nil {
			return nil, err
		}
	}
	// Return a copy to avoid the buffer being reused while data is still being written
	result := make([]byte, e.buf.Len())
	copy(result, e.buf.Bytes())
	return result, nil
}

// EncodeBorrow encodes headers and returns a byte slice backed by the encoder's
// internal buffer. The returned slice is only valid until the next call to
// Encode/EncodeBorrow or Close. Callers must ensure exclusive access.
func (e *HeaderEncoder) EncodeBorrow(headers [][2]string) ([]byte, error) {
	e.buf.Reset()
	for _, h := range headers {
		if err := e.encoder.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]}); err != nil {
			return nil, err
		}
	}
	return e.buf.Bytes(), nil
}

// Close releases internal resources back to the pool. The encoder instance should
// not be used after Close.
func (e *HeaderEncoder) Close() {
	if e.buf != nil {
		e.buf.Reset()
		headerBufPool.Put(e.buf)
		e.buf = nil
		e.encoder = hpack.NewEncoder(new(bytes.Buffer)) // detach writer defensively
	}
}

// HeaderDecoder decodes HTTP headers using HPACK
type HeaderDecoder struct {
	decoder *hpack.Decoder
}

// NewHeaderDecoder creates a new header decoder
func NewHeaderDecoder(maxSize uint32) *HeaderDecoder {
	return &HeaderDecoder{
		decoder: hpack.NewDecoder(maxSize, nil),
	}
}

// Decode decodes HPACK-encoded headers
func (d *HeaderDecoder) Decode(data []byte) ([][2]string, error) {
	headers := make([][2]string, 0)
	d.decoder.SetEmitFunc(func(hf hpack.HeaderField) {
		headers = append(headers, [2]string{hf.Name, hf.Value})
	})

	if _, err := d.decoder.Write(data); err != nil {
		return nil, fmt.Errorf("hpack decode error: %w", err)
	}

	return headers, nil
}
