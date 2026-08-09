package directional

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	resultManifestSchema     = 1
	maximumManifestBytes     = 64 << 10
	maximumMetadataTextBytes = 4096
)

type resultManifest struct {
	Schema          int       `json:"schema"`
	Kind            Kind      `json:"kind"`
	ScienceDigest   string    `json:"science_digest"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	DatasetFile     string    `json:"dataset_file"`
	DatasetBytes    int64     `json:"dataset_bytes"`
	ETag            string    `json:"etag"`
	ContentEncoding string    `json:"content_encoding,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	GridProfile     string    `json:"grid_profile,omitempty"`
	GeometryDigest  string    `json:"grid_geometry_digest,omitempty"`
}

type resultCache struct {
	root       string
	entriesDir string
	stagingDir string
	ttl        time.Duration
	limit      int
	now        func() time.Time

	mutex   sync.Mutex
	entries map[string]Result
}

func newResultCache(root string, ttl time.Duration, limit int, now func() time.Time) (*resultCache, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve directional result root: %w", err)
	}
	cache := &resultCache{
		root: absolute, entriesDir: filepath.Join(absolute, "entries"),
		stagingDir: filepath.Join(absolute, "staging"), ttl: ttl, limit: limit,
		now: now, entries: make(map[string]Result),
	}
	if err := os.MkdirAll(cache.entriesDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directional result entries: %w", err)
	}
	// A workspace is never a published result. Removing it at startup is safe
	// and guarantees interrupted runners cannot be mistaken for cache hits.
	if err := os.RemoveAll(cache.stagingDir); err != nil {
		return nil, fmt.Errorf("clean directional staging: %w", err)
	}
	if err := os.MkdirAll(cache.stagingDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directional staging: %w", err)
	}
	if err := cache.recover(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (cache *resultCache) newWorkspace(jobID string) (string, error) {
	workspace, err := os.MkdirTemp(cache.stagingDir, jobID+"-")
	if err != nil {
		return "", fmt.Errorf("create directional workspace: %w", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return "", fmt.Errorf("protect directional workspace: %w", err)
	}
	return workspace, nil
}

func (cache *resultCache) lookup(scienceDigest string) (Result, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.pruneLocked(cache.now().UTC())
	result, exists := cache.entries[scienceDigest]
	return result, exists
}

func (cache *resultCache) open(scienceDigest string) (*os.File, Result, error) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.pruneLocked(cache.now().UTC())
	result, exists := cache.entries[scienceDigest]
	if !exists {
		return nil, Result{}, ErrNotFound
	}
	file, err := openVerifiedDataset(result)
	if err != nil {
		_ = os.RemoveAll(filepath.Dir(result.Path))
		delete(cache.entries, scienceDigest)
		return nil, Result{}, ErrNotFound
	}
	return file, result, nil
}

func (cache *resultCache) publish(ctx context.Context, kind Kind, scienceDigest, workspace string, runnerResult RunnerResult) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("directional publication context is required")
	}
	if runnerResult.ContentEncoding != "" && runnerResult.ContentEncoding != "gzip" {
		return Result{}, errors.New("directional dataset content encoding must be empty or gzip")
	}
	for _, value := range []string{runnerResult.Provider, runnerResult.RunID, runnerResult.GridProfile, runnerResult.GeometryDigest} {
		if len(value) > maximumMetadataTextBytes {
			return Result{}, errors.New("directional result metadata is too large")
		}
	}
	sourcePath, err := validateWorkspaceResult(workspace, runnerResult.DatasetPath)
	if err != nil {
		return Result{}, err
	}

	now := cache.now().UTC()
	cache.mutex.Lock()
	cache.pruneLocked(now)
	if existing, ok := cache.entries[scienceDigest]; ok {
		cache.mutex.Unlock()
		return existing, nil
	}
	cache.mutex.Unlock()
	finalDirectory := filepath.Join(cache.entriesDir, scienceDigest)
	if existing, err := cache.loadEntry(finalDirectory, now); err == nil {
		cache.mutex.Lock()
		cache.entries[scienceDigest] = existing
		cache.mutex.Unlock()
		return existing, nil
	} else if removeErr := os.RemoveAll(finalDirectory); removeErr != nil {
		return Result{}, fmt.Errorf("remove invalid directional result: %w", removeErr)
	}

	publishDirectory, err := os.MkdirTemp(cache.entriesDir, ".publish-")
	if err != nil {
		return Result{}, fmt.Errorf("create directional publish directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(publishDirectory)
		}
	}()
	datasetFile := "dataset.json"
	if runnerResult.ContentEncoding == "gzip" {
		datasetFile = "dataset.json.gz"
	}
	datasetPath := filepath.Join(publishDirectory, datasetFile)
	source, err := os.Open(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("open runner dataset: %w", err)
	}
	destination, err := os.OpenFile(datasetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o440)
	if err != nil {
		_ = source.Close()
		return Result{}, fmt.Errorf("create published dataset: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hash), contextReader{ctx: ctx, reader: source})
	resultErr := errors.Join(copyErr, source.Close())
	if resultErr == nil {
		resultErr = destination.Sync()
	}
	resultErr = errors.Join(resultErr, destination.Close())
	if resultErr != nil {
		return Result{}, fmt.Errorf("publish directional dataset bytes: %w", resultErr)
	}
	etag := `"` + hex.EncodeToString(hash.Sum(nil)) + `"`
	manifest := resultManifest{
		Schema: resultManifestSchema, Kind: kind, ScienceDigest: scienceDigest,
		CreatedAt: now, ExpiresAt: now.Add(cache.ttl), DatasetFile: datasetFile,
		DatasetBytes: written, ETag: etag, ContentEncoding: runnerResult.ContentEncoding,
		Provider: runnerResult.Provider, RunID: runnerResult.RunID,
		GridProfile: runnerResult.GridProfile, GeometryDigest: runnerResult.GeometryDigest,
	}
	if err := writeAtomicManifest(publishDirectory, manifest); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := syncDirectory(publishDirectory); err != nil {
		return Result{}, fmt.Errorf("sync directional publish directory: %w", err)
	}
	if err := os.Rename(publishDirectory, finalDirectory); err != nil {
		if existing, loadErr := cache.loadEntry(finalDirectory, now); loadErr == nil {
			cache.mutex.Lock()
			cache.entries[scienceDigest] = existing
			cache.mutex.Unlock()
			return existing, nil
		}
		return Result{}, fmt.Errorf("atomically publish directional result: %w", err)
	}
	cleanup = false
	if err := syncDirectory(cache.entriesDir); err != nil {
		return Result{}, fmt.Errorf("sync directional entries directory: %w", err)
	}
	result := manifest.result(finalDirectory)
	cache.mutex.Lock()
	cache.entries[scienceDigest] = result
	cache.pruneLocked(now)
	cache.mutex.Unlock()
	return result, nil
}

func (cache *resultCache) recover() error {
	entries, err := os.ReadDir(cache.entriesDir)
	if err != nil {
		return fmt.Errorf("list directional result cache: %w", err)
	}
	now := cache.now().UTC()
	for _, entry := range entries {
		path := filepath.Join(cache.entriesDir, entry.Name())
		if strings.HasPrefix(entry.Name(), ".publish-") {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove interrupted directional publication: %w", err)
			}
			continue
		}
		if !entry.IsDir() || !validScienceDigest(entry.Name()) {
			continue
		}
		result, loadErr := cache.loadEntry(path, now)
		if loadErr != nil {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove invalid directional result: %w", err)
			}
			continue
		}
		cache.entries[entry.Name()] = result
	}
	cache.pruneLocked(now)
	return syncDirectory(cache.entriesDir)
}

func (cache *resultCache) loadEntry(directory string, now time.Time) (Result, error) {
	manifestFile, err := os.Open(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return Result{}, err
	}
	decoder := json.NewDecoder(io.LimitReader(manifestFile, maximumManifestBytes))
	decoder.DisallowUnknownFields()
	var manifest resultManifest
	decodeErr := decoder.Decode(&manifest)
	var trailing any
	if decodeErr == nil {
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			decodeErr = errors.New("directional manifest contains trailing JSON")
		}
	}
	closeErr := manifestFile.Close()
	if err := errors.Join(decodeErr, closeErr); err != nil {
		return Result{}, err
	}
	if err := validateResultManifest(manifest, filepath.Base(directory), now); err != nil {
		return Result{}, err
	}
	result := manifest.result(directory)
	verified, err := openVerifiedDataset(result)
	if err != nil {
		return Result{}, err
	}
	if err := verified.Close(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (cache *resultCache) pruneLocked(now time.Time) {
	type candidate struct {
		digest string
		result Result
	}
	ordered := make([]candidate, 0, len(cache.entries))
	for digest, result := range cache.entries {
		if !result.ExpiresAt.After(now) {
			_ = os.RemoveAll(filepath.Join(cache.entriesDir, digest))
			delete(cache.entries, digest)
			continue
		}
		ordered = append(ordered, candidate{digest: digest, result: result})
	}
	sort.Slice(ordered, func(first, second int) bool {
		return ordered[first].result.CreatedAt.Before(ordered[second].result.CreatedAt)
	})
	for len(ordered) > cache.limit {
		oldest := ordered[0]
		ordered = ordered[1:]
		_ = os.RemoveAll(filepath.Join(cache.entriesDir, oldest.digest))
		delete(cache.entries, oldest.digest)
	}
}

func validateWorkspaceResult(workspace, datasetPath string) (string, error) {
	if workspace == "" || datasetPath == "" {
		return "", errors.New("runner dataset path is required")
	}
	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve directional workspace: %w", err)
	}
	absoluteDataset := datasetPath
	if !filepath.IsAbs(absoluteDataset) {
		absoluteDataset = filepath.Join(workspace, absoluteDataset)
	}
	realDataset, err := filepath.EvalSymlinks(absoluteDataset)
	if err != nil {
		return "", fmt.Errorf("resolve runner dataset: %w", err)
	}
	relative, err := filepath.Rel(realWorkspace, realDataset)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("runner dataset escapes its workspace")
	}
	info, err := os.Lstat(realDataset)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("runner dataset is not a regular file")
	}
	return realDataset, nil
}

func writeAtomicManifest(directory string, manifest resultManifest) error {
	temporaryPath := filepath.Join(directory, "manifest.json.tmp")
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o440)
	if err != nil {
		return fmt.Errorf("create directional result manifest: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	writeErr := encoder.Encode(manifest)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	writeErr = errors.Join(writeErr, file.Close())
	if writeErr != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("write directional result manifest: %w", writeErr)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, "manifest.json")); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("publish directional result manifest: %w", err)
	}
	return nil
}

func validateResultManifest(manifest resultManifest, directoryName string, now time.Time) error {
	if manifest.Schema != resultManifestSchema || !validKind(manifest.Kind) || manifest.ScienceDigest != directoryName || !validScienceDigest(manifest.ScienceDigest) {
		return errors.New("directional result manifest identity is invalid")
	}
	if manifest.CreatedAt.IsZero() || manifest.ExpiresAt.IsZero() || !manifest.ExpiresAt.After(manifest.CreatedAt) || !manifest.ExpiresAt.After(now) {
		return errors.New("directional result manifest lifetime is invalid")
	}
	if manifest.DatasetFile != "dataset.json" && manifest.DatasetFile != "dataset.json.gz" {
		return errors.New("directional result manifest dataset name is invalid")
	}
	if manifest.DatasetBytes < 0 || (manifest.ContentEncoding != "" && manifest.ContentEncoding != "gzip") {
		return errors.New("directional result manifest dataset metadata is invalid")
	}
	if len(manifest.ETag) != 66 || manifest.ETag[0] != '"' || manifest.ETag[len(manifest.ETag)-1] != '"' {
		return errors.New("directional result manifest ETag is invalid")
	}
	if _, err := hex.DecodeString(manifest.ETag[1 : len(manifest.ETag)-1]); err != nil {
		return errors.New("directional result manifest ETag is invalid")
	}
	return nil
}

func (manifest resultManifest) result(directory string) Result {
	return Result{
		Path: filepath.Join(directory, manifest.DatasetFile), Bytes: manifest.DatasetBytes,
		ETag: manifest.ETag, ContentEncoding: manifest.ContentEncoding,
		Provider: manifest.Provider, RunID: manifest.RunID, GridProfile: manifest.GridProfile,
		GeometryDigest: manifest.GeometryDigest, CreatedAt: manifest.CreatedAt, ExpiresAt: manifest.ExpiresAt,
	}
}

func validScienceDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func openVerifiedDataset(result Result) (*os.File, error) {
	file, err := os.Open(result.Path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != result.Bytes {
		_ = file.Close()
		return nil, errors.New("directional cached dataset is missing or incomplete")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	actualETag := `"` + hex.EncodeToString(hash.Sum(nil)) + `"`
	if actualETag != result.ETag {
		_ = file.Close()
		return nil, errors.New("directional cached dataset checksum mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func syncDirectory(path string) (resultErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.Close())
	}()
	return directory.Sync()
}
