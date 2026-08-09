//go:build linux

package model

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const defaultFlockPollInterval = 20 * time.Millisecond

// RunRetentionLeaseDigest is the common lock identity used by a reader that
// needs one immutable base/dome run and by the retention cleaner that would
// otherwise remove those directories. It is intentionally independent of a
// particular manifest digest so the cleaner can acquire it before opening a
// run that may already be obsolete.
const RunRetentionLeaseDigest = "run-retention-v1"

// RunLeaseManager coordinates readers and cleaners of immutable model runs.
// Lock files are intentionally persistent: process death releases the kernel
// lock, so cleanup never depends on deleting a PID file.
type RunLeaseManager struct {
	root         string
	pollInterval time.Duration
}

// RunLease is a process-scoped advisory lease. Closing it is idempotent.
type RunLease struct {
	file     *os.File
	path     string
	closeErr error
	once     sync.Once
}

// NewRunLeaseManager creates a manager rooted below a caller-controlled state
// directory. Provider, run ID, and manifest digest never become path elements.
func NewRunLeaseManager(root string) (*RunLeaseManager, error) {
	if root == "" {
		return nil, errors.New("run lease root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve run lease root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("create run lease root: %w", err)
	}
	return &RunLeaseManager{root: absolute, pollInterval: defaultFlockPollInterval}, nil
}

// LockPath returns the traversal-safe lock path for one immutable run input.
func (manager *RunLeaseManager) LockPath(provider, runID, manifestDigest string) (string, error) {
	if manager == nil || manager.root == "" {
		return "", errors.New("run lease manager is not initialized")
	}
	identity, err := runLeaseIdentity(provider, runID, manifestDigest)
	if err != nil {
		return "", err
	}
	return filepath.Join(manager.root, identity+".lock"), nil
}

// AcquireShared pins a run while a reader uses it. It waits without spawning a
// goroutine and returns promptly when ctx is cancelled.
func (manager *RunLeaseManager) AcquireShared(ctx context.Context, provider, runID, manifestDigest string) (*RunLease, error) {
	return manager.acquire(ctx, provider, runID, manifestDigest, syscall.LOCK_SH, true)
}

// AcquireExclusive waits until no reader or other cleaner holds the run.
func (manager *RunLeaseManager) AcquireExclusive(ctx context.Context, provider, runID, manifestDigest string) (*RunLease, error) {
	return manager.acquire(ctx, provider, runID, manifestDigest, syscall.LOCK_EX, true)
}

// TryExclusive is the non-blocking cleaner operation. acquired=false means the
// run is currently pinned and must be skipped, not treated as an error.
func (manager *RunLeaseManager) TryExclusive(provider, runID, manifestDigest string) (lease *RunLease, acquired bool, err error) {
	lease, err = manager.acquire(context.Background(), provider, runID, manifestDigest, syscall.LOCK_EX, false)
	if errors.Is(err, errFlockBusy) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return lease, true, nil
}

func (manager *RunLeaseManager) acquire(ctx context.Context, provider, runID, manifestDigest string, operation int, wait bool) (*RunLease, error) {
	if ctx == nil {
		return nil, errors.New("run lease context is required")
	}
	path, err := manager.LockPath(provider, runID, manifestDigest)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open run lease %q: %w", path, err)
	}
	pollInterval := manager.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultFlockPollInterval
	}
	if !wait {
		err = flockOnce(file, operation)
	} else {
		err = flockContext(ctx, file, operation, pollInterval)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &RunLease{file: file, path: path}, nil
}

// Path is useful for diagnostics; callers must not remove a live lock file.
func (lease *RunLease) Path() string {
	if lease == nil {
		return ""
	}
	return lease.path
}

// Close releases the kernel lease and closes its descriptor.
func (lease *RunLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		if lease.file == nil {
			return
		}
		unlockErr := syscall.Flock(int(lease.file.Fd()), syscall.LOCK_UN)
		closeErr := lease.file.Close()
		lease.closeErr = errors.Join(unlockErr, closeErr)
	})
	return lease.closeErr
}

var errFlockBusy = errors.New("advisory lock is busy")

func flockOnce(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation|syscall.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
			return errFlockBusy
		default:
			return fmt.Errorf("acquire advisory lock: %w", err)
		}
	}
}

func flockContext(ctx context.Context, file *os.File, operation int, pollInterval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := flockOnce(file, operation)
		if err == nil {
			if contextErr := ctx.Err(); contextErr != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				return contextErr
			}
			return nil
		}
		if !errors.Is(err, errFlockBusy) {
			return err
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			_ = timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func runLeaseIdentity(provider, runID, manifestDigest string) (string, error) {
	parts := []string{provider, runID, manifestDigest}
	for index, part := range parts {
		if part == "" {
			return "", fmt.Errorf("run lease identity component %d is empty", index)
		}
		if len(part) > 4096 {
			return "", fmt.Errorf("run lease identity component %d is too long", index)
		}
	}
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
