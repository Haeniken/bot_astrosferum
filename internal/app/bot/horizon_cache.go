package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	horizonCacheImage    = "horizon.png"
	horizonCacheManifest = "manifest.json"
	horizonCacheLease    = ".lease-"
)

type horizonCache struct {
	root    string
	ttl     time.Duration
	maximum int
	mu      sync.Mutex
}

type horizonCacheManifestData struct {
	Schema    string    `json:"schema"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
}

type horizonCacheCandidate struct {
	path     string
	modified time.Time
}

func newHorizonCache(root string, ttl time.Duration, maximum int) (*horizonCache, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create horizon cache: %w", err)
	}
	cache := &horizonCache{root: root, ttl: ttl, maximum: maximum}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := cache.cleanupLocked(time.Now(), true); err != nil {
		return nil, err
	}
	return cache, nil
}

func (cache *horizonCache) load(key string, now time.Time) (string, bool) {
	if !validHorizonCacheKey(key) {
		return "", false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	path, ok := cache.loadLocked(key, now, true)
	if !ok {
		_ = os.RemoveAll(filepath.Join(cache.root, key))
	}
	return path, ok
}

func (cache *horizonCache) publish(key string, now time.Time, render func(string) error) (string, error) {
	if !validHorizonCacheKey(key) {
		return "", errors.New("invalid horizon cache key")
	}
	if render == nil {
		return "", errors.New("horizon cache renderer is required")
	}
	if path, ok := cache.load(key, now); ok {
		return path, nil
	}
	staging, err := os.MkdirTemp(cache.root, ".incoming-")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = os.RemoveAll(staging)
	}()
	imagePath := filepath.Join(staging, horizonCacheImage)
	if err := render(imagePath); err != nil {
		return "", err
	}
	info, err := os.Stat(imagePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", errors.New("horizon renderer did not produce a non-empty regular image")
	}
	manifest := horizonCacheManifestData{Schema: horizonCacheSchema, Key: key, CreatedAt: now.UTC()}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(staging, horizonCacheManifest), manifestBytes, 0o640); err != nil {
		return "", err
	}
	// Rendering intentionally happens outside the cache lock. While a heavy
	// worker renders, another callback can still inspect the cache and
	// join the in-memory singleflight instead of blocking for minutes.
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if path, ok := cache.loadLocked(key, now, true); ok {
		return path, nil
	}
	final := filepath.Join(cache.root, key)
	if err := os.Rename(staging, final); err != nil {
		if path, ok := cache.loadLocked(key, now, true); ok {
			return path, nil
		}
		return "", err
	}
	_ = os.Chtimes(final, now, now)
	if err := cache.cleanupLocked(now, false); err != nil {
		return "", err
	}
	return filepath.Join(final, horizonCacheImage), nil
}

func (cache *horizonCache) cleanup(now time.Time) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.cleanupLocked(now, false)
}

// lease hard-links a completed image into the cache root. Cleanup can then
// evict the keyed directory without invalidating an already-admitted delivery;
// the link is removed by the delivery worker after the upload attempt.
func (cache *horizonCache) lease(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", errors.New("horizon cache image is unavailable")
	}
	lease, err := os.CreateTemp(cache.root, horizonCacheLease+"*.png")
	if err != nil {
		return "", err
	}
	leasePath := lease.Name()
	if err := lease.Close(); err != nil {
		_ = os.Remove(leasePath)
		return "", err
	}
	if err := os.Remove(leasePath); err != nil {
		return "", err
	}
	if err := os.Link(path, leasePath); err != nil {
		return "", err
	}
	return leasePath, nil
}

func (cache *horizonCache) loadLocked(key string, now time.Time, touch bool) (string, bool) {
	directory := filepath.Join(cache.root, key)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", false
	}
	manifest, err := readHorizonCacheManifest(filepath.Join(directory, horizonCacheManifest))
	age := now.Sub(manifest.CreatedAt)
	if err != nil || manifest.Schema != horizonCacheSchema || manifest.Key != key || manifest.CreatedAt.IsZero() || age > cache.ttl || age < -5*time.Minute {
		return "", false
	}
	path := filepath.Join(directory, horizonCacheImage)
	image, err := os.Stat(path)
	if err != nil || !image.Mode().IsRegular() || image.Size() <= 0 {
		return "", false
	}
	if touch {
		_ = os.Chtimes(directory, now, now)
	}
	return path, true
}

func (cache *horizonCache) cleanupLocked(now time.Time, startup bool) error {
	entries, err := os.ReadDir(cache.root)
	if err != nil {
		return fmt.Errorf("read horizon cache: %w", err)
	}
	candidates := make([]horizonCacheCandidate, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(cache.root, name)
		if strings.HasPrefix(name, ".incoming-") || strings.HasPrefix(name, horizonCacheLease) {
			if startup {
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("remove abandoned horizon cache artifact: %w", err)
				}
			}
			continue
		}
		if !entry.IsDir() || !validHorizonCacheKey(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if _, ok := cache.loadLocked(name, now, false); !ok {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove expired horizon cache entry: %w", err)
			}
			continue
		}
		candidates = append(candidates, horizonCacheCandidate{path: path, modified: info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified.After(candidates[j].modified) })
	if len(candidates) > cache.maximum {
		for _, candidate := range candidates[cache.maximum:] {
			if err := os.RemoveAll(candidate.path); err != nil {
				return fmt.Errorf("remove excess horizon cache entry: %w", err)
			}
		}
	}
	return nil
}

func readHorizonCacheManifest(path string) (horizonCacheManifestData, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return horizonCacheManifestData{}, err
	}
	var manifest horizonCacheManifestData
	if err := json.Unmarshal(content, &manifest); err != nil {
		return horizonCacheManifestData{}, err
	}
	return manifest, nil
}

func validHorizonCacheKey(key string) bool {
	if len(key) != sha256HexLength {
		return false
	}
	for _, character := range key {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

const sha256HexLength = 64
