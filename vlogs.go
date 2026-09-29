package vlogs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single log entry for VictoriaLogs
type LogEntry struct {
	Log    map[string]string `json:"log"`
	Date   string            `json:"date"`
	Stream string            `json:"stream"`
}

// Client is a VictoriaLogs client with batch writing support
type Client struct {
	url           string
	batchSize     int
	flushInterval time.Duration
	httpClient    *http.Client

	mu          sync.Mutex
	batch       []LogEntry
	lastFlush   time.Time
	serviceName string
}

// Config holds configuration for VictoriaLogs client
type Config struct {
	URL           string        // VictoriaLogs insert URL
	BatchSize     int           // Number of logs to batch before flush
	FlushInterval time.Duration // Maximum time to wait before flush
	ServiceName   string        // Service name for stream tag
	HTTPTimeout   time.Duration // HTTP client timeout
}

// DefaultConfig returns a config with sensible defaults, reading from environment
func DefaultConfig() Config {
	return Config{
		URL:           os.Getenv("VLOGS_INSERT_URL"),
		BatchSize:     100,          // 100条日志批量
		FlushInterval: 30 * time.Second, // 30秒超时刷新
		ServiceName:   detectServiceName(),
		HTTPTimeout:   30 * time.Second,
	}
}

// detectServiceName tries to detect service name from:
// 1. SERVICE_NAME environment variable
// 2. Caller's module path (extract last meaningful part)
func detectServiceName() string {
	// First try environment variable
	if name := os.Getenv("SERVICE_NAME"); name != "" {
		return name
	}

	// Fallback: extract from caller's module path
	// Skip runtime callers and get the actual service package
	pcs := make([]uintptr, 10)
	n := runtime.Callers(2, pcs)
	if n == 0 {
		return "unknown-service"
	}

	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		// Skip runtime and vlogs-client packages
		if !strings.Contains(frame.Function, "runtime.") &&
			!strings.Contains(frame.Function, "vlogs-client.") {
			// Extract package name from full path
			// e.g., "bug-agent-server/main.main" -> "bug-agent-server"
			// e.g., "github.com/user/service/handler.Handle" -> "service"
			parts := strings.Split(frame.Function, "/")
			if len(parts) >= 2 {
				// Get the module part (second to last)
				modulePart := parts[len(parts)-2]
				// Remove GitHub user prefix if present
				if idx := strings.LastIndex(modulePart, "."); idx != -1 {
					modulePart = modulePart[idx+1:]
				}
				return modulePart
			}
		}
		if !more {
			break
		}
	}

	return "unknown-service"
}

// NewClient creates a new VictoriaLogs client
func NewClient(cfg Config) *Client {
	if cfg.URL == "" {
		log.Printf("[VLogs] VLOGS_INSERT_URL not set, logs will not be sent to VictoriaLogs")
		return nil
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "unknown-service"
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}

	return &Client{
		url:           cfg.URL,
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
		serviceName:   cfg.ServiceName,
		httpClient:    &http.Client{Timeout: cfg.HTTPTimeout},
		lastFlush:     time.Now(),
	}
}

// Log sends a log entry (adds to batch, may trigger flush)
func (c *Client) Log(level, message string, fields map[string]string) {
	if c == nil {
		return
	}

	entry := LogEntry{
		Log: map[string]string{
			"level":   level,
			"message": message,
		},
		Date:   time.Now().UTC().Format(time.RFC3339),
		Stream: c.serviceName,
	}

	// Add extra fields
	if len(fields) > 0 {
		for k, v := range fields {
			entry.Log[k] = v
		}
	}

	c.addEntry(entry)
}

// addEntry adds a log entry to the batch and flushes if needed
func (c *Client) addEntry(entry LogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.batch = append(c.batch, entry)

	// 只在达到批次大小时才flush，超时作为兜底机制
	if len(c.batch) >= c.batchSize {
		c.flushLocked()
		return
	}

	// 超时兜底：如果超过flushInterval且batch不为空
	now := time.Now()
	if now.Sub(c.lastFlush) >= c.flushInterval && len(c.batch) > 0 {
		c.flushLocked()
	}
}

// Flush forces flush of current batch
func (c *Client) Flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushLocked()
}

// flushLocked flushes the current batch (caller must hold lock)
func (c *Client) flushLocked() {
	if len(c.batch) == 0 {
		return
	}

	// Build JSON lines
	var buf bytes.Buffer
	for i, entry := range c.batch {
		if i > 0 {
			buf.WriteByte('\n')
		}
		jsonBytes, err := json.Marshal(entry)
		if err != nil {
			log.Printf("[VLogs] Failed to marshal log entry: %v", err)
			continue
		}
		buf.Write(jsonBytes)
	}

	batchSize := len(c.batch)
	// Reset batch before sending to avoid race
	c.batch = c.batch[:0]
	c.lastFlush = time.Now()

	// Send batch asynchronously
	go c.sendBatch(buf.Bytes(), batchSize)
}

// sendBatch sends the batch to VictoriaLogs
func (c *Client) sendBatch(data []byte, count int) {
	resp, err := c.httpClient.Post(c.url, "application/stream+json", bytes.NewReader(data))
	if err != nil {
		log.Printf("[VLogs] Failed to send batch (%d logs): %v", count, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[VLogs] Sent batch: %d logs, HTTP %d", count, resp.StatusCode)
	} else {
		log.Printf("[VLogs] Batch send failed: HTTP %d, %d logs", resp.StatusCode, count)
	}
}

// LogLLMRequest logs LLM request metrics
func (c *Client) LogLLMRequest(model, endpoint string, inputTokens int, requestPayload string, duration time.Duration) {
	if c == nil {
		return
	}

	fields := map[string]string{
		"type":         "llm_request",
		"model":        model,
		"endpoint":     endpoint,
		"input_tokens": fmt.Sprintf("%d", inputTokens),
		"duration":     fmt.Sprintf("%.2f", duration.Seconds()),
	}

	// Include full request body
	if requestPayload != "" {
		fields["request_body"] = requestPayload
	}

	c.Log("info", fmt.Sprintf("LLM request: %s, model: %s, tokens: %d, duration: %.2fs",
		endpoint, model, inputTokens, duration.Seconds()), fields)
}

// LogLLMResponse logs LLM response metrics
func (c *Client) LogLLMResponse(model, endpoint string, inputTokens, outputTokens int, responseContent string, duration time.Duration) {
	if c == nil {
		return
	}

	fields := map[string]string{
		"type":          "llm_response",
		"model":         model,
		"endpoint":      endpoint,
		"input_tokens":  fmt.Sprintf("%d", inputTokens),
		"output_tokens": fmt.Sprintf("%d", outputTokens),
		"total_tokens":  fmt.Sprintf("%d", inputTokens+outputTokens),
		"duration":      fmt.Sprintf("%.2f", duration.Seconds()),
	}

	// Include full response content
	if responseContent != "" {
		fields["response_body"] = responseContent
	}

	c.Log("info", fmt.Sprintf("LLM response: %s, model: %s, input: %d, output: %d, duration: %.2fs",
		endpoint, model, inputTokens, outputTokens, duration.Seconds()), fields)
}

// LogLLMError logs LLM error details
func (c *Client) LogLLMError(model, endpoint, errorMsg string, duration time.Duration) {
	if c == nil {
		return
	}

	fields := map[string]string{
		"type":        "llm_error",
		"model":       model,
		"endpoint":    endpoint,
		"error":       errorMsg,
		"duration":    fmt.Sprintf("%.2f", duration.Seconds()),
	}

	c.Log("error", fmt.Sprintf("LLM error: %s, model: %s, error: %s, duration: %.2fs",
		endpoint, model, errorMsg, duration.Seconds()), fields)
}

// LogLLMCallComplete logs complete LLM call with full request and response body
// This should be called after the LLM call completes (streaming or non-streaming)
func (c *Client) LogLLMCallComplete(model, endpoint string, requestBody string, responseBody string, inputTokens, outputTokens int, duration time.Duration, hasError bool, errorMsg string) {
	if c == nil {
		log.Printf("[VLogs] LogLLMCallComplete skipped: client is nil")
		return
	}

	log.Printf("[VLogs] LogLLMCallComplete called: model=%s, input=%d, output=%d, duration=%.2fs", model, inputTokens, outputTokens, duration.Seconds())

	fields := map[string]string{
		"type":          "llm_call",
		"model":         model,
		"endpoint":      endpoint,
		"input_tokens":  fmt.Sprintf("%d", inputTokens),
		"output_tokens": fmt.Sprintf("%d", outputTokens),
		"total_tokens":  fmt.Sprintf("%d", inputTokens+outputTokens),
		"duration":      fmt.Sprintf("%.2f", duration.Seconds()),
	}

	// Include full request body
	if requestBody != "" {
		fields["request_body"] = requestBody
	}

	// Include full response body
	if responseBody != "" {
		fields["response_body"] = responseBody
	}

	// Include error if present
	if hasError && errorMsg != "" {
		fields["error"] = errorMsg
	}

	logLevel := "info"
	if hasError {
		logLevel = "error"
	}

	c.Log(logLevel, fmt.Sprintf("LLM call complete: %s, model: %s, input: %d, output: %d, duration: %.2fs",
		endpoint, model, inputTokens, outputTokens, duration.Seconds()), fields)
}

// LogToolExecution logs tool execution metrics
func (c *Client) LogToolExecution(toolName, input, output string, duration time.Duration, success bool) {
	if c == nil {
		return
	}

	level := "info"
	if !success {
		level = "error"
	}

	fields := map[string]string{
		"type":        "tool_execution",
		"tool_name":   toolName,
		"duration":    fmt.Sprintf("%.2f", duration.Seconds()),
		"success":     fmt.Sprintf("%t", success),
	}

	// Only include input/output if not too large
	if len(input) < 5000 {
		fields["input"] = input
	}
	if len(output) < 5000 {
		fields["output"] = output
	}

	c.Log(level, fmt.Sprintf("Tool %s executed in %.2fs, success: %t",
		toolName, duration.Seconds(), success), fields)
}

// Global client instance for easy access
var globalClient *Client

// Init initializes the global VictoriaLogs client
func Init(cfg Config) *Client {
	globalClient = NewClient(cfg)
	return globalClient
}

// GetClient returns the global client
func GetClient() *Client {
	return globalClient
}

// Log is a convenience function using the global client
func Log(level, message string, fields map[string]string) {
	if globalClient != nil {
		globalClient.Log(level, message, fields)
	}
}

// Flush flushes the global client
func Flush() {
	if globalClient != nil {
		globalClient.Flush()
	}
}
