---
weight: 30
title: "HTTP/2 Priority Handling"
---

# HTTP/2 Priority Handling

Celeris fully implements HTTP/2 stream priority handling as defined in RFC 7540 Section 5.3.

## Overview

HTTP/2 allows clients to assign priority to streams, which helps the server optimize resource delivery. Celeris maintains a priority tree and uses it to schedule stream processing.

## Priority Tree

The priority tree is maintained automatically by Celeris. Each stream can:
- **Depend on another stream**: Creating a parent-child relationship
- **Have a weight**: A value from 1 to 256 (default is 16)
- **Be exclusive**: When set, the stream becomes the sole child of its parent, and other children become its children

## How It Works

### Priority Information Sources

Priority information comes from two frame types:

1. **HEADERS frames with priority**: Contains stream dependency, weight, and exclusive flag
2. **PRIORITY frames**: Dedicated frames for updating stream priority

### Priority Calculation

Celeris calculates a priority score for each stream based on:
- **Weight**: Higher weight = higher priority
- **Tree depth**: Streams closer to the root have higher priority

```go
score = weight + (10 - depth) * 10
```

## Usage

Priority handling is automatic and transparent to your application. However, you can access priority information if needed:

```go
router.GET("/resource", func(ctx *celeris.Context) error {
    // Priority is handled automatically
    // The server schedules responses based on priority
    
    return ctx.JSON(200, data)
})
```

## Internal API

For advanced use cases, you can access the priority tree directly through the stream processor:

```go
// Get priority score for a stream
score := processor.GetStreamPriority(streamID)

// Priority tree operations (internal use)
priorityTree.SetPriority(streamID, priority)
priorityTree.GetWeight(streamID)
priorityTree.GetChildren(streamID)
```

## Priority Tree Management

### Dependency Updates

When a stream sets an exclusive dependency:
1. All current children of the parent become children of the new stream
2. The new stream becomes the sole child of the parent

### Stream Closure

When a stream closes:
1. It's removed from the priority tree
2. Its children are reassigned to its parent

## Best Practices

1. **Trust the Client**: Priority information comes from the client and represents their resource loading strategy
2. **Don't Override**: The server should respect client priorities rather than imposing its own
3. **Use for Scheduling**: Priority affects the order of processing when multiple streams are ready

## Compliance

Celeris implements HTTP/2 priority handling in full compliance with:
- RFC 7540 Section 5.3 (Stream Priority)
- RFC 7540 Section 6.3 (PRIORITY Frame)

## Performance Impact

Priority handling adds minimal overhead:
- Priority tree operations are O(log n)
- Memory usage is proportional to active streams
- No additional network overhead (uses standard HTTP/2 frames)

