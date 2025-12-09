---
title: "Streaming Examples"
weight: 3
---

# Streaming Examples

Examples of streaming data with HTTP/2.

## Server-Sent Events (SSE)

Send events to clients:

```go
package main

import (
    "fmt"
    "time"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func sseHandler(ctx *celeris.Context) error {
    ctx.SetHeader("Content-Type", "text/event-stream")
    ctx.SetHeader("Cache-Control", "no-cache")
    ctx.SetHeader("Connection", "keep-alive")
    
    // Send events
    for i := 0; i < 10; i++ {
        event := fmt.Sprintf("data: Event %d\n\n", i)
        ctx.WriteString(event)
        time.Sleep(1 * time.Second)
    }
    
    return nil
}

func main() {
    router := celeris.NewRouter()
    router.GET("/events", sseHandler)
    
    server := celeris.NewWithDefaults()
    server.ListenAndServe(router)
}
```

## Streaming JSON

Stream JSON array elements:

```go
package main

import (
    "encoding/json"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

type Record struct {
    ID   int    `json:"id"`
    Data string `json:"data"`
}

func streamJSONHandler(ctx *celeris.Context) error {
    ctx.SetHeader("Content-Type", "application/json")
    ctx.WriteString("[")
    
    // Stream records
    for i := 0; i < 100; i++ {
        record := Record{
            ID:   i,
            Data: fmt.Sprintf("Record %d", i),
        }
        
        data, _ := json.Marshal(record)
        ctx.Write(data)
        
        if i < 99 {
            ctx.WriteString(",")
        }
    }
    
    ctx.WriteString("]")
    return nil
}
```

## File Upload with Progress

Handle large file uploads:

```go
package main

import (
    "io"
    "os"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func uploadHandler(ctx *celeris.Context) error {
    body := ctx.Body()
    
    // Create file
    file, err := os.Create("/tmp/upload.dat")
    if err != nil {
        return ctx.JSON(500, map[string]string{
            "error": "Failed to create file",
        })
    }
    defer file.Close()
    
    // Copy data with progress tracking
    buf := make([]byte, 32*1024)
    var totalBytes int64
    
    for {
        n, err := body.Read(buf)
        if n > 0 {
            if _, writeErr := file.Write(buf[:n]); writeErr != nil {
                return ctx.JSON(500, map[string]string{
                    "error": "Write failed",
                })
            }
            totalBytes += int64(n)
        }
        
        if err == io.EOF {
            break
        }
        if err != nil {
            return ctx.JSON(500, map[string]string{
                "error": err.Error(),
            })
        }
    }
    
    return ctx.JSON(200, map[string]interface{}{
        "bytes_received": totalBytes,
        "message":        "Upload complete",
    })
}

func main() {
    router := celeris.NewRouter()
    router.POST("/upload", uploadHandler)
    
    server := celeris.NewWithDefaults()
    server.ListenAndServe(router)
}
```

## Chunked Response

Send large response in chunks:

```go
package main

import (
    "fmt"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func chunkedHandler(ctx *celeris.Context) error {
    ctx.SetHeader("Content-Type", "text/plain")
    
    // Send data in chunks
    for i := 0; i < 100; i++ {
        chunk := fmt.Sprintf("Chunk %d\n", i)
        if _, err := ctx.WriteString(chunk); err != nil {
            return err
        }
        
        // Simulate processing time
        time.Sleep(100 * time.Millisecond)
    }
    
    return nil
}
```

## WebSocket-like Communication

Bidirectional communication pattern:

```go
package main

import (
    "bufio"
    "fmt"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func bidirectionalHandler(ctx *celeris.Context) error {
    reader := bufio.NewReader(ctx.Body())
    
    // Read and echo lines
    for {
        line, err := reader.ReadString('\n')
        if err != nil {
            if err == io.EOF {
                break
            }
            return err
        }
        
        // Echo back
        response := fmt.Sprintf("Echo: %s", line)
        if _, err := ctx.WriteString(response); err != nil {
            return err
        }
    }
    
    return nil
}
```

## Real-time Log Streaming

Stream logs in real-time:

```go
package main

import (
    "bufio"
    "os"
    "time"
    "github.com/FumingPower3925/celeris/pkg/celeris"
)

func logsHandler(ctx *celeris.Context) error {
    ctx.SetHeader("Content-Type", "text/plain")
    ctx.SetHeader("X-Content-Type-Options", "nosniff")
    
    // Open log file
    file, err := os.Open("/var/log/app.log")
    if err != nil {
        return ctx.String(500, "Failed to open log file")
    }
    defer file.Close()
    
    // Seek to end
    file.Seek(0, io.SeekEnd)
    
    // Stream new lines
    scanner := bufio.NewScanner(file)
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    
    timeout := time.After(5 * time.Minute)
    
    for {
        select {
        case <-timeout:
            return nil
        case <-ticker.C:
            for scanner.Scan() {
                line := scanner.Text()
                ctx.WriteString(line + "\n")
            }
        case <-ctx.Context().Done():
            return nil
        }
    }
}

func main() {
    router := celeris.NewRouter()
    router.GET("/logs", logsHandler)
    
    server := celeris.NewWithDefaults()
    server.ListenAndServe(router)
}
```

