package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// FileInfo represents a single file entry in the PyPI JSON API.
type FileInfo struct {
	Filename       string
	URL            string
	Size           int64
	SHA256         string
	MD5            string
	RequiresPython string
	UploadTime     string
	Yanked         bool
	YankedReason   string
}

// FetchPackageJSON fetches the JSON metadata for a package from PyPI.
func FetchPackageJSON(ctx context.Context, client *http.Client, pypiURL, packageName string) ([]byte, error) {
	url := fmt.Sprintf("%s/pypi/%s/json", pypiURL, packageName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch package json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch package json: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return body, nil
}

// pypiJSONResponse represents the top-level PyPI JSON API response.
type pypiJSONResponse struct {
	URLs     []pypiFileEntry            `json:"urls"`
	Releases map[string][]pypiFileEntry `json:"releases"`
}

type pypiFileEntry struct {
	Filename       string            `json:"filename"`
	URL            string            `json:"url"`
	Size           int64             `json:"size"`
	Digests        map[string]string `json:"digests"`
	RequiresPython string            `json:"requires_python"`
	UploadTime     string            `json:"upload_time"`
	Yanked         bool              `json:"yanked"`
	YankedReason   string            `json:"yanked_reason"`
}

// ParsePackageFiles extracts file info from PyPI JSON API response.
func ParsePackageFiles(jsonData []byte) ([]FileInfo, error) {
	var resp pypiJSONResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	seen := make(map[string]struct{})
	var files []FileInfo

	addFile := func(entry pypiFileEntry) {
		if _, ok := seen[entry.Filename]; ok {
			return
		}
		seen[entry.Filename] = struct{}{}
		fi := FileInfo{
			Filename:       entry.Filename,
			URL:            entry.URL,
			Size:           entry.Size,
			RequiresPython: entry.RequiresPython,
			UploadTime:     entry.UploadTime,
			Yanked:         entry.Yanked,
			YankedReason:   entry.YankedReason,
		}
		if entry.Digests != nil {
			fi.SHA256 = entry.Digests["sha256"]
			fi.MD5 = entry.Digests["md5"]
		}
		files = append(files, fi)
	}

	// Add files from "urls" (latest version)
	for _, entry := range resp.URLs {
		addFile(entry)
	}

	// Add files from all releases
	for _, releaseFiles := range resp.Releases {
		for _, entry := range releaseFiles {
			addFile(entry)
		}
	}

	return files, nil
}
