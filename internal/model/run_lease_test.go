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
	"regexp"
	"testing"
	"time"
)

var crashHelperRunLease *RunLease

func TestRunLeaseSharedReadersBlockCleaner(t *testing.T) {
	manager, err := NewRunLeaseManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.AcquireShared(context.Background(), "icon-eu", "2026072800", "digest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first run lease: %v", err)
		}
	})
	second, err := manager.AcquireShared(context.Background(), "icon-eu", "2026072800", "digest")
	if err != nil {
		t.Fatal(err)
	}

	if lease, acquired, err := manager.TryExclusive("icon-eu", "2026072800", "digest"); err != nil {
		t.Fatal(err)
	} else if acquired || lease != nil {
		t.Fatal("cleaner acquired an exclusively leased run")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if lease, acquired, err := manager.TryExclusive("icon-eu", "2026072800", "digest"); err != nil {
		t.Fatal(err)
	} else if acquired || lease != nil {
		t.Fatal("cleaner acquired while one shared reader remained")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	exclusive, acquired, err := manager.TryExclusive("icon-eu", "2026072800", "digest")
	if err != nil {
		t.Fatal(err)
	}
	if !acquired || exclusive == nil {
		t.Fatal("cleaner did not acquire an unpinned run")
	}
	if err := exclusive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := exclusive.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}

func TestRunLeaseWaitHonorsContext(t *testing.T) {
	manager, err := NewRunLeaseManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	held, err := manager.AcquireShared(context.Background(), "icon-eu", "run", "digest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := held.Close(); err != nil {
			t.Errorf("close held run lease: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := manager.AcquireExclusive(ctx, "icon-eu", "run", "digest"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireExclusive error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("context cancellation took %s", elapsed)
	}
}

func TestRunLeasePathCannotTraverseRoot(t *testing.T) {
	root := t.TempDir()
	manager, err := NewRunLeaseManager(root)
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.LockPath("../../tmp", "/absolute/run", "../digest")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != root {
		t.Fatalf("lock escaped root: %q", path)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}\.lock$`).MatchString(filepath.Base(path)) {
		t.Fatalf("unexpected safe lock name %q", filepath.Base(path))
	}
	other, err := manager.LockPath("../../tmp", "/absolute/run", "../digest-2")
	if err != nil {
		t.Fatal(err)
	}
	if other == path {
		t.Fatal("distinct identities collided")
	}
}

func TestRunLeaseProcessCrashReleasesKernelLock(t *testing.T) {
	root := t.TempDir()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRunLeaseCrashHelper$")
	command.Env = append(os.Environ(), "ASTRO_TEST_RUN_LEASE_ROOT="+root)
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
	if line != "locked\n" {
		_ = command.Process.Kill()
		t.Fatalf("helper readiness = %q", line)
	}

	manager, err := NewRunLeaseManager(root)
	if err != nil {
		t.Fatal(err)
	}
	if lease, acquired, err := manager.TryExclusive("icon-eu", "crash-run", "digest"); err != nil {
		t.Fatal(err)
	} else if acquired || lease != nil {
		t.Fatal("parent acquired the helper's live lease")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := manager.AcquireExclusive(ctx, "icon-eu", "crash-run", "digest")
	if err != nil {
		t.Fatalf("acquire after helper crash: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLeaseCrashHelper(t *testing.T) {
	root := os.Getenv("ASTRO_TEST_RUN_LEASE_ROOT")
	if root == "" {
		return
	}
	manager, err := NewRunLeaseManager(root)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.AcquireExclusive(context.Background(), "icon-eu", "crash-run", "digest")
	if err != nil {
		t.Fatal(err)
	}
	crashHelperRunLease = lease
	_, _ = fmt.Fprintln(os.Stdout, "locked")
	select {}
}
