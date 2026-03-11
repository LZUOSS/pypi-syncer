package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// LogEntry represents a single HTTP access log entry.
type LogEntry struct {
	Method    string
	Path      string
	Status    int
	BytesSent int64
	Duration  time.Duration
	ClientIP  string
	UserAgent string
	Referer   string
	Proxied   bool
}

// AccessLogger writes access logs in mirror-json or combined format.
type AccessLogger struct {
	mu      sync.Mutex
	file    *os.File
	buf     *bufio.Writer
	format  string
	path    string
	done    chan struct{}
}

// NewAccessLogger creates a new AccessLogger that writes to the given path.
func NewAccessLogger(path, format string) (*AccessLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	l := &AccessLogger{
		file:   f,
		buf:    bufio.NewWriter(f),
		format: format,
		path:   path,
		done:   make(chan struct{}),
	}
	go l.autoFlush()
	return l, nil
}

func (l *AccessLogger) autoFlush() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			_ = l.buf.Flush()
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

type jsonLogEntry struct {
	Time       string `json:"time"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"duration_ms"`
	ClientIP   string `json:"client_ip"`
	UserAgent  string `json:"user_agent"`
	Referer    string `json:"referer"`
	Proxied    string `json:"proxied"`
}

// Write writes a log entry.
func (l *AccessLogger) Write(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch l.format {
	case "mirror-json":
		l.writeJSON(entry)
	default:
		l.writeCombined(entry)
	}
}

func (l *AccessLogger) writeJSON(entry LogEntry) {
	proxied := "0"
	if entry.Proxied {
		proxied = "1"
	}
	je := jsonLogEntry{
		Time:       time.Now().UTC().Format(time.RFC3339),
		Method:     entry.Method,
		Path:       entry.Path,
		Status:     entry.Status,
		Bytes:      entry.BytesSent,
		DurationMS: entry.Duration.Milliseconds(),
		ClientIP:   entry.ClientIP,
		UserAgent:  entry.UserAgent,
		Referer:    entry.Referer,
		Proxied:    proxied,
	}
	data, _ := json.Marshal(je)
	l.buf.Write(data)
	l.buf.WriteByte('\n')
}

func (l *AccessLogger) writeCombined(entry LogEntry) {
	t := time.Now().Format("02/Jan/2006:15:04:05 -0700")
	fmt.Fprintf(l.buf, "%s - - [%s] \"%s %s HTTP/1.1\" %d %d \"%s\" \"%s\"\n",
		entry.ClientIP, t, entry.Method, entry.Path,
		entry.Status, entry.BytesSent, entry.Referer, entry.UserAgent)
}

// Flush flushes buffered data to the underlying file.
func (l *AccessLogger) Flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Flush()
}

// Close stops the auto-flush goroutine, flushes, and closes the log file.
func (l *AccessLogger) Close() error {
	close(l.done)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.buf.Flush(); err != nil {
		l.file.Close()
		return err
	}
	return l.file.Close()
}

// Reopen closes and reopens the log file (for log rotation).
func (l *AccessLogger) Reopen() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.buf.Flush(); err != nil {
		return fmt.Errorf("flush before reopen: %w", err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close before reopen: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("reopen log file: %w", err)
	}
	l.file = f
	l.buf = bufio.NewWriter(f)
	return nil
}
