package syncer

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	tiers      []config.CacheTier
}

// NewCacheManager creates a new CacheManager.
func NewCacheManager(cfg *config.Config, database *db.DB) (*CacheManager, error) {
	transport, err := config.NewTransport(cfg.Upstream.Proxy)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}
	client := &http.Client{Timeout: 60 * time.Second, Transport: transport}
	dl := NewDownloader(client, cfg.Sync.UserAgent, cfg.Sync.Retry)
	return &CacheManager{
		cfg:        cfg,
		db:         database,
		downloader: dl,
		tiers:      cfg.EffectiveTiers(),
	}, nil
}

// localFile holds information about a locally cached file.
type localFile struct {
	size int64
	tier int
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

	type candidateFile struct {
		path  string
		size  int64
		score float64
		local bool
		tier  int // current tier (for local files)
	}

	var all []candidateFile
	for path, lf := range inventory {
		votes := popularMap[path]
		sc := scoreFile(votes, lf.size)
		all = append(all, candidateFile{path: path, size: lf.size, score: sc, local: true, tier: lf.tier})
	}
	for _, mf := range missingFiles {
		votes := popularMap[mf.path]
		sc := scoreFile(votes, mf.size)
		all = append(all, candidateFile{path: mf.path, size: mf.size, score: sc, local: false, tier: -1})
	}

	// Phase D: Two-pass assignment + execution
	log.Println("[cache] Phase D: assigning files to tiers and executing")

	// Sort descending by score
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })

	// Assignment pass: assign each file to the hottest tier with remaining capacity
	remaining := make([]int64, len(cm.tiers))
	for i, t := range cm.tiers {
		remaining[i] = t.SizeLimit.Bytes()
	}

	type assignment struct {
		candidateFile
		assignedTier int // -1 = evict/skip
	}

	assignments := make([]assignment, len(all))
	for i, c := range all {
		assigned := -1
		for t := 0; t < len(cm.tiers); t++ {
			if remaining[t] >= c.size {
				remaining[t] -= c.size
				assigned = t
				break
			}
		}
		assignments[i] = assignment{candidateFile: c, assignedTier: assigned}
	}

	packagesURL := strings.TrimRight(cm.cfg.Upstream.PackagesURL, "/")
	downloadErrors := 0

	// Execution pass
	for _, a := range assignments {
		if downloadErrors >= cm.cfg.Sync.DownloadErrorThreshold {
			log.Printf("[cache] Phase D: stopping, reached %d download errors", downloadErrors)
			break
		}

		if a.assignedTier == -1 {
			// Evict or skip
			if a.local {
				srcPath := cm.tierFilePath(a.tier, a.path)
				if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
					log.Printf("[cache] Phase D: evict error %s: %v", a.path, err)
				}
				cm.db.DeleteLocalSize(a.path)
				delete(inventory, a.path)
				log.Printf("[cache] Phase D: evicted %s (score=%.4f)", a.path, a.score)
			}
			continue
		}

		if !a.local {
			// Download to assigned tier
			destPath := cm.tierFilePath(a.assignedTier, a.path)
			url := packagesURL + "/" + a.path
			if err := cm.downloader.Download(ctx, url, destPath); err != nil {
				log.Printf("[cache] Phase D: download error %s: %v", a.path, err)
				downloadErrors++
				continue
			}
			inventory[a.path] = localFile{size: a.size, tier: a.assignedTier}
			cm.db.SetLocalSize(a.path, a.size, a.assignedTier)
			log.Printf("[cache] Phase D: downloaded %s to tier %d (%d bytes)", a.path, a.assignedTier, a.size)
			continue
		}

		// Local file
		if a.tier == a.assignedTier {
			// No-op
			continue
		}

		// Needs promotion or demotion
		srcPath := cm.tierFilePath(a.tier, a.path)
		dstPath := cm.tierFilePath(a.assignedTier, a.path)
		if err := moveFile(srcPath, dstPath); err != nil {
			log.Printf("[cache] Phase D: move error %s tier %d->%d: %v", a.path, a.tier, a.assignedTier, err)
			continue
		}
		inventory[a.path] = localFile{size: a.size, tier: a.assignedTier}
		cm.db.UpdateLocalSizeTier(a.path, a.assignedTier)
		log.Printf("[cache] Phase D: moved %s tier %d->%d", a.path, a.tier, a.assignedTier)
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

// tierFilePath returns the absolute path for a file in a given tier.
func (cm *CacheManager) tierFilePath(tierIdx int, relPath string) string {
	return filepath.Join(cm.tiers[tierIdx].Path, relPath)
}

func (cm *CacheManager) inventoryLocal() (map[string]localFile, error) {
	inventory := make(map[string]localFile)

	for tierIdx, tier := range cm.tiers {
		err := filepath.WalkDir(tier.Path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(tier.Path, path)
			if err != nil {
				return err
			}

			// If already found in a hotter tier, skip (prefer hotter tier entry)
			if _, exists := inventory[relPath]; exists {
				return nil
			}

			size, dbTier, ok, dbErr := cm.db.GetLocalSize(relPath)
			if dbErr != nil {
				return dbErr
			}
			if ok {
				inventory[relPath] = localFile{size: size, tier: dbTier}
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}
			size = info.Size()
			if err := cm.db.SetLocalSize(relPath, size, tierIdx); err != nil {
				return err
			}
			inventory[relPath] = localFile{size: size, tier: tierIdx}
			return nil
		})

		if err != nil && !os.IsNotExist(err) {
			log.Printf("[cache] Phase A: walk error for tier %d (%s): %v", tierIdx, tier.Path, err)
		}
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

// moveFile moves src to dst, trying os.Rename first and falling back to
// copy+delete if they are on different filesystems.
func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Rename failed (possibly cross-device); fall back to copy+delete.
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove src after copy: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.CreateTemp(filepath.Dir(dst), ".tmp-")
	if err != nil {
		return err
	}
	tmpName := out.Name()

	_, err = io.Copy(out, in)
	out.Close()
	if err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dst)
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
