//go:build linux

package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// HardDiskProjectCapBytes is an invariant of the Astrodome rollout. A
	// configured budget may be lower, but never higher.
	HardDiskProjectCapBytes   uint64 = 400 << 30
	diskReservationSchema            = 1
	maxReservationRecordBytes        = 64 << 10
)

var (
	ErrDiskProjectCap = errors.New("disk project cap would be exceeded")
	ErrDiskFreeBytes  = errors.New("minimum free bytes would be violated")
	ErrDiskFreeInodes = errors.New("minimum free inodes would be violated")
)

// StorageProfile is selected solely from the projected storage peak. Runtime,
// payload, or scientific errors are deliberately absent from this API.
type StorageProfile string

const (
	StorageProfileUnavailable StorageProfile = "unavailable"
	StorageProfileDense       StorageProfile = "dense"
	StorageProfileSparse      StorageProfile = "sparse"
)

// DiskProjection is additional peak allocation reserved by one operation.
type DiskProjection struct {
	Bytes  uint64
	Inodes uint64
}

// DiskBudgetConfig names all managed storage classes explicitly. Overlapping
// roots and hardlinks are safe because usage is deduplicated by device+inode.
type DiskBudgetConfig struct {
	LockPath       string
	ReservationDir string
	FilesystemPath string

	PublishedRoots []string
	StagingRoots   []string
	LeasedRoots    []string
	CacheRoots     []string
	TemporaryRoots []string

	ProjectCapBytes uint64
	MinFreeBytes    uint64
	MinFreeInodes   uint64
}

// DiskBudget serializes admission, stale-record recovery, and release under a
// single host-wide advisory lock shared by all configured processes.
type DiskBudget struct {
	lockPath       string
	reservationDir string
	filesystemPath string
	managedRoots   []string
	projectCap     uint64
	minFreeBytes   uint64
	minFreeInodes  uint64
	pollInterval   time.Duration
}

// DiskBudgetSnapshot is measured while holding the host-wide lock.
type DiskBudgetSnapshot struct {
	AllocatedBytes     uint64
	AllocatedInodes    uint64
	ReservedBytes      uint64
	ReservedInodes     uint64
	ActiveReservations int
	FreeBytes          uint64
	FreeInodes         uint64
	ProjectCapBytes    uint64
}

// DiskReservation stays active for as long as its record descriptor remains
// exclusively locked. A process crash releases that lock automatically.
type DiskReservation struct {
	budget     *DiskBudget
	file       *os.File
	path       string
	projection DiskProjection
	mutex      sync.Mutex
	released   bool
	releaseErr error
}

type diskReservationRecord struct {
	Schema    int            `json:"schema"`
	ID        string         `json:"id"`
	PID       int            `json:"pid"`
	CreatedAt time.Time      `json:"created_at"`
	Peak      DiskProjection `json:"peak"`
}

type diskFileIdentity struct {
	device uint64
	inode  uint64
}

// NewDiskBudget validates the hard cap and prepares the lock/state locations.
func NewDiskBudget(config DiskBudgetConfig) (*DiskBudget, error) {
	if config.LockPath == "" || config.ReservationDir == "" || config.FilesystemPath == "" {
		return nil, errors.New("disk lock, reservation directory, and filesystem path are required")
	}
	projectCap := config.ProjectCapBytes
	if projectCap == 0 {
		projectCap = HardDiskProjectCapBytes
	}
	if projectCap > HardDiskProjectCapBytes {
		return nil, fmt.Errorf("disk project cap %d exceeds hard limit %d", projectCap, HardDiskProjectCapBytes)
	}
	lockPath, err := filepath.Abs(config.LockPath)
	if err != nil {
		return nil, fmt.Errorf("resolve disk lock path: %w", err)
	}
	reservationDir, err := filepath.Abs(config.ReservationDir)
	if err != nil {
		return nil, fmt.Errorf("resolve reservation directory: %w", err)
	}
	filesystemPath, err := filepath.Abs(config.FilesystemPath)
	if err != nil {
		return nil, fmt.Errorf("resolve filesystem path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o750); err != nil {
		return nil, fmt.Errorf("create disk lock directory: %w", err)
	}
	if err := os.MkdirAll(reservationDir, 0o750); err != nil {
		return nil, fmt.Errorf("create disk reservation directory: %w", err)
	}
	if _, err := os.Stat(filesystemPath); err != nil {
		return nil, fmt.Errorf("stat disk budget filesystem path: %w", err)
	}

	roots := make([]string, 0, len(config.PublishedRoots)+len(config.StagingRoots)+len(config.LeasedRoots)+len(config.CacheRoots)+len(config.TemporaryRoots))
	for _, group := range [][]string{
		config.PublishedRoots, config.StagingRoots, config.LeasedRoots,
		config.CacheRoots, config.TemporaryRoots,
	} {
		for _, root := range group {
			if root == "" {
				return nil, errors.New("disk budget managed root cannot be empty")
			}
			absolute, resolveErr := filepath.Abs(root)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve managed root %q: %w", root, resolveErr)
			}
			roots = append(roots, absolute)
		}
	}

	return &DiskBudget{
		lockPath: lockPath, reservationDir: reservationDir,
		filesystemPath: filesystemPath, managedRoots: roots,
		projectCap: projectCap, minFreeBytes: config.MinFreeBytes,
		minFreeInodes: config.MinFreeInodes, pollInterval: defaultFlockPollInterval,
	}, nil
}

// SelectStorageProfile enforces dense preference exactly. Sparse is eligible
// only when dense exceeds the configured byte cap and sparse does not.
func SelectStorageProfile(denseProjectedPeakBytes, sparseProjectedPeakBytes, projectCapBytes uint64) StorageProfile {
	if projectCapBytes == 0 || projectCapBytes > HardDiskProjectCapBytes {
		return StorageProfileUnavailable
	}
	if denseProjectedPeakBytes <= projectCapBytes {
		return StorageProfileDense
	}
	if sparseProjectedPeakBytes <= projectCapBytes {
		return StorageProfileSparse
	}
	return StorageProfileUnavailable
}

// Snapshot returns allocated blocks, active outstanding reservations, and the
// independent filesystem free-space counters. It also removes stale records.
func (budget *DiskBudget) Snapshot(ctx context.Context) (DiskBudgetSnapshot, error) {
	var snapshot DiskBudgetSnapshot
	err := budget.withHostLock(ctx, func() error {
		measured, err := budget.snapshotLocked()
		if err != nil {
			return err
		}
		snapshot = measured
		return nil
	})
	return snapshot, err
}

// Reserve admits one generic operation against cap, byte, and inode limits.
func (budget *DiskBudget) Reserve(ctx context.Context, id string, projection DiskProjection) (*DiskReservation, error) {
	var reservation *DiskReservation
	err := budget.withHostLock(ctx, func() error {
		snapshot, err := budget.snapshotLocked()
		if err != nil {
			return err
		}
		if err := budget.checkProjection(snapshot, projection); err != nil {
			return err
		}
		reservation, err = budget.createReservationLocked(id, projection)
		return err
	})
	return reservation, err
}

// ReserveProfile chooses dense/sparse using only the byte cap, then validates
// the selected projection against independent free-byte and free-inode limits.
func (budget *DiskBudget) ReserveProfile(ctx context.Context, id string, dense, sparse DiskProjection) (*DiskReservation, StorageProfile, error) {
	profile := StorageProfileUnavailable
	var reservation *DiskReservation
	err := budget.withHostLock(ctx, func() error {
		snapshot, err := budget.snapshotLocked()
		if err != nil {
			return err
		}
		basePeak, overflow := addUint64(snapshot.AllocatedBytes, snapshot.ReservedBytes)
		if overflow {
			return ErrDiskProjectCap
		}
		densePeak, denseOverflow := addUint64(basePeak, dense.Bytes)
		sparsePeak, sparseOverflow := addUint64(basePeak, sparse.Bytes)
		if denseOverflow {
			densePeak = math.MaxUint64
		}
		if sparseOverflow {
			sparsePeak = math.MaxUint64
		}
		profile = SelectStorageProfile(densePeak, sparsePeak, budget.projectCap)
		var selected DiskProjection
		switch profile {
		case StorageProfileDense:
			selected = dense
		case StorageProfileSparse:
			selected = sparse
		default:
			return ErrDiskProjectCap
		}
		if err := budget.checkProjection(snapshot, selected); err != nil {
			profile = StorageProfileUnavailable
			return err
		}
		reservation, err = budget.createReservationLocked(id, selected)
		return err
	})
	return reservation, profile, err
}

// RecoverStaleReservations removes atomic records whose owning process no
// longer holds the kernel lock.
func (budget *DiskBudget) RecoverStaleReservations(ctx context.Context) (int, error) {
	recovered := 0
	err := budget.withHostLock(ctx, func() error {
		_, _, _, recoveredCount, err := budget.scanReservationsLocked()
		recovered = recoveredCount
		return err
	})
	return recovered, err
}

func (budget *DiskBudget) snapshotLocked() (DiskBudgetSnapshot, error) {
	allocatedBytes, allocatedInodes, err := allocatedDiskUsage(budget.managedRoots)
	if err != nil {
		return DiskBudgetSnapshot{}, err
	}
	reservedBytes, reservedInodes, active, _, err := budget.scanReservationsLocked()
	if err != nil {
		return DiskBudgetSnapshot{}, err
	}
	freeBytes, freeInodes, err := filesystemFree(budget.filesystemPath)
	if err != nil {
		return DiskBudgetSnapshot{}, err
	}
	return DiskBudgetSnapshot{
		AllocatedBytes: allocatedBytes, AllocatedInodes: allocatedInodes,
		ReservedBytes: reservedBytes, ReservedInodes: reservedInodes,
		ActiveReservations: active, FreeBytes: freeBytes, FreeInodes: freeInodes,
		ProjectCapBytes: budget.projectCap,
	}, nil
}

func (budget *DiskBudget) checkProjection(snapshot DiskBudgetSnapshot, projection DiskProjection) error {
	projected, overflow := addUint64(snapshot.AllocatedBytes, snapshot.ReservedBytes, projection.Bytes)
	if overflow || projected > budget.projectCap {
		return ErrDiskProjectCap
	}
	requiredFreeBytes, overflow := addUint64(snapshot.ReservedBytes, projection.Bytes, budget.minFreeBytes)
	if overflow || requiredFreeBytes > snapshot.FreeBytes {
		return ErrDiskFreeBytes
	}
	requiredFreeInodes, overflow := addUint64(snapshot.ReservedInodes, projection.Inodes, budget.minFreeInodes)
	if overflow || requiredFreeInodes > snapshot.FreeInodes {
		return ErrDiskFreeInodes
	}
	return nil
}

func (budget *DiskBudget) createReservationLocked(id string, projection DiskProjection) (*DiskReservation, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("disk reservation ID is required")
	}
	if len(id) > 4096 {
		return nil, errors.New("disk reservation ID is too long")
	}
	temporary, err := os.CreateTemp(budget.reservationDir, ".reservation-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create disk reservation record: %w", err)
	}
	temporaryPath := temporary.Name()
	finalPath := strings.TrimSuffix(temporaryPath, ".tmp") + ".json"
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		_ = os.Remove(finalPath)
	}
	if err := temporary.Chmod(0o640); err != nil {
		cleanup()
		return nil, fmt.Errorf("set disk reservation permissions: %w", err)
	}
	record := diskReservationRecord{
		Schema: diskReservationSchema, ID: id, PID: os.Getpid(),
		CreatedAt: time.Now().UTC(), Peak: projection,
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		cleanup()
		return nil, fmt.Errorf("write disk reservation record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("sync disk reservation record: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		cleanup()
		return nil, fmt.Errorf("publish disk reservation record: %w", err)
	}
	if err := syncDirectory(budget.reservationDir); err != nil {
		cleanup()
		return nil, fmt.Errorf("sync disk reservation directory: %w", err)
	}
	if err := flockOnce(temporary, syscall.LOCK_EX); err != nil {
		cleanup()
		return nil, fmt.Errorf("lock disk reservation record: %w", err)
	}
	return &DiskReservation{
		budget: budget, file: temporary, path: finalPath, projection: projection,
	}, nil
}

func (budget *DiskBudget) scanReservationsLocked() (reservedBytes, reservedInodes uint64, active, recovered int, resultErr error) {
	entries, err := os.ReadDir(budget.reservationDir)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("list disk reservations: %w", err)
	}
	removed := false
	for _, entry := range entries {
		path := filepath.Join(budget.reservationDir, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return 0, 0, 0, recovered, fmt.Errorf("remove stale reservation temporary file: %w", removeErr)
			}
			removed = true
			recovered++
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file, openErr := os.OpenFile(path, os.O_RDWR, 0)
		if openErr != nil {
			return 0, 0, 0, recovered, fmt.Errorf("open disk reservation %q: %w", path, openErr)
		}
		lockErr := flockOnce(file, syscall.LOCK_EX)
		if lockErr == nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return 0, 0, 0, recovered, fmt.Errorf("remove stale disk reservation %q: %w", path, removeErr)
			}
			removed = true
			recovered++
			continue
		}
		if !errors.Is(lockErr, errFlockBusy) {
			_ = file.Close()
			return 0, 0, 0, recovered, fmt.Errorf("inspect disk reservation %q: %w", path, lockErr)
		}
		record, decodeErr := decodeReservationRecord(file)
		_ = file.Close()
		if decodeErr != nil {
			return 0, 0, 0, recovered, fmt.Errorf("read active disk reservation %q: %w", path, decodeErr)
		}
		var overflow bool
		reservedBytes, overflow = addUint64(reservedBytes, record.Peak.Bytes)
		if overflow {
			return 0, 0, 0, recovered, errors.New("active disk reservation byte sum overflow")
		}
		reservedInodes, overflow = addUint64(reservedInodes, record.Peak.Inodes)
		if overflow {
			return 0, 0, 0, recovered, errors.New("active disk reservation inode sum overflow")
		}
		active++
	}
	if removed {
		if err := syncDirectory(budget.reservationDir); err != nil {
			return 0, 0, 0, recovered, fmt.Errorf("sync recovered disk reservations: %w", err)
		}
	}
	return reservedBytes, reservedInodes, active, recovered, nil
}

func decodeReservationRecord(file *os.File) (diskReservationRecord, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return diskReservationRecord{}, err
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxReservationRecordBytes))
	decoder.DisallowUnknownFields()
	var record diskReservationRecord
	if err := decoder.Decode(&record); err != nil {
		return diskReservationRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return diskReservationRecord{}, errors.New("reservation record contains trailing JSON")
		}
		return diskReservationRecord{}, err
	}
	if record.Schema != diskReservationSchema || strings.TrimSpace(record.ID) == "" || record.CreatedAt.IsZero() {
		return diskReservationRecord{}, errors.New("reservation record is invalid")
	}
	return record, nil
}

func (budget *DiskBudget) withHostLock(ctx context.Context, operation func() error) (resultErr error) {
	if budget == nil || budget.lockPath == "" {
		return errors.New("disk budget is not initialized")
	}
	if ctx == nil {
		return errors.New("disk budget context is required")
	}
	lockFile, err := os.OpenFile(budget.lockPath, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return fmt.Errorf("open host disk budget lock: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, lockFile.Close())
	}()
	pollInterval := budget.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultFlockPollInterval
	}
	if err := flockContext(ctx, lockFile, syscall.LOCK_EX, pollInterval); err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN))
	}()
	return operation()
}

// ReleaseContext atomically removes the reservation under the host-wide lock.
func (reservation *DiskReservation) ReleaseContext(ctx context.Context) error {
	if reservation == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("disk reservation release context is required")
	}
	reservation.mutex.Lock()
	defer reservation.mutex.Unlock()
	if reservation.released {
		return reservation.releaseErr
	}
	if reservation.budget == nil || reservation.file == nil {
		reservation.released = true
		return nil
	}
	operationStarted := false
	releaseErr := reservation.budget.withHostLock(ctx, func() error {
		operationStarted = true
		unlockErr := syscall.Flock(int(reservation.file.Fd()), syscall.LOCK_UN)
		closeErr := reservation.file.Close()
		removeErr := os.Remove(reservation.path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		syncErr := syncDirectory(reservation.budget.reservationDir)
		return errors.Join(unlockErr, closeErr, removeErr, syncErr)
	})
	if operationStarted {
		reservation.released = true
		reservation.releaseErr = releaseErr
	}
	return releaseErr
}

// Release uses a non-cancellable context for io.Closer-style cleanup.
func (reservation *DiskReservation) Release() error {
	return reservation.ReleaseContext(context.Background())
}

func (reservation *DiskReservation) Close() error {
	return reservation.Release()
}

func (reservation *DiskReservation) Path() string {
	if reservation == nil {
		return ""
	}
	return reservation.path
}

func (reservation *DiskReservation) Projection() DiskProjection {
	if reservation == nil {
		return DiskProjection{}
	}
	return reservation.projection
}

func allocatedDiskUsage(roots []string) (bytes, inodes uint64, resultErr error) {
	seen := make(map[diskFileIdentity]struct{})
	orderedRoots := append([]string(nil), roots...)
	sort.Strings(orderedRoots)
	for _, root := range orderedRoots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, os.ErrNotExist) && path == root {
					return nil
				}
				return walkErr
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("read Linux stat for %q", path)
			}
			identity := diskFileIdentity{device: stat.Dev, inode: stat.Ino}
			if _, exists := seen[identity]; exists {
				return nil
			}
			seen[identity] = struct{}{}
			if stat.Blocks < 0 {
				return fmt.Errorf("negative allocated block count for %q", path)
			}
			blocks := uint64(stat.Blocks)
			if blocks > math.MaxUint64/512 {
				return fmt.Errorf("allocated block count overflow for %q", path)
			}
			allocated := blocks * 512
			if math.MaxUint64-bytes < allocated {
				return errors.New("allocated disk usage overflow")
			}
			bytes += allocated
			inodes++
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, 0, fmt.Errorf("measure allocated disk usage below %q: %w", root, err)
		}
	}
	return bytes, inodes, nil
}

func filesystemFree(path string) (bytes, inodes uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("stat filesystem capacity: %w", err)
	}
	if stat.Bsize <= 0 {
		return 0, 0, errors.New("filesystem reports a non-positive block size")
	}
	blockSize := uint64(stat.Bsize)
	if stat.Bavail > math.MaxUint64/blockSize {
		return 0, 0, errors.New("filesystem free byte count overflow")
	}
	return stat.Bavail * blockSize, stat.Ffree, nil
}

func addUint64(values ...uint64) (uint64, bool) {
	var result uint64
	for _, value := range values {
		if math.MaxUint64-result < value {
			return 0, true
		}
		result += value
	}
	return result, false
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
