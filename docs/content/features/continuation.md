---
weight: 32
title: "CONTINUATION Frame Handling"
---

# CONTINUATION Frame Handling

Celeris fully supports HTTP/2 CONTINUATION frames for handling large header blocks.

## Overview

In HTTP/2, header blocks are compressed using HPACK. If a header block is too large to fit in a single HEADERS frame, it's split across multiple frames. The initial fragment is sent in a HEADERS frame, and subsequent fragments are sent in CONTINUATION frames.

## How It Works

### Frame Sequence

A typical sequence looks like:

```
HEADERS (flags: none)
  [header block fragment 1]
CONTINUATION (flags: none)
  [header block fragment 2]
CONTINUATION (flags: END_HEADERS)
  [header block fragment 3]
```

### Automatic Handling

Celeris handles CONTINUATION frames automatically:

1. **HEADERS frame received** without END_HEADERS flag
   - Buffers the header block fragment
   - Sets continuation state to expect more fragments

2. **CONTINUATION frames received**
   - Appends fragments to the buffer
   - Validates stream ID matches

3. **Final CONTINUATION** with END_HEADERS flag
   - Decodes the complete header block using HPACK
   - Processes the request normally

## State Management

### Continuation State

Celeris tracks:
- `streamID`: Which stream is expecting CONTINUATION frames
- `headerBlock`: Accumulated header fragments
- `endStream`: Whether the stream ends after headers complete
- `expectingMore`: Whether more CONTINUATION frames are expected

### Protocol Compliance

Celeris enforces RFC 7540 Section 6.10 requirements:

✅ **Ordered Processing**: CONTINUATION frames must be contiguous
✅ **Stream Matching**: CONTINUATION must be for the expected stream  
✅ **Error Handling**: Invalid sequences trigger RST_STREAM with PROTOCOL_ERROR

## Error Conditions

### Unexpected CONTINUATION

If a CONTINUATION frame arrives when none is expected:

```
ERROR: Unexpected CONTINUATION frame on stream N
-> RST_STREAM with PROTOCOL_ERROR
```

### Wrong Stream ID

If a CONTINUATION frame arrives for the wrong stream:

```
ERROR: CONTINUATION on wrong stream: expected X, got Y
-> RST_STREAM with PROTOCOL_ERROR
```

### Interleaved Frames

If a frame for another stream arrives during CONTINUATION:

```
Stream 1: HEADERS (no END_HEADERS)
Stream 3: DATA          <- PROTOCOL ERROR!
Stream 1: CONTINUATION
```

This violates RFC 7540 and results in connection error.

## Buffer Management

### Memory Usage

Celeris buffers header fragments in memory:
- Initial allocation from HEADERS frame
- Grows with each CONTINUATION frame
- Released after complete headers are decoded

### Size Limits

Header size is limited by:
- Maximum frame size (default 16KB per frame)
- Total header list size (configurable)
- Available memory

## Implementation Details

### Internal Structure

```go
type ContinuationState struct {
    streamID      uint32
    headerBlock   []byte
    endStream     bool
    expectingMore bool
}
```

### Processing Flow

```go
// HEADERS frame without END_HEADERS
if !headersFrame.HeadersEnded() {
    processor.continuationState = &ContinuationState{
        streamID:      headersFrame.StreamID,
        headerBlock:   fragment,
        endStream:     headersFrame.StreamEnded(),
        expectingMore: true,
    }
    return // Wait for CONTINUATION
}

// CONTINUATION frame
if continuationFrame.HeadersEnded() {
    // Decode complete header block
    headers := decodeHPACK(state.headerBlock)
    // Process stream
    // Clear continuation state
}
```

## Performance

### Overhead

CONTINUATION frames add minimal overhead:
- Buffer allocation: proportional to header size
- Validation: constant time per frame
- Assembly: append operation

### Optimization

Celeris optimizes by:
- Preallocating buffer based on first fragment
- Using append for efficient growth
- Releasing state immediately after processing

## Testing

### Large Headers

Test with large headers that require CONTINUATION:

```go
// Generate large header block
headers := make(map[string]string)
for i := 0; i < 100; i++ {
    headers[fmt.Sprintf("x-custom-%d", i)] = strings.Repeat("value", 100)
}

// Make request - will use CONTINUATION frames
resp, err := client.Do(req)
```

### Edge Cases

Test coverage includes:
- Single CONTINUATION frame
- Multiple CONTINUATION frames
- Maximum header size
- Protocol errors (wrong stream ID, unexpected CONTINUATION)

## Debugging

### Logging

Enable frame-level logging to see CONTINUATION handling:

```go
log.Printf("HEADERS frame: stream=%d, fragment_len=%d, end_headers=%v",
    streamID, len(fragment), headersFrame.HeadersEnded())
    
log.Printf("CONTINUATION frame: stream=%d, fragment_len=%d, end_headers=%v",
    streamID, len(fragment), contFrame.HeadersEnded())
```

### Inspection

Use tools like Wireshark to inspect the frame sequence:
```
Frame 1: HEADERS (0x01)
Frame 2: CONTINUATION (0x09)
Frame 3: CONTINUATION (0x09, END_HEADERS)
```

## Compliance

Celeris implements CONTINUATION frames per:
- RFC 7540 Section 6.10 (CONTINUATION Frame)
- RFC 7540 Section 4.3 (Header Compression and Decompression)

### Required Behavior

✅ Processes CONTINUATION frames in sequence  
✅ Validates stream ID matches  
✅ Enforces frame ordering rules  
✅ Handles END_HEADERS flag correctly  
✅ Sends PROTOCOL_ERROR for violations

## Best Practices

### For Users

You don't need to do anything special - CONTINUATION handling is automatic and transparent.

### For Clients

- Keep headers small when possible
- Use HPACK compression effectively
- Respect SETTINGS_MAX_HEADER_LIST_SIZE

### For Developers

- Don't assume headers fit in one frame
- Always check END_HEADERS flag
- Buffer fragments until complete

## Limitations

### Current Implementation

✅ Fully implements RFC 7540 requirements  
✅ Handles arbitrary header sizes  
✅ Validates protocol compliance  

The current implementation has no known limitations.

## See Also

- [HPACK Compression](../architecture/hpack.md)
- [Frame Processing](../architecture/frames.md)
- [Error Handling](../features/error-handling.md)

