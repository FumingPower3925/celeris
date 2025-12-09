package h1

import (
	"log"
	"strconv"
	"sync"

	"github.com/albertbausili/celeris/internal/date"
	"github.com/panjf2000/gnet/v2"
)

// Pre-allocated common headers to avoid allocations
var (
	// Pre-built status lines for common HTTP status codes (avoid strconv)
	statusLine200 = []byte("HTTP/1.1 200 OK\r\n")
	statusLine201 = []byte("HTTP/1.1 201 Created\r\n")
	statusLine204 = []byte("HTTP/1.1 204 No Content\r\n")
	statusLine301 = []byte("HTTP/1.1 301 Moved Permanently\r\n")
	statusLine302 = []byte("HTTP/1.1 302 Found\r\n")
	statusLine304 = []byte("HTTP/1.1 304 Not Modified\r\n")
	statusLine400 = []byte("HTTP/1.1 400 Bad Request\r\n")
	statusLine401 = []byte("HTTP/1.1 401 Unauthorized\r\n")
	statusLine403 = []byte("HTTP/1.1 403 Forbidden\r\n")
	statusLine404 = []byte("HTTP/1.1 404 Not Found\r\n")
	statusLine500 = []byte("HTTP/1.1 500 Internal Server Error\r\n")
	statusLine502 = []byte("HTTP/1.1 502 Bad Gateway\r\n")
	statusLine503 = []byte("HTTP/1.1 503 Service Unavailable\r\n")

	headerDate          = []byte("date: ")
	headerContentLength = []byte("content-length: ")
	headerConnection    = []byte("connection: ")
	headerClose         = []byte("close\r\n")
	headerSep           = []byte(": ")
	crlf                = []byte("\r\n")
	chunkEnd            = []byte("0\r\n\r\n")

	// Buffer pool for response assembly
	responseBufferPool = sync.Pool{
		New: func() any {
			b := make([]byte, 0, 32768)
			return &b
		},
	}
)

// ResponseWriter handles HTTP/1.1 response writing with efficient batching.
type ResponseWriter struct {
	conn           gnet.Conn
	logger         *log.Logger
	pending        [][]byte
	inflight       bool
	queued         [][]byte
	queuedReleases []*[]byte
	headersSent    bool
	chunkedMode    bool
	keepAlive      bool
	contentLength  int64
	bytesWritten   int64
	coalesceBuf    []byte
	coalescePooled *[]byte
	coalesceThresh int
	releases       []*[]byte
}

// WriteRaw writes a prebuilt HTTP/1.1 response buffer directly.
func (w *ResponseWriter) WriteRaw(resp []byte) error {
	w.pending = append(w.pending, resp)
	return w.flush()
}

// NewResponseWriter creates a new HTTP/1.1 response writer.
func NewResponseWriter(conn gnet.Conn, logger *log.Logger, keepAlive bool) *ResponseWriter {
	// Allocate coalescing buffer from pool to avoid per-connection allocations
	pooled := responseBufferPool.Get().(*[]byte)
	buf := (*pooled)[:0]
	// Ensure a minimum capacity for coalescing
	if cap(buf) < 65536 {
		// Return small pooled buffer and allocate a larger one temporarily for coalescing
		responseBufferPool.Put(pooled)
		pooled = new([]byte)
		b := make([]byte, 0, 65536)
		*pooled = b
		buf = (*pooled)[:0]
	}

	return &ResponseWriter{
		conn:           conn,
		logger:         logger,
		keepAlive:      keepAlive,
		coalesceThresh: 65536,
		coalesceBuf:    buf,
		coalescePooled: pooled,
	}
}

// WriteResponse writes HTTP/1.1 response with status, headers and body.
// Optimized path: assembles entire response in a single buffer and sends with one AsyncWritev.
func (w *ResponseWriter) WriteResponse(status int, headers [][2]string, body []byte, endResponse bool) error {
	if !w.headersSent {
		// FAST PATH: Assemble status + headers into a single buffer, then iov with body (zero-copy)
		pooledPtr := responseBufferPool.Get().(*[]byte)
		buf := (*pooledPtr)[:0]

		// Estimate final size to minimize reallocations
		// Status line: ~ 17-40 bytes, CRLF: 2, connection header ~ (len + value)
		const smallBodyMax = 16384
		wantCoalesceBody := len(body) > 0 && len(body) <= smallBodyMax
		// Base overhead: Status(avg 25) + Date(37) + Connection(clos~20) + CRLF(2)
		expected := 25 + 37 + 22 + 2
		// Headers length + CRLF per header
		for _, h := range headers {
			expected += len(h[0]) + 2 + len(h[1]) + 2
		}
		// Content-Length header if we add it (~20)
		if len(body) > 0 {
			expected += 20
		}
		// If we plan to coalesce small body, reserve capacity for it
		if wantCoalesceBody {
			expected += len(body)
		}
		// Ensure capacity
		if cap(buf) < expected {
			// return pooled buffer unused and allocate exact-fit temporary
			responseBufferPool.Put(pooledPtr)
			pooledPtr = nil
			buf = make([]byte, 0, expected)
		}

		// Status line (fast-path for common status codes using pre-built bytes)
		buf = appendStatusLine(buf, status)

		// Add Date header (zero-copy from atomic cache)
		buf = append(buf, headerDate...)
		buf = append(buf, date.Current()...)
		buf = append(buf, crlf...)

		// Determine content-length upfront
		hasContentLength := false
		for _, h := range headers {
			if h[0] == "content-length" {
				hasContentLength = true
				break
			}
		}

		// If no content-length and we have a body, add it
		if !hasContentLength && len(body) > 0 {
			buf = append(buf, headerContentLength...)
			buf = strconv.AppendInt(buf, int64(len(body)), 10)
			buf = append(buf, crlf...)
		}

		// Write all headers (zero-alloc for common cases)
		for _, h := range headers {
			buf = append(buf, h[0]...)
			buf = append(buf, headerSep...)
			buf = append(buf, h[1]...)
			buf = append(buf, crlf...)
		}

		// Connection header only when closing; HTTP/1.1 default is keep-alive
		if !w.keepAlive {
			buf = append(buf, headerConnection...)
			buf = append(buf, headerClose...)
		}

		// End of headers
		buf = append(buf, crlf...)

		// Prefer zero-copy body as separate iovec, but coalesce small bodies into header buffer when it fits
		if len(body) > 0 {
			// If small body and capacity allows, append to header buffer to reduce iovec count
			if wantCoalesceBody && cap(buf)-len(buf) >= len(body) {
				buf = append(buf, body...)
				w.pending = append(w.pending, buf)
			} else {
				w.pending = append(w.pending, buf)
				w.pending = append(w.pending, body)
			}
		} else {
			w.pending = append(w.pending, buf)
		}
		// Track pooled header buffer for release after write completes
		if pooledPtr != nil {
			// Update pooled slice to reference built buffer so capacity can be reused later
			*pooledPtr = buf
			w.releases = append(w.releases, pooledPtr)
		}

		w.headersSent = true
		return w.flush()
	}

	// Streaming path (headers already sent)
	if len(body) > 0 {
		w.writeBody(body)
	}

	if endResponse && w.chunkedMode {
		w.pending = append(w.pending, chunkEnd)
	}

	return w.flush()
}

// writeBody writes response body, using chunked encoding if enabled.
func (w *ResponseWriter) writeBody(body []byte) {
	if w.chunkedMode {
		// Write chunk size in hex without allocations
		var tmp [32]byte
		b := strconv.AppendInt(tmp[:0], int64(len(body)), 16)
		b = append(b, '\r', '\n')
		// Coalesce chunk header + body + CRLF into coalesce buffer
		w.coalesceBuf = append(w.coalesceBuf, b...)
		w.coalesceBuf = append(w.coalesceBuf, body...)
		w.coalesceBuf = append(w.coalesceBuf, crlf...)
	} else {
		// Coalesce raw body
		w.coalesceBuf = append(w.coalesceBuf, body...)
	}

	w.bytesWritten += int64(len(body))

	// If coalesce buffer exceeds threshold, move it to pending and reset
	if len(w.coalesceBuf) >= w.coalesceThresh {
		// Hand off current coalesced buffer without copy
		handoff := w.coalesceBuf
		w.pending = append(w.pending, handoff)
		// Track buffer for release after write completes
		if w.coalescePooled != nil {
			// Update pooled slice to reference current handoff
			*w.coalescePooled = handoff
			w.releases = append(w.releases, w.coalescePooled)
		}
		// Acquire a fresh coalescing buffer
		pooled := responseBufferPool.Get().(*[]byte)
		w.coalescePooled = pooled
		w.coalesceBuf = (*pooled)[:0]
	}
}

// flush sends all pending data using AsyncWritev.
func (w *ResponseWriter) flush() error {
	if w.inflight {
		// Queue additional data to be sent after current inflight completes
		if len(w.pending) > 0 {
			w.queued = append(w.queued, w.pending...)
			w.pending = nil
			if len(w.releases) > 0 {
				w.queuedReleases = append(w.queuedReleases, w.releases...)
				w.releases = nil
			}
		}
		return nil
	}

	// If there is coalesced data not yet moved to pending, move it now
	if len(w.coalesceBuf) > 0 {
		// Hand off remaining coalesced data without copying
		handoff := w.coalesceBuf
		w.pending = append(w.pending, handoff)
		if w.coalescePooled != nil {
			*w.coalescePooled = handoff
			w.releases = append(w.releases, w.coalescePooled)
		}
		// Allocate a fresh buffer for future coalescing
		pooled := responseBufferPool.Get().(*[]byte)
		w.coalescePooled = pooled
		w.coalesceBuf = (*pooled)[:0]
	}

	batch := w.pending
	rel := w.releases
	w.pending = nil
	w.releases = nil

	if len(batch) == 0 {
		return nil
	}

	w.inflight = true

	// Vectorized async write to minimize syscalls
	relThis := rel
	totalBytes := 0
	for _, b := range batch {
		totalBytes += len(b)
	}
	if w.logger != nil {
		w.logger.Printf("[ResponseWriter] Queuing AsyncWritev: %d bytes in %d buffers", totalBytes, len(batch))
	}
	return w.conn.AsyncWritev(batch, func(_ gnet.Conn, err error) error {
		if w.logger != nil {
			if err != nil {
				w.logger.Printf("[ResponseWriter] AsyncWritev completed with error: %v", err)
			} else {
				w.logger.Printf("[ResponseWriter] AsyncWritev completed successfully: %d bytes", totalBytes)
			}
		}

		// Release pooled buffers used in this batch
		for _, p := range relThis {
			if p != nil {
				*p = (*p)[:0]
				responseBufferPool.Put(p)
			}
		}

		// On completion, check if there is queued data, and send it next
		next := w.queued
		nextRel := w.queuedReleases
		if len(next) > 0 {
			w.queued = nil
			w.queuedReleases = nil
			w.inflight = true
			relNext := nextRel
			return w.conn.AsyncWritev(next, func(_ gnet.Conn, err error) error {
				if err != nil && w.logger != nil {
					w.logger.Printf("AsyncWritev callback error: %v", err)
				}
				// Release pooled buffers for queued batch
				for _, p := range relNext {
					if p != nil {
						*p = (*p)[:0]
						responseBufferPool.Put(p)
					}
				}
				w.inflight = false
				return nil
			})
		}
		w.inflight = false
		return nil
	})
}

// Reset resets the writer state for connection reuse.
func (w *ResponseWriter) Reset(keepAlive bool) {
	w.headersSent = false
	w.chunkedMode = false
	w.keepAlive = keepAlive
	w.contentLength = 0
	w.bytesWritten = 0
	w.pending = nil
	// Don't reset queued or inflight as they may still be in progress
}

// ShouldKeepAlive returns whether the connection should be kept alive.
func (w *ResponseWriter) ShouldKeepAlive() bool {
	return w.keepAlive
}

// statusText returns the status text for common HTTP status codes.
func statusText(code int) string {
	switch code {
	case 100:
		return "Continue"
	case 101:
		return "Switching Protocols"
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 202:
		return "Accepted"
	case 204:
		return "No Content"
	case 206:
		return "Partial Content"
	case 301:
		return "Moved Permanently"
	case 302:
		return "Found"
	case 304:
		return "Not Modified"
	case 307:
		return "Temporary Redirect"
	case 308:
		return "Permanent Redirect"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 408:
		return "Request Timeout"
	case 409:
		return "Conflict"
	case 410:
		return "Gone"
	case 413:
		return "Payload Too Large"
	case 414:
		return "URI Too Long"
	case 415:
		return "Unsupported Media Type"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 501:
		return "Not Implemented"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	case 504:
		return "Gateway Timeout"
	default:
		return "Unknown"
	}
}

// appendStatusLine appends the HTTP status line to the buffer.
func appendStatusLine(buf []byte, status int) []byte {
	switch status {
	case 200:
		return append(buf, statusLine200...)
	case 201:
		return append(buf, statusLine201...)
	case 204:
		return append(buf, statusLine204...)
	case 301:
		return append(buf, statusLine301...)
	case 302:
		return append(buf, statusLine302...)
	case 304:
		return append(buf, statusLine304...)
	case 400:
		return append(buf, statusLine400...)
	case 401:
		return append(buf, statusLine401...)
	case 403:
		return append(buf, statusLine403...)
	case 404:
		return append(buf, statusLine404...)
	case 500:
		return append(buf, statusLine500...)
	case 502:
		return append(buf, statusLine502...)
	case 503:
		return append(buf, statusLine503...)
	default:
		buf = append(buf, "HTTP/1.1 "...)
		buf = strconv.AppendInt(buf, int64(status), 10)
		buf = append(buf, ' ')
		buf = append(buf, statusText(status)...)
		return append(buf, crlf...)
	}
}
