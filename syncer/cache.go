package syncer

import (
	"container/heap"
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kexi/pypi-mirror/config"
	"github.com/kexi/pypi-mirror/db"
)

// CacheManager manages the local package cache.
type CacheManager struct {
	cfg        *config.Config
	db         *db.DB
	downloader *Downloader
}

// NewCacheManager creates a new CacheManager.
func NewCacheManager(cfg *config.Config, database *db.DB) *CacheManager {
	client := &http.Client{Timeout: 60 * time.Second}
	dl := NewDownloader(client, cfg.Sync.UserAgent, cfg.Sync.Retry)
	return &CacheManager{
		cfg:        cfg,
		db:         database,
		downloader: dl,
	}
}

// Run executes all cache management phases A-E.
func (cm *CacheManager) Run(ctx context.Context) error {
	// Phase A: Inventory local files
	log.Println("[cache] Phase A: inventorying local files")
	inventory, err := cm.inventoryLocal()
	if err != nil {
		return fmt.Errorf("phase A: %w", err)
	}
	log.Printf("[cache] Phase A: found %d local files", len(inventory))

	// Phase B: Resolve remote sizes for popular files
	log.Println("[cache] Phase B: resolving remote sizes")
	voteWindow, err := parseDuration(cm.cfg.Cache.VoteWindow)
	if err != nil {
		return fmt.Errorf("parse vote_window: %w", err)
	}
	since := time.Now().Add(-voteWindow)
	popular, err := cm.db.QueryPopular(since, cm.cfg.Cache.MinVoteCount)
	if err != nil {
		return fmt.Errorf("query popular: %w", err)
	}
	log.Printf("[cache] Phase B: %d popular files", len(popular))

	// Map for quick lookup of vote counts
	popularMap := make(map[string]int, len(popular))
	for _, pf := range popular {
		popularMap[pf.FilePath] = pf.VoteCount
	}

	// Resolve remote sizes for popular files not in local inventory
	type remoteFile struct {
		path string
		size int64
	}
	var missingFiles []remoteFile
	for _, pf := range popular {
		if _, local := inventory[pf.FilePath]; local {
			continue
		}
		size, err := cm.resolveRemoteSize(ctx, pf.FilePath)
		if err != nil {
			log.Printf("[cache] Phase B: error resolving %s: %v", pf.FilePath, err)
			continue
		}
		if size == nil {
			continue // 404
		}
		if *size > cm.cfg.Cache.FilesizeLimit.Bytes() {
			continue
		}
		missingFiles = append(missingFiles, remoteFile{path: pf.FilePath, size: *size})
	}

	// Phase C: Score all files
	log.Println("[cache] Phase C: scoring files")
	var downloadHeap maxScoreHeap
	var evictHeap minScoreHeap

	for path, size := range inventory {
		votes := popularMap[path] // 0 if not popular
		sc := scoreFile(votes, size)
		evictHeap = append(evictHeap, scoredFile{path: path, size: size, score: sc, local: true})
	}
	for _, mf := range missingFiles {
		votes := popularMap[mf.path]
		sc := scoreFile(votes, mf.size)
		downloadHeap = append(downloadHeap, scoredFile{path: mf.path, size: mf.size, score: sc, local: false})
	}

	heap.Init(&downloadHeap)
	heap.Init(&evictHeap)

	// Phase D: Download/evict
	log.Println("[cache] Phase D: downloading and evicting")
	var currentSize int64
	for _, size := range inventory {
		currentSize += size
	}
	sizeLimit := cm.cfg.Cache.SizeLimit.Bytes()
	downloadErrors := 0
	packagesURL := strings.TrimRight(cm.cfg.Upstream.PackagesURL, "/")

	for downloadHeap.Len() > 0 {
		if downloadErrors >= cm.cfg.Sync.DownloadErrorThreshold {
			log.Printf("[cache] Phase D: stopping, reached %d download errors", downloadErrors)
			break
		}

		candidate := heap.Pop(&downloadHeap).(scoredFile)

		if currentSize+candidate.size <= sizeLimit {
			// Fits without eviction
			if err := cm.downloadFile(ctx, packagesURL, candidate.path, candidate.size, inventory); err != nil {
				log.Printf("[cache] Phase D: download error %s: %v", candidate.path, err)
				downloadErrors++
				continue
			}
			currentSize += candidate.size
			continue
		}

		// Try evicting
		fitted := false
		for evictHeap.Len() > 0 {
			evictCandidate := evictHeap[0] // peek
			if evictCandidate.score >= candidate.score {
				break // no point evicting higher-scored files
			}
			if currentSize+candidate.size-evictCandidate.size <= sizeLimit {
				// Evict this file
				evicted := heap.Pop(&evictHeap).(scoredFile)
				evictPath := filepath.Join(cm.cfg.RepoPath, "packages", evicted.path)
				if err := os.Remove(evictPath); err != nil && !os.IsNotExist(err) {
					log.Printf("[cache] Phase D: evict error %s: %v", evicted.path, err)
				}
				cm.db.DeleteLocalSize(evicted.path)
				delete(inventory, evicted.path)
				currentSize -= evicted.size
				log.Printf("[cache] Phase D: evicted %s (score=%.4f)", evicted.path, evicted.score)

				// Now download
				if err := cm.downloadFile(ctx, packagesURL, candidate.path, candidate.size, inventory); err != nil {
					log.Printf("[cache] Phase D: download error %s: %v", candidate.path, err)
					downloadErrors++
				} else {
					currentSize += candidate.size
				}
				fitted = true
				break
			}
			// Can't fit even with this eviction, try next
			heap.Pop(&evictHeap)
		}
		if !fitted {
			break // no more downloads possible
		}
	}

	// Phase E: Cleanup
	log.Println("[cache] Phase E: cleanup")
	cm.db.DeleteOldVotes(time.Now().Add(-voteWindow))

	sizeDBTTL, err := parseDuration(cm.cfg.Cache.SizeDBTTL)
	if err != nil {
		return fmt.Errorf("parse size_db_ttl: %w", err)
	}
	cm.db.CleanRemoteSizes(sizeDBTTL)

	knownPaths := make(map[string]struct{}, len(inventory))
	for p := range inventory {
		knownPaths[p] = struct{}{}
	}
	cm.db.CleanLocalSizes(knownPaths)

	log.Println("[cache] done")
	return nil
}

func (cm *CacheManager) inventoryLocal() (map[string]int64, error) {
	inventory := make(map[string]int64)
	packagesDir := filepath.Join(cm.cfg.RepoPath, "packages")

	err := filepath.WalkDir(packagesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(packagesDir, path)
		if err != nil {
			return err
		}

		size, ok, dbErr := cm.db.GetLocalSize(relPath)
		if dbErr != nil {
			return dbErr
		}
		if ok {
			inventory[relPath] = size
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		size = info.Size()
		if err := cm.db.SetLocalSize(relPath, size); err != nil {
			return err
		}
		inventory[relPath] = size
		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		return inventory, nil // return what we have
	}
	return inventory, nil
}

func (cm *CacheManager) resolveRemoteSize(ctx context.Context, filePath string) (*int64, error) {
	size, ok, err := cm.db.GetRemoteSize(filePath)
	if err != nil {
		return nil, err
	}
	if ok {
		return size, nil
	}

	packagesURL := strings.TrimRight(cm.cfg.Upstream.PackagesURL, "/")
	url := packagesURL + "/" + filePath
	remoteSize, exists, err := cm.downloader.HeadFile(ctx, url)
	if err != nil {
		return nil, err
	}
	if !exists {
		cm.db.SetRemoteSize(filePath, nil)
		return nil, nil
	}
	cm.db.SetRemoteSize(filePath, &remoteSize)
	return &remoteSize, nil
}

func (cm *CacheManager) downloadFile(ctx context.Context, packagesURL, filePath string, size int64, inventory map[string]int64) error {
	url := packagesURL + "/" + filePath
	destPath := filepath.Join(cm.cfg.RepoPath, "packages", filePath)
	if err := cm.downloader.Download(ctx, url, destPath); err != nil {
		return err
	}
	inventory[filePath] = size
	cm.db.SetLocalSize(filePath, size)
	log.Printf("[cache] Phase D: downloaded %s (%d bytes)", filePath, size)
	return nil
}

func scoreFile(voteCount int, size int64) float64 {
	cap := int64(2 * 1024 * 1024 * 1024)
	s := size
	if s < cap {
		s = cap
	}
	return float64(voteCount) / float64(s+1) * 1048576.0
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// scoredFile holds a file with its score for heap operations.
type scoredFile struct {
	path  string
	size  int64
	score float64
	local bool
}

// maxScoreHeap is a max-heap of scoredFile (pop = highest score).
type maxScoreHeap []scoredFile

func (h maxScoreHeap) Len() int            { return len(h) }
func (h maxScoreHeap) Less(i, j int) bool   { return h[i].score > h[j].score }
func (h maxScoreHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *maxScoreHeap) Push(x interface{})  { *h = append(*h, x.(scoredFile)) }
func (h *maxScoreHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// minScoreHeap is a min-heap of scoredFile (pop = lowest score).
type minScoreHeap []scoredFile

func (h minScoreHeap) Len() int            { return len(h) }
func (h minScoreHeap) Less(i, j int) bool   { return h[i].score < h[j].score }
func (h minScoreHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *minScoreHeap) Push(x interface{})  { *h = append(*h, x.(scoredFile)) }
func (h *minScoreHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
