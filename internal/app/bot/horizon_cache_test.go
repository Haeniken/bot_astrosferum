package bot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHorizonCachePublicationIsAtomic(t *testing.T) {
	root := t.TempDir()
	cache, err := newHorizonCache(root, time.Hour, 4)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", sha256HexLength)
	_, err = cache.publish(key, time.Now(), func(destination string) error {
		if writeErr := os.WriteFile(destination, []byte("partial"), 0o640); writeErr != nil {
			return writeErr
		}
		return errors.New("render failed")
	})
	if err == nil {
		t.Fatal("partial render unexpectedly succeeded")
	}
	if _, statErr := os.Stat(filepath.Join(root, key)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial final cache exists: %v", statErr)
	}
	assertNoHorizonStaging(t, root)

	path, err := cache.publish(key, time.Now(), func(destination string) error {
		return os.WriteFile(destination, []byte("complete"), 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	if content, readErr := os.ReadFile(path); readErr != nil || string(content) != "complete" {
		t.Fatalf("published image = %q, %v", content, readErr)
	}
	if loaded, ok := cache.load(key, time.Now()); !ok || loaded != path {
		t.Fatalf("load = %q, %t; want %q, true", loaded, ok, path)
	}
}

func TestHorizonCacheDoesNotHoldLockWhileRendering(t *testing.T) {
	root := t.TempDir()
	cache, err := newHorizonCache(root, time.Hour, 4)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("e", sha256HexLength)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, publishErr := cache.publish(key, time.Now(), func(destination string) error {
			close(started)
			<-release
			return os.WriteFile(destination, []byte("image"), 0o640)
		})
		finished <- publishErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cache render did not start")
	}
	checked := make(chan struct{})
	go func() {
		_, _ = cache.load(key, time.Now())
		close(checked)
	}()
	select {
	case <-checked:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cache lookup blocked behind an in-progress render")
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cache publish did not finish")
	}
}

func TestHorizonCacheStartupRemovesStagingStaleAndInvalidEntries(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	incoming := filepath.Join(root, ".incoming-crashed")
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		t.Fatal(err)
	}
	staleKey := strings.Repeat("b", sha256HexLength)
	writeHorizonCacheFixture(t, root, staleKey, now.Add(-2*time.Hour), true)
	invalidKey := strings.Repeat("c", sha256HexLength)
	writeHorizonCacheFixture(t, root, invalidKey, now, false)
	cache, err := newHorizonCache(root, time.Hour, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{incoming, filepath.Join(root, staleKey), filepath.Join(root, invalidKey)} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("startup cleanup retained %q: %v", path, statErr)
		}
	}
	if cache == nil {
		t.Fatal("cache is nil")
	}
}

func TestHorizonCacheTTLAndEntryCapBoundGrowth(t *testing.T) {
	root := t.TempDir()
	cache, err := newHorizonCache(root, time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	keys := []string{strings.Repeat("1", sha256HexLength), strings.Repeat("2", sha256HexLength), strings.Repeat("3", sha256HexLength)}
	for index, key := range keys {
		_, err := cache.publish(key, now.Add(time.Duration(index)*time.Minute), func(destination string) error {
			return os.WriteFile(destination, []byte("image"), 0o640)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, keys[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest cache entry survived cap: %v", err)
	}
	for _, key := range keys[1:] {
		if _, err := os.Stat(filepath.Join(root, key)); err != nil {
			t.Fatalf("recent cache entry %q missing: %v", key, err)
		}
	}

	if _, ok := cache.load(keys[1], now.Add(30*time.Minute)); !ok {
		t.Fatal("fresh cache entry was not returned before TTL")
	}
	if _, ok := cache.load(keys[1], now.Add(2*time.Hour)); ok {
		t.Fatal("LRU access extended the maximum result age beyond TTL")
	}
	if _, err := os.Stat(filepath.Join(root, keys[1])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired cache entry was not deleted: %v", err)
	}
}

func TestHorizonCacheSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	key := strings.Repeat("d", sha256HexLength)
	now := time.Now()
	first, err := newHorizonCache(root, time.Hour, 4)
	if err != nil {
		t.Fatal(err)
	}
	want, err := first.publish(key, now, func(destination string) error {
		return os.WriteFile(destination, []byte("persistent"), 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := newHorizonCache(root, time.Hour, 4)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := second.load(key, now.Add(time.Minute))
	if !ok || got != want {
		t.Fatalf("restart load = %q, %t; want %q, true", got, ok, want)
	}
}

func TestHorizonCacheLeaseSurvivesEntryEviction(t *testing.T) {
	root := t.TempDir()
	cache, err := newHorizonCache(root, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	firstKey := strings.Repeat("4", sha256HexLength)
	firstPath, err := cache.publish(firstKey, now, func(destination string) error {
		return os.WriteFile(destination, []byte("first-image"), 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := cache.lease(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(lease)
	}()

	secondKey := strings.Repeat("5", sha256HexLength)
	if _, err := cache.publish(secondKey, now.Add(time.Minute), func(destination string) error {
		return os.WriteFile(destination, []byte("second-image"), 0o640)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, firstKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first keyed entry survived eviction: %v", err)
	}
	content, err := os.ReadFile(lease)
	if err != nil || string(content) != "first-image" {
		t.Fatalf("leased image after eviction = %q, %v", content, err)
	}
}

func writeHorizonCacheFixture(t *testing.T, root, key string, modified time.Time, withImage bool) {
	t.Helper()
	directory := filepath.Join(root, key)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema":"` + horizonCacheSchema + `","key":"` + key + `","created_at":"` + modified.UTC().Format(time.RFC3339Nano) + `"}`)
	if err := os.WriteFile(filepath.Join(directory, horizonCacheManifest), manifest, 0o640); err != nil {
		t.Fatal(err)
	}
	if withImage {
		if err := os.WriteFile(filepath.Join(directory, horizonCacheImage), []byte("image"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(directory, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func assertNoHorizonStaging(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".incoming-") {
			t.Fatalf("cache staging directory leaked: %q", entry.Name())
		}
	}
}

func assertNoHorizonLeases(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), horizonCacheLease) {
			t.Fatalf("cache delivery lease leaked: %q", entry.Name())
		}
	}
}
