# The Road to 100% HTTP/2 Compliance

**Celeris: Road to v0.1.0**

*August 22, 2025*

---

## Introduction

In the previous article, we built the foundation of Celeris—a working HTTP/2 server with gnet-powered networking. But "working" isn't the same as "correct." This article chronicles the journey from 45 passing h2spec tests to 147/147 (100% compliance).

I'll be honest: this was humbling. Every time I thought I understood the HTTP/2 spec, h2spec found another edge case I'd missed. The spec is detailed for good reason—there are a lot of subtle interactions that only matter under specific conditions.

### What is h2spec?

[h2spec](https://github.com/summerwind/h2spec) is the official HTTP/2 specification conformance testing tool:
- **147 tests** covering RFC 7540 and extensions
- **Strict mode** catches every edge case
- Tests frame parsing, state machines, error handling, flow control
- Used by major HTTP/2 implementations (nginx, Apache, Cloudflare)

Running h2spec against a server reveals how well it actually implements the spec—not just the happy path.

---

## The Compliance Journey

### Starting Point: 45/147 Tests (30.6%)

```bash
$ h2spec --strict -h 127.0.0.1 -p 8080
Finished in 1.2345 seconds
147 tests, 45 passed, 0 skipped, 102 failed
```

Major failure categories:
- ❌ Frame size validation
- ❌ Stream state transitions  
- ❌ Flow control windows
- ❌ HPACK compression errors
- ❌ CONTINUATION frame handling
- ❌ GOAWAY timing

### The Methodology

After each fix:
1. `make lint` - Ensure code quality
2. `make h2spec` - Check compliance
3. `make test-race` - Verify no race conditions
4. `make test-unit` - Unit tests pass

This prevented regressions while iteratively improving compliance.

---

## Challenge 1: Flow Control Enforcement

### The Problem

HTTP/2 uses flow control to prevent fast senders from overwhelming slow receivers. Each connection and stream has a "window" of bytes that can be sent before waiting for a WINDOW_UPDATE.

h2spec test: *"Sends SETTINGS to set initial window to 1 byte, then sends HEADERS"*
- **Expected**: Server sends DATA frame with exactly 1 byte
- **Our response**: 13 bytes (entire body)
- **Result**: FAILED

### Root Cause

We weren't respecting flow control windows:

```go
// BEFORE - Ignores flow control
func (c *Connection) WriteResponse(...) {
    c.writer.WriteData(streamID, true, body)  // Sends all at once
}
```

### The Solution

Strict window enforcement with chunked sending:

```go
// AFTER - Respects flow control
func (c *Connection) WriteResponse(...) {
    if len(body) > 0 {
        connWin, streamWin, maxFrame := c.processor.GetSendWindows(streamID)
        
        // Respect the SMALLEST window
        allow := min(int(connWin), int(streamWin), int(maxFrame), len(body))
        
        if allow > 0 {
            chunk := body[:allow]
            c.writer.WriteData(streamID, len(body) == allow, chunk)
            c.processor.ConsumeSendWindow(streamID, int32(allow))
        }
        
        // Buffer remainder for later
        if allow < len(body) {
            stream.OutboundBuffer.Write(body[allow:])
            stream.OutboundEndStream = true
        }
    }
}

// When WINDOW_UPDATE arrives, flush buffered data
func (p *Processor) handleWindowUpdate(f *http2.WindowUpdateFrame) error {
    // Update window
    p.manager.UpdateWindow(f.StreamID, f.Increment)
    
    // Try sending buffered data
    p.flushBufferedData(f.StreamID)
    return nil
}
```

**Result**: Server correctly sends 1-byte DATA frames when window = 1 ✅

---

## Challenge 2: CONTINUATION Frame Handling

### The Problem

Large header blocks that exceed `MAX_FRAME_SIZE` must be fragmented across multiple frames:
- First fragment: HEADERS frame (without END_HEADERS flag)
- Subsequent fragments: CONTINUATION frames
- Last fragment: Has END_HEADERS flag

h2spec tests 3.10.1-2 would timeout waiting for response after CONTINUATION.

### Root Cause Discovery

When HEADERS lacked END_HEADERS flag, we:
1. Buffered the header fragment ✓
2. Set continuation state ✓
3. **Returned early without creating stream** ✗

```go
// BEFORE - Premature return
if !f.HeadersEnded() {
    p.continuationState = &ContinuationState{...}
    return nil  // ← Bug: No stream created, handler never starts
}
```

### The Solution

Create stream immediately, complete headers when CONTINUATION arrives:

```go
// AFTER - Stream created immediately
if !f.HeadersEnded() {
    // Buffer fragment
    p.continuationState = &ContinuationState{
        streamID:    streamID,
        headerBlock: headerFragment,
    }
    
    // Create stream anyway (headers will be completed later)
    stream := p.manager.CreateStream(streamID)
    stream.SetState(StateOpen)
    
    return nil  // Handler waits for complete headers
}
```

When CONTINUATION with END_HEADERS arrives:
```go
func (p *Processor) handleContinuation(f *http2.ContinuationFrame) error {
    // Append fragment
    p.continuationState.headerBlock = append(
        p.continuationState.headerBlock, 
        f.HeaderBlockFragment()...,
    )
    
    if f.HeadersEnded() {
        // Complete headers are now available
        stream := p.manager.GetStream(f.StreamID)
        headers := p.decodeHeaders(p.continuationState.headerBlock)
        stream.AddHeaders(headers)
        
        // Start handler
        go p.handler.HandleStream(stream.ctx, stream)
        
        p.continuationState = nil
    }
    
    return nil
}
```

**Result**: Both CONTINUATION tests pass ✅

---

## Challenge 3: Mid-Header Block Violations

### The Problem

RFC 7540 §4.3: While a header block is open (HEADERS sent without END_HEADERS), no other frames for different streams may be sent. Violation requires GOAWAY with PROTOCOL_ERROR.

h2spec tests 4.3.3, 5.5.2, 6.2.2 expected GOAWAY when HEADERS arrived on a different stream while a header block was open. We sent RST_STREAM instead.

### Root Cause

Multiple checks executed in wrong order:

```go
// Frame processing order
1. Check MAX_CONCURRENT_STREAMS  → Sends RST_STREAM (wrong!)
2. Check mid-header block        → Would send GOAWAY (never reached)
```

### The Solution

Check mid-header block violations FIRST:

```go
func (c *Connection) HandleData(ctx context.Context, data []byte) error {
    // ... parse frame ...
    
    switch frame := f.(type) {
    case *http2.HeadersFrame:
        // Check mid-header violation BEFORE anything else
        if c.processor.IsExpectingContinuation() {
            expID, _ := c.processor.GetExpectedContinuationStreamID()
            if frame.StreamID != expID {
                // GOAWAY before any other checks
                c.processor.SendGoAway(
                    c.processor.LastStreamID(),
                    http2.ErrCodeProtocol,
                    []byte("HEADERS during header block"),
                )
                c.writer.Flush()
                c.conn.Close()
                return nil
            }
        }
        
        // Now check concurrency limits
        if !c.processor.CanOpenStream() {
            c.writer.WriteRSTStream(frame.StreamID, http2.ErrCodeRefusedStream)
            return nil
        }
        
        // ... process HEADERS normally ...
    }
}
```

**Result**: All mid-header violation tests pass ✅

---

## Challenge 4: Stream State Edge Cases

### The Problem

Control frames (WINDOW_UPDATE, PRIORITY, RST_STREAM) can arrive on streams in various states, including half-closed and closed. Not all combinations are errors.

h2spec tests Generic 2.2-2.3 sent WINDOW_UPDATE/PRIORITY on half-closed streams and expected DATA frames, but only saw HEADERS.

### Root Cause

These tests set `SETTINGS(INITIAL_WINDOW_SIZE=0)` before creating streams:

```
[DEBUG] Created stream 1 with WindowSize=0
[DEBUG] Stream 1 body write: len=5 connWin=65535 streamWin=0
```

With window=0, our synchronous write loop couldn't send any data. Response was buffered. When WINDOW_UPDATE arrived on "closed" stream, we ignored it.

### The Solution

Accept WINDOW_UPDATE on closed streams gracefully:

```go
func (p *Processor) handleWindowUpdate(f *http2.WindowUpdateFrame) error {
    stream, ok := p.manager.GetStream(f.StreamID)
    
    if !ok {
        // Check if this was a previously valid stream
        if f.StreamID <= p.manager.GetLastClientStreamID() {
            // Acceptable per RFC - just ignore gracefully
            return nil
        }
        // Unknown stream - error
        return p.sendConnectionError(http2.ErrCodeProtocol)
    }
    
    // Handle closed state
    if stream.GetState() == StateClosed {
        // Accept but don't update window
        return nil
    }
    
    // Update window normally
    stream.WindowSize += int32(f.Increment)
    p.flushBufferedData(f.StreamID)
    
    return nil
}
```

**Result**: Tests 2.2-2.3 pass ✅

---

## Challenge 5: The END_STREAM Mystery

### The Problem

Flow-control tests (6.5.3.1, 6.9.1.1, 6.9.2.1-2) reported "Unable to get server data length"—h2spec couldn't measure response DATA.

### Investigation

Added debug logging:
```
[H2][frame] WriteData sid=1 end=false len=1
[H2][frame] WriteData sid=1 end=false len=1
[H2][frame] WriteData sid=1 end=false len=5
```

**Every DATA frame had `end=false`!** Responses never closed.

### Root Cause

A streaming flag intended for SSE was set on ALL responses:

```go
// BEFORE - The bug
if !endStream && len(body) > 0 {
    st.IsStreaming = true  // ← Set on every response with body!
}

// Later during flush
endStream := s.OutboundBuffer.Len() == 0 && 
             s.OutboundEndStream && 
             !isStreaming  // ← Always false!
```

### The Solution

Remove the incorrect flag assignment:

```go
// AFTER - Fixed
st.HeadersSent = true
st.SetPhase(stream.PhaseHeadersSent)
// Don't set IsStreaming - reserve for actual streaming APIs
```

**Result**: Fixed 8 tests in one change! ✅

---

## Challenge 6: Frame Ordering Under Load

### The Problem

At 200+ concurrent clients, protocol errors appeared:

```
protocol error: received DATA before a HEADERS frame on stream 428541
```

HTTP/2 requires HEADERS before DATA on each stream. Under load, this was violated.

### Root Cause: AsyncWrite Race

```
Time    Thread 1 (stream A)              Thread 2 (stream B)
----    ---------------------            -------------------
T0      WriteHeaders(A)
T1      Flush() → AsyncWrite #1 (returns immediately)
T2                                       WriteHeaders(B)
T3      WriteData(A)
T4                                       Flush() → AsyncWrite #2
T5      Flush() → AsyncWrite #3
T6                                       WriteData(B)

If AsyncWrite #3 completes before #1, DATA(A) arrives before HEADERS(A)!
```

### Evolution of Solutions

**Attempt 1**: Flush immediately after HEADERS
- Helped but didn't eliminate race

**Attempt 2**: Per-stream phase tracking
- Better but occasional failures

**Attempt 3**: Batch HEADERS + first DATA before flush
- Significant improvement

**Final Solution**: Serialize all async writes with queue

```go
type connWriter struct {
    conn     gnet.Conn
    mu       *sync.Mutex
    pending  [][]byte  // Current batch
    inflight bool      // Is AsyncWritev in flight?
    queued   [][]byte  // Next batch (after inflight completes)
}

func (w *connWriter) Flush() error {
    w.mu.Lock()
    if w.inflight {
        // Queue instead of sending now
        w.queued = append(w.queued, w.pending...)
        w.pending = nil
        w.mu.Unlock()
        return nil
    }
    
    batch := w.pending
    w.pending = nil
    w.inflight = true
    w.mu.Unlock()
    
    return w.conn.AsyncWritev(batch, func(_ gnet.Conn, _ error) error {
        w.mu.Lock()
        if len(w.queued) > 0 {
            next := w.queued
            w.queued = nil
            w.mu.Unlock()
            return w.conn.AsyncWritev(next, ...)  // Chain next batch
        }
        w.inflight = false
        w.mu.Unlock()
        return nil
    })
}
```

**Key Properties**:
- FIFO ordering guaranteed
- Single in-flight write at a time
- Non-blocking (queue instead of wait)
- Chained callbacks for continuous throughput

**Result**: Zero protocol errors under extreme load ✅

---

## Challenge 7: HPACK Error Classification

### The Problem

HPACK errors must be distinguished:
- **Compression errors**: Send GOAWAY (connection-level)
- **Semantic errors**: Send RST_STREAM (stream-level)

Some h2spec tests expected GOAWAY, we sent RST_STREAM.

### The Solution

Classify errors at decode time:

```go
func (p *Processor) handleHeaders(f *http2.HeadersFrame) error {
    headers, err := p.decodeHeaders(f.HeaderBlockFragment())
    
    if err != nil {
        // Compression error = connection error
        if isCompressionError(err) {
            return p.sendConnectionError(http2.ErrCodeCompression)
        }
        
        // Semantic error = stream error
        p.writer.WriteRSTStream(f.StreamID, http2.ErrCodeProtocol)
        return nil
    }
    
    // ... continue ...
}

func isCompressionError(err error) bool {
    return strings.Contains(err.Error(), "HPACK") ||
           strings.Contains(err.Error(), "dynamic table")
}
```

---

## Final Result: 100% Compliance

```bash
$ h2spec --strict -h 127.0.0.1 -p 18081

Finished in 6.0625 seconds
147 tests, 147 passed, 0 skipped, 0 failed
```

### All Categories Passing

| Category | Tests |
|----------|-------|
| Connection Preface | ✅ All |
| SETTINGS Frames | ✅ All |
| HEADERS Frames | ✅ All |
| DATA Frames | ✅ All |
| PRIORITY Frames | ✅ All |
| RST_STREAM | ✅ All |
| PING | ✅ All |
| GOAWAY | ✅ All |
| WINDOW_UPDATE | ✅ All |
| Flow Control | ✅ All (including 1-byte windows!) |
| Stream States | ✅ All |
| CONTINUATION | ✅ All |
| HPACK | ✅ All |

---

## Key Lessons

1. **Edge cases matter**: h2spec tests window=1, window=0, mid-header interruptions—real clients never trigger these, but correct handling builds confidence

2. **Order of checks matters**: Protocol violation checks must come before other logic

3. **Async requires explicit ordering**: gnet's AsyncWrite completes out of order; must serialize with queues

4. **State machines are unforgiving**: Every transition must be validated

5. **HPACK is connection-scoped**: Decoder/encoder must persist across requests

---

## What's Next

In the next article, we add HTTP/1.1 support alongside HTTP/2—protocol multiplexing on a single port with automatic detection.

---

*100% h2spec compliance is a milestone, not a finish line. There's still plenty to improve.*
