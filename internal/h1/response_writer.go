package h1

import (
    "fmt"
    "log"
    "strconv"
    "sync"

    "github.com/panjf2000/gnet/v2"
)

// ResponseWriter handles HTTP/1.1 response writing with efficient batching.
type ResponseWriter struct {
	conn          gnet.Conn
	mu            sync.Mutex
	logger        *log.Logger
	pending       [][]byte
	inflight      bool
	queued        [][]byte
	headersSent   bool
	chunkedMode   bool
	keepAlive     bool
	contentLength int64
	bytesWritten  int64
}

// NewResponseWriter creates a new HTTP/1.1 response writer.
func NewResponseWriter(conn gnet.Conn, logger *log.Logger, keepAlive bool) *ResponseWriter {
	return &ResponseWriter{
		conn:      conn,
		logger:    logger,
		keepAlive: keepAlive,
	}
}

// WriteResponse writes HTTP/1.1 response with status, headers and body.
// If this is the first call, it writes status line and headers.
// Subsequent calls write body chunks.
func (w *ResponseWriter) WriteResponse(status int, headers [][2]string, body []byte, endResponse bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.headersSent {
		// First write: send status line and headers
		w.writeStatusAndHeaders(status, headers, body)
		w.headersSent = true
	}

	// Write body
	if len(body) > 0 {
		w.writeBody(body)
	}

	// If this is the end and we're in chunked mode, write final chunk
	if endResponse && w.chunkedMode {
		w.pending = append(w.pending, []byte("0\r\n\r\n"))
	}

	return w.flush()
}

// writeStatusAndHeaders writes the HTTP status line and headers.
func (w *ResponseWriter) writeStatusAndHeaders(status int, headers [][2]string, body []byte) {
    // Build status line (fast-path for 200 OK)
    if status == 200 {
        w.pending = append(w.pending, []byte("HTTP/1.1 200 OK\r\n"))
    } else {
        statusLine := fmt.Sprintf("HTTP/1.1 %d %s\r\n", status, statusText(status))
        w.pending = append(w.pending, []byte(statusLine))
    }

	// Determine if we should use chunked encoding
	hasContentLength := false
	hasTransferEncoding := false

	for _, h := range headers {
		if h[0] == "content-length" {
			hasContentLength = true
			if cl, err := strconv.ParseInt(h[1], 10, 64); err == nil {
				w.contentLength = cl
			}
		}
		if h[0] == "transfer-encoding" && h[1] == "chunked" {
			hasTransferEncoding = true
			w.chunkedMode = true
		}
	}

    // Prefer Content-Length when body is known to avoid chunked overhead
    if !hasContentLength && !hasTransferEncoding && len(body) > 0 {
        w.contentLength = int64(len(body))
        headers = append(headers, [2]string{"content-length", strconv.FormatInt(w.contentLength, 10)})
    } else if !hasContentLength && !hasTransferEncoding && w.keepAlive {
        // Streaming or unknown length: use chunked if keeping connection alive
        w.chunkedMode = true
        headers = append(headers, [2]string{"transfer-encoding", "chunked"})
    }

	// Add Connection header
	if w.keepAlive {
		headers = append(headers, [2]string{"connection", "keep-alive"})
	} else {
		headers = append(headers, [2]string{"connection", "close"})
	}

	// Write headers
	for _, h := range headers {
		headerLine := fmt.Sprintf("%s: %s\r\n", h[0], h[1])
		w.pending = append(w.pending, []byte(headerLine))
	}

	// End of headers
	w.pending = append(w.pending, []byte("\r\n"))
}

// writeBody writes response body, using chunked encoding if enabled.
func (w *ResponseWriter) writeBody(body []byte) {
	if w.chunkedMode {
		// Write chunk size in hex
		chunkSize := fmt.Sprintf("%x\r\n", len(body))
		w.pending = append(w.pending, []byte(chunkSize))
		w.pending = append(w.pending, body)
		w.pending = append(w.pending, []byte("\r\n"))
	} else {
		w.pending = append(w.pending, body)
	}

	w.bytesWritten += int64(len(body))
}

// flush sends all pending data using AsyncWritev.
func (w *ResponseWriter) flush() error {
	if w.inflight {
		// Queue additional data to be sent after current inflight completes
		if len(w.pending) > 0 {
			w.queued = append(w.queued, w.pending...)
			w.pending = nil
		}
		return nil
	}

	batch := w.pending
	w.pending = nil

	if len(batch) == 0 {
		_ = w.conn.Wake(nil)
		return nil
	}

	w.inflight = true

	// Use vectorized async write to minimize syscalls
	return w.conn.AsyncWritev(batch, func(_ gnet.Conn, err error) error {
		if err != nil && w.logger != nil {
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
				if err != nil && w.logger != nil {
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

// Reset resets the writer state for connection reuse.
func (w *ResponseWriter) Reset(keepAlive bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

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
	w.mu.Lock()
	defer w.mu.Unlock()
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
