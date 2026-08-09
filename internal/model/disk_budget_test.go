//go:build linux

package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

var crashHelperDiskReservation *DiskReservation

func TestSelectStorageProfileExactGate(t *testing.T) {
	const capBytes = uint64(100)
	tests := []struct {
		name          string
		dense, sparse uint64
		want          StorageProfile
	}{
		{name: "dense below cap", dense: 99, sparse: 1, want: StorageProfileDense},
		{name: "dense exactly cap", dense: 100, sparse: 1, want: StorageProfileDense},
		{name: "sparse only after dense exceeds", dense: 101, sparse: 100, want: StorageProfileSparse},
		{name: "both exceed", dense: 101, sparse: 101, want: StorageProfileUnavailable},
		{name: "invalid cap", dense: 1, sparse: 1, want: StorageProfileUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capForTest := capBytes
			if test.name == "invalid cap" {
				capForTest = 0
			}
			if got := SelectStorageProfile(test.dense, test.sparse, capForTest); got != test.want {
				t.Fatalf("SelectStorageProfile(%d, %d, %d) = %q, want %q", test.dense, test.sparse, capForTest, got, test.want)
			}
		})
	}
	if got := SelectStorageProfile(HardDiskProjectCapBytes+1, 1, HardDiskProjectCapBytes+1); got != StorageProfileUnavailable {
		t.Fatalf("cap above hard maximum selected %q", got)
	}
}

func TestAllocatedDiskUsageUsesBlocksAndDeduplicatesHardlinks(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "model.grib2")
	if err := os.WriteFile(first, make([]byte, 128<<10), 0o640); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(root, "leased.grib2")
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	bytes, inodes, err := allocatedDiskUsage([]string{first, second, first})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(first)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	wantBytes := uint64(stat.Blocks) * 512
	if bytes != wantBytes || inodes != 1 {
		t.Fatalf("allocated usage = %d bytes/%d inodes, want %d/1", bytes, inodes, wantBytes)
	}
}

func TestDiskBudgetCountsAllRootClassesOnce(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 5)
	for index := range paths {
		paths[index] = filepath.Join(root, fmt.Sprintf("class-%d.bin", index))
		if err := os.WriteFile(paths[index], make([]byte, 8192), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	budget := newTestDiskBudget(t, root, 1<<30, func(config *DiskBudgetConfig) {
		config.PublishedRoots = []string{paths[0]}
		config.StagingRoots = []string{paths[1]}
		config.LeasedRoots = []string{paths[2], paths[0]}
		config.CacheRoots = []string{paths[3]}
		config.TemporaryRoots = []string{paths[4]}
	})
	snapshot, err := budget.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, wantInodes, err := allocatedDiskUsage(paths)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AllocatedBytes != want || snapshot.AllocatedInodes != wantInodes {
		t.Fatalf("snapshot usage = %d/%d, want %d/%d", snapshot.AllocatedBytes, snapshot.AllocatedInodes, want, wantInodes)
	}
}

func TestDiskBudgetReservationsAreSerializedAcrossInstances(t *testing.T) {
	root := t.TempDir()
	first := newTestDiskBudget(t, root, 8, nil)
	second := newTestDiskBudget(t, root, 8, nil)
	start := make(chan struct{})
	var wait sync.WaitGroup
	var mutex sync.Mutex
	reservations := make([]*DiskReservation, 0, 8)
	var accepted, rejected int
	for index := range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			budget := first
			if index%2 != 0 {
				budget = second
			}
			reservation, err := budget.Reserve(context.Background(), fmt.Sprintf("job-%d", index), DiskProjection{Bytes: 1, Inodes: 1})
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				accepted++
				reservations = append(reservations, reservation)
				return
			}
			if !errors.Is(err, ErrDiskProjectCap) {
				t.Errorf("Reserve error = %v", err)
			}
			rejected++
		}()
	}
	close(start)
	wait.Wait()
	if accepted != 8 || rejected != 16 {
		t.Fatalf("accepted/rejected = %d/%d, want 8/16", accepted, rejected)
	}
	snapshot, err := first.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReservedBytes != 8 || snapshot.ReservedInodes != 8 || snapshot.ActiveReservations != 8 {
		t.Fatalf("active reservations = %+v", snapshot)
	}
	for _, reservation := range reservations {
		if err := reservation.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiskBudgetProfileDoesNotUseSparseForFreeSpaceFailure(t *testing.T) {
	root := t.TempDir()
	budget := newTestDiskBudget(t, root, 100, func(config *DiskBudgetConfig) {
		config.MinFreeBytes = ^uint64(0)
	})
	reservation, profile, err := budget.ReserveProfile(
		context.Background(), "free-space", DiskProjection{Bytes: 90}, DiskProjection{Bytes: 1},
	)
	if reservation != nil || profile != StorageProfileUnavailable || !errors.Is(err, ErrDiskFreeBytes) {
		t.Fatalf("ReserveProfile = reservation %v, profile %q, err %v", reservation, profile, err)
	}
}

func TestDiskBudgetUsesSparseOnlyWhenDenseExceedsCap(t *testing.T) {
	root := t.TempDir()
	budget := newTestDiskBudget(t, root, 100, nil)
	reservation, profile, err := budget.ReserveProfile(
		context.Background(), "profile", DiskProjection{Bytes: 101, Inodes: 5}, DiskProjection{Bytes: 80, Inodes: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reservation.Release(); err != nil {
			t.Errorf("release disk reservation: %v", err)
		}
	})
	if profile != StorageProfileSparse || reservation.Projection() != (DiskProjection{Bytes: 80, Inodes: 2}) {
		t.Fatalf("profile/projection = %q/%+v", profile, reservation.Projection())
	}
}

func TestDiskBudgetChecksFreeInodesIndependently(t *testing.T) {
	root := t.TempDir()
	budget := newTestDiskBudget(t, root, 100, func(config *DiskBudgetConfig) {
		config.MinFreeInodes = ^uint64(0)
	})
	reservation, err := budget.Reserve(context.Background(), "inodes", DiskProjection{Bytes: 1, Inodes: 1})
	if reservation != nil || !errors.Is(err, ErrDiskFreeInodes) {
		t.Fatalf("Reserve = reservation %v, err %v", reservation, err)
	}
}

func TestDiskBudgetCrashStaleReservationRecovery(t *testing.T) {
	root := t.TempDir()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDiskBudgetCrashHelper$")
	command.Env = append(os.Environ(), "ASTRO_TEST_DISK_BUDGET_ROOT="+root)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		_ = command.Process.Kill()
		t.Fatal(err)
	}
	if line != "reserved\n" {
		_ = command.Process.Kill()
		t.Fatalf("helper readiness = %q", line)
	}

	budget := newTestDiskBudget(t, root, 100, nil)
	if reservation, err := budget.Reserve(context.Background(), "blocked", DiskProjection{Bytes: 80}); reservation != nil || !errors.Is(err, ErrDiskProjectCap) {
		t.Fatalf("live reservation was ignored: reservation %v, err %v", reservation, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	recovered, err := budget.RecoverStaleReservations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered reservations = %d, want 1", recovered)
	}
	reservation, err := budget.Reserve(context.Background(), "after-crash", DiskProjection{Bytes: 80})
	if err != nil {
		t.Fatalf("reserve after crash recovery: %v", err)
	}
	if err := reservation.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestDiskBudgetRejectsCapAboveHardMaximum(t *testing.T) {
	root := t.TempDir()
	_, err := NewDiskBudget(DiskBudgetConfig{
		LockPath: filepath.Join(root, "state", "budget.lock"), ReservationDir: filepath.Join(root, "state", "reservations"),
		FilesystemPath: root, ProjectCapBytes: HardDiskProjectCapBytes + 1,
	})
	if err == nil {
		t.Fatal("cap above hard maximum was accepted")
	}
}

func TestDiskBudgetCrashHelper(t *testing.T) {
	root := os.Getenv("ASTRO_TEST_DISK_BUDGET_ROOT")
	if root == "" {
		return
	}
	budget := newHelperDiskBudget(t, root, 100)
	reservation, err := budget.Reserve(context.Background(), "crashed", DiskProjection{Bytes: 30})
	if err != nil {
		t.Fatal(err)
	}
	crashHelperDiskReservation = reservation
	_, _ = fmt.Fprintln(os.Stdout, "reserved")
	select {}
}

func newTestDiskBudget(t *testing.T, root string, capBytes uint64, mutate func(*DiskBudgetConfig)) *DiskBudget {
	t.Helper()
	config := testDiskBudgetConfig(root, capBytes)
	if mutate != nil {
		mutate(&config)
	}
	budget, err := NewDiskBudget(config)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func newHelperDiskBudget(t *testing.T, root string, capBytes uint64) *DiskBudget {
	t.Helper()
	budget, err := NewDiskBudget(testDiskBudgetConfig(root, capBytes))
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func testDiskBudgetConfig(root string, capBytes uint64) DiskBudgetConfig {
	return DiskBudgetConfig{
		LockPath: filepath.Join(root, "state", "budget.lock"), ReservationDir: filepath.Join(root, "state", "reservations"),
		FilesystemPath: root, ProjectCapBytes: capBytes,
	}
}

func TestDiskBudgetHostLockHonorsContext(t *testing.T) {
	root := t.TempDir()
	budget := newTestDiskBudget(t, root, 100, nil)
	file, err := os.OpenFile(budget.lockPath, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close host lock file: %v", err)
		}
	})
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			t.Errorf("unlock host lock file: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := budget.Snapshot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Snapshot error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("host lock cancellation took %s", elapsed)
	}
}

func TestDiskReservationReleaseCanRetryAfterCancelledLockWait(t *testing.T) {
	root := t.TempDir()
	budget := newTestDiskBudget(t, root, 100, nil)
	reservation, err := budget.Reserve(context.Background(), "release-retry", DiskProjection{Bytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(budget.lockPath, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := reservation.ReleaseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReleaseContext error = %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reservation.Release(); err != nil {
		t.Fatalf("retry Release: %v", err)
	}
	if _, err := os.Stat(reservation.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reservation record remains after retry: %v", err)
	}
}
