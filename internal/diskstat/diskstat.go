package diskstat

import (
	"io/fs"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Warning levels for disk space.
const (
	WarnNone   = 0
	WarnYellow = 1
	WarnRed    = 2
	WarnBlock  = 3
)

// Stats is a point-in-time snapshot of disk usage.
type Stats struct {
	TotalBytes       uint64
	FreeBytes        uint64
	AppBytes         uint64 // bytes under DATA_DIR
	WatermarkedBytes uint64
	AssetsBytes      uint64
	UploadsBytes     uint64
	CapturedAt       time.Time
}

// PctFree returns the percentage of disk space that is free (0–100).
func (s Stats) PctFree() float64 {
	if s.TotalBytes == 0 {
		return 100
	}
	return float64(s.FreeBytes) / float64(s.TotalBytes) * 100
}

// WarningLevel returns the warning level given threshold percentages.
func (s Stats) WarningLevel(yellowPct, redPct, blockPct float64) int {
	pct := s.PctFree()
	switch {
	case pct <= blockPct:
		return WarnBlock
	case pct <= redPct:
		return WarnRed
	case pct <= yellowPct:
		return WarnYellow
	default:
		return WarnNone
	}
}

// PublishEstimate returns estimated bytes needed for a campaign publish.
func PublishEstimate(assetBytes int64, recipientCount int, compressionFactor float64) int64 {
	if compressionFactor <= 0 {
		compressionFactor = 0.9
	}
	return int64(float64(assetBytes) * compressionFactor * float64(recipientCount))
}

// Cache is a goroutine-safe cached disk stats value, refreshed periodically.
type Cache struct {
	mu      sync.RWMutex
	stats   Stats
	dataDir string
	ttl     time.Duration
	stop    chan struct{}
}

// New creates a Cache and starts background polling.
func New(dataDir string, ttl time.Duration) *Cache {
	c := &Cache{
		dataDir: dataDir,
		ttl:     ttl,
		stop:    make(chan struct{}),
	}
	return c
}

// Start begins background polling.
func (c *Cache) Start() {
	c.refresh()
	go func() {
		t := time.NewTicker(c.ttl)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-t.C:
				c.refresh()
			}
		}
	}()
}

// Stop halts background polling.
func (c *Cache) Stop() {
	select {
	case c.stop <- struct{}{}:
	default:
	}
}

// Get returns the latest cached stats.
func (c *Cache) Get() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

// Refresh forces an immediate update.
func (c *Cache) Refresh() {
	c.refresh()
}

func (c *Cache) refresh() {
	total, free, err := statFS(c.dataDir)
	if err != nil {
		// Not fatal; leave previous values in place
		return
	}
	app, wm, assets, uploads := walkDirSizes(c.dataDir)
	s := Stats{
		TotalBytes:       total,
		FreeBytes:        free,
		AppBytes:         app,
		WatermarkedBytes: wm,
		AssetsBytes:      assets,
		UploadsBytes:     uploads,
		CapturedAt:       time.Now(),
	}
	c.mu.Lock()
	c.stats = s
	c.mu.Unlock()
}

func statFS(path string) (total, free uint64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	bsize := uint64(stat.Bsize)
	return bsize * stat.Blocks, bsize * stat.Bfree, nil
}

func walkDirSizes(dataDir string) (total, watermarked, assets, uploads uint64) {
	filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size := uint64(info.Size())
		total += size
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return nil
		}
		switch {
		case len(rel) >= 11 && rel[:11] == "watermarked":
			watermarked += size
		case len(rel) >= 9 && rel[:9] == "originals":
			assets += size
		case len(rel) >= 7 && rel[:7] == "uploads":
			uploads += size
		}
		return nil
	})
	return
}
