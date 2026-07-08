package tunnel

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CapturedRequest stores a captured HTTP request for replay.
type CapturedRequest struct {
	Index      int               // 1-based index in the buffer
	Timestamp  time.Time         // When the request was received
	Method     string            // HTTP method
	Path       string            // Request path
	Headers    map[string]string // Request headers
	Body       []byte            // Request body (may be truncated)
	Truncated  bool              // True if body was truncated to maxBodySize
	StatusCode int               // Response status code
	StatusText string            // Response status text
	RespHeaders map[string]string // Response headers
	Latency    time.Duration     // How long the request took
}

// ReplayBuffer is a bounded, thread-safe ring buffer that captures HTTP requests
// flowing through the tunnel for later replay via `hop replay`.
type ReplayBuffer struct {
	mu          sync.RWMutex
	entries     []*CapturedRequest
	maxEntries  int
	maxBodySize int64
	totalCount  int // Total requests captured (for indexing)
}

// NewReplayBuffer creates a new replay buffer with the given capacity and max body size.
func NewReplayBuffer(maxEntries int, maxBodySize int64) *ReplayBuffer {
	return &ReplayBuffer{
		entries:     make([]*CapturedRequest, 0, maxEntries),
		maxEntries:  maxEntries,
		maxBodySize: maxBodySize,
	}
}

// Add captures a request/response pair in the buffer.
func (rb *ReplayBuffer) Add(method, path string, headers map[string]string, body []byte,
	statusCode int, statusText string, respHeaders map[string]string, latency time.Duration) {

	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.totalCount++

	// Truncate body if needed
	truncated := false
	capturedBody := body
	if int64(len(body)) > rb.maxBodySize {
		capturedBody = body[:rb.maxBodySize]
		truncated = true
	}

	// Deep copy the body to avoid holding references to large buffers
	bodyCopy := make([]byte, len(capturedBody))
	copy(bodyCopy, capturedBody)

	// Deep copy headers
	headersCopy := make(map[string]string, len(headers))
	for k, v := range headers {
		headersCopy[k] = v
	}

	respHeadersCopy := make(map[string]string, len(respHeaders))
	for k, v := range respHeaders {
		respHeadersCopy[k] = v
	}

	entry := &CapturedRequest{
		Index:       rb.totalCount,
		Timestamp:   time.Now(),
		Method:      method,
		Path:        path,
		Headers:     headersCopy,
		Body:        bodyCopy,
		Truncated:   truncated,
		StatusCode:  statusCode,
		StatusText:  statusText,
		RespHeaders: respHeadersCopy,
		Latency:     latency,
	}

	// Ring buffer: if full, evict oldest
	if len(rb.entries) >= rb.maxEntries {
		rb.entries = rb.entries[1:]
	}
	rb.entries = append(rb.entries, entry)
}

// List returns all captured requests in chronological order (oldest first).
func (rb *ReplayBuffer) List() []*CapturedRequest {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := make([]*CapturedRequest, len(rb.entries))
	copy(result, rb.entries)
	return result
}

// Get returns the Nth most recent request (1 = most recent).
func (rb *ReplayBuffer) Get(nthMostRecent int) (*CapturedRequest, error) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if len(rb.entries) == 0 {
		return nil, fmt.Errorf("replay buffer is empty")
	}

	if nthMostRecent < 1 || nthMostRecent > len(rb.entries) {
		return nil, fmt.Errorf("request #%d not found (buffer has %d entries)", nthMostRecent, len(rb.entries))
	}

	// Most recent is at the end of the slice
	idx := len(rb.entries) - nthMostRecent
	return rb.entries[idx], nil
}

// Count returns the number of entries currently in the buffer.
func (rb *ReplayBuffer) Count() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return len(rb.entries)
}

// TotalCount returns the total number of requests ever captured.
func (rb *ReplayBuffer) TotalCount() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.totalCount
}

// MaxEntries returns the buffer capacity.
func (rb *ReplayBuffer) MaxEntries() int {
	return rb.maxEntries
}

// Replay sends the Nth most recent captured request to the given target port
// on localhost. Returns the response status code, status text, and latency.
func (rb *ReplayBuffer) Replay(nthMostRecent int, targetPort int) (statusCode int, statusText string, latency time.Duration, err error) {
	entry, err := rb.Get(nthMostRecent)
	if err != nil {
		return 0, "", 0, err
	}

	// Build the local HTTP request
	targetURL := fmt.Sprintf("http://localhost:%d%s", targetPort, entry.Path)

	var bodyReader io.Reader
	if len(entry.Body) > 0 {
		bodyReader = bytes.NewReader(entry.Body)
	}

	req, err := http.NewRequest(entry.Method, targetURL, bodyReader)
	if err != nil {
		return 0, "", 0, fmt.Errorf("building replay request: %w", err)
	}

	// Copy original headers
	for k, v := range entry.Headers {
		lower := strings.ToLower(k)
		if lower == "host" || lower == "connection" {
			continue
		}
		req.Header.Set(k, v)
	}

	// Mark as replay
	req.Header.Set("X-Hop-Replay", "true")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency = time.Since(start)
	if err != nil {
		return 0, "", latency, fmt.Errorf("replay request failed: %w", err)
	}
	defer resp.Body.Close()
	// Drain the body to properly close the connection
	io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, http.StatusText(resp.StatusCode), latency, nil
}
