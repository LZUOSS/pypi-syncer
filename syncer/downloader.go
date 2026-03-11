package syncer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Downloader handles HTTP file downloads with retry and mtime preservation.
type Downloader struct {
	client    *http.Client
	userAgent string
	retries   int
}

// NewDownloader creates a new Downloader.
func NewDownloader(client *http.Client, userAgent string, retries int) *Downloader {
	return &Downloader{
		client:    client,
		userAgent: userAgent,
		retries:   retries,
	}
}

// Download downloads url to destPath atomically (via temp file + rename).
// It preserves the Last-Modified header as the file mtime.
// Retries up to d.retries times on failure.
func (d *Downloader) Download(ctx context.Context, url, destPath string) error {
	var lastErr error
	tmpPath := destPath + ".tmp"

	for attempt := 0; attempt < d.retries; attempt++ {
		lastErr = d.downloadOnce(ctx, url, destPath, tmpPath)
		if lastErr == nil {
			return nil
		}
		os.Remove(tmpPath)
	}
	return fmt.Errorf("download %s failed after %d attempts: %w", url, d.retries, lastErr)
}

func (d *Downloader) downloadOnce(ctx context.Context, url, destPath, tmpPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", d.userAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, err := time.Parse(time.RFC1123, lm); err == nil {
			os.Chtimes(destPath, time.Now(), t)
		}
	}

	return nil
}

// HeadFile sends a HEAD request to url and returns the Content-Length.
// Returns exists=false if status 404.
func (d *Downloader) HeadFile(ctx context.Context, url string) (size int64, exists bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", d.userAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("HEAD %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("HEAD %s: status %d", url, resp.StatusCode)
	}

	cl := resp.Header.Get("Content-Length")
	if cl != "" {
		size, err = strconv.ParseInt(cl, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("parse content-length: %w", err)
		}
	}
	return size, true, nil
}
