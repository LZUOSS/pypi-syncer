package syncer

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/kexi/pypi-mirror/config"
	"github.com/kexi/pypi-mirror/db"
)

// Syncer handles PyPI index synchronization.
type Syncer struct {
	cfg    *config.Config
	db     *db.DB
	client *http.Client
}

// NewSyncer creates a new Syncer.
func NewSyncer(cfg *config.Config, database *db.DB) *Syncer {
	return &Syncer{
		cfg: cfg,
		db:  database,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// SyncIndex synchronizes the local index with the upstream PyPI.
func (s *Syncer) SyncIndex(ctx context.Context) error {
	log.Println("Starting index sync...")
	start := time.Now()

	// 1. Fetch remote package list with serials
	remote, err := s.listPackagesWithSerial(ctx)
	if err != nil {
		return fmt.Errorf("list packages with serial: %w", err)
	}
	log.Printf("Remote has %d packages", len(remote))

	// 2. Get local serials
	local, err := s.db.GetAllSerials()
	if err != nil {
		return fmt.Errorf("get local serials: %w", err)
	}
	log.Printf("Local has %d packages", len(local))

	// 3. Categorize: new, updated, removed
	var toProcess []string
	for name, remoteSerial := range remote {
		localSerial, exists := local[name]
		if !exists || localSerial < remoteSerial {
			toProcess = append(toProcess, name)
		}
	}
	sort.Strings(toProcess)

	var removed []string
	for name := range local {
		if _, exists := remote[name]; !exists {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)

	log.Printf("To process: %d (new/updated), to remove: %d", len(toProcess), len(removed))

	// 4. Process new + updated with concurrency
	var (
		mu         sync.Mutex
		errCount   int
		processed  int
	)
	sem := make(chan struct{}, s.cfg.Sync.ConcurrentDownloads)
	var wg sync.WaitGroup

	for _, name := range toProcess {
		if ctx.Err() != nil {
			break
		}

		name := name
		serial := remote[name]
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if err := s.syncPackage(ctx, name, serial); err != nil {
				log.Printf("Error syncing %s: %v", name, err)
				mu.Lock()
				errCount++
				mu.Unlock()
				return
			}

			mu.Lock()
			processed++
			mu.Unlock()
		}()
	}
	wg.Wait()

	// 5. Process removed
	removedCount := 0
	for _, name := range removed {
		normalized := normalizeName(name)
		os.RemoveAll(filepath.Join(s.cfg.RepoPath, "simple", normalized))
		os.Remove(filepath.Join(s.cfg.RepoPath, "json", normalized))
		if err := s.db.DeleteSerial(name); err != nil {
			log.Printf("Error deleting serial for %s: %v", name, err)
		} else {
			removedCount++
		}
	}

	// 6. Collect all current package names
	var allNames []string
	for name := range remote {
		allNames = append(allNames, name)
	}

	// 7. Generate root simple pages
	if err := GenerateRootSimplePages(s.cfg.RepoPath, allNames, s.cfg.Prefix); err != nil {
		return fmt.Errorf("generate root simple pages: %w", err)
	}

	// 8. Log stats
	log.Printf("Sync complete in %s: processed=%d, removed=%d, errors=%d",
		time.Since(start).Round(time.Millisecond), processed, removedCount, errCount)

	return nil
}

// syncPackage fetches and writes index data for a single package.
func (s *Syncer) syncPackage(ctx context.Context, name string, serial int64) error {
	jsonData, err := FetchPackageJSON(ctx, s.client, s.cfg.Upstream.PypiURL, name)
	if err != nil {
		return fmt.Errorf("fetch json: %w", err)
	}

	files, err := ParsePackageFiles(jsonData)
	if err != nil {
		return fmt.Errorf("parse files: %w", err)
	}

	if err := GenerateSimplePages(s.cfg.RepoPath, name, files, s.cfg.Prefix); err != nil {
		return fmt.Errorf("generate simple pages: %w", err)
	}

	// Write JSON to {repoPath}/json/{normalizedName}
	normalized := normalizeName(name)
	jsonDir := filepath.Join(s.cfg.RepoPath, "json")
	if err := os.MkdirAll(jsonDir, 0o755); err != nil {
		return fmt.Errorf("mkdir json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(jsonDir, normalized), jsonData, 0o644); err != nil {
		return fmt.Errorf("write json: %w", err)
	}

	if err := s.db.SetSerial(name, serial); err != nil {
		return fmt.Errorf("set serial: %w", err)
	}

	return nil
}

// XML-RPC types for parsing list_packages_with_serial response

type methodResponse struct {
	Params xmlParams `xml:"params"`
}

type xmlParams struct {
	Param xmlParam `xml:"param"`
}

type xmlParam struct {
	Value xmlValue `xml:"value"`
}

type xmlValue struct {
	Struct xmlStruct `xml:"struct"`
}

type xmlStruct struct {
	Members []xmlMember `xml:"member"`
}

type xmlMember struct {
	Name  string      `xml:"name"`
	Value xmlMemberValue `xml:"value"`
}

type xmlMemberValue struct {
	Int string `xml:"int"`
	I4  string `xml:"i4"`
}

// listPackagesWithSerial calls the PyPI XML-RPC API to get all packages with serials.
func (s *Syncer) listPackagesWithSerial(ctx context.Context) (map[string]int64, error) {
	body := []byte(`<?xml version='1.0'?>
<methodCall>
  <methodName>list_packages_with_serial</methodName>
  <params></params>
</methodCall>`)

	url := s.cfg.Upstream.PypiURL + "/pypi"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xmlrpc call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xmlrpc call: status %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var methodResp methodResponse
	if err := xml.Unmarshal(respBody, &methodResp); err != nil {
		return nil, fmt.Errorf("parse xmlrpc response: %w", err)
	}

	result := make(map[string]int64)
	for _, member := range methodResp.Params.Param.Value.Struct.Members {
		serialStr := member.Value.Int
		if serialStr == "" {
			serialStr = member.Value.I4
		}
		var serial int64
		if _, err := fmt.Sscanf(serialStr, "%d", &serial); err != nil {
			continue
		}
		result[member.Name] = serial
	}

	return result, nil
}
