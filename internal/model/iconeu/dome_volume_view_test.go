package iconeu

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestDomeCachedColumnViewDoesNotRestorePerQuadratureDeepClone(t *testing.T) {
	root := t.TempDir()
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), writeDomeVolumePublication(t, root),
		&domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	stencil, err := volume.HorizontalStencil(context.Background(), forecast.Location{
		Latitude: grid.MinLat + 2.5*grid.Increment, Longitude: grid.MinLon + 3.5*grid.Increment,
	})
	if err != nil {
		t.Fatal(err)
	}
	columnID := stencil.Supports[0].ColumnID
	publicCopy, err := volume.Column(context.Background(), columnID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := volume.domeCachedColumnView(context.Background(), columnID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := volume.domeCachedColumnView(context.Background(), columnID)
	if err != nil {
		t.Fatal(err)
	}
	if &first.Frames[0].FullLevels[0] != &second.Frames[0].FullLevels[0] {
		t.Fatal("internal immutable view deep-cloned its native arrays")
	}
	if &first.Frames[0].FullLevels[0] == &publicCopy.Frames[0].FullLevels[0] {
		t.Fatal("public Column stopped returning a defensive deep copy")
	}
	allocations := testing.AllocsPerRun(100, func() {
		if _, viewErr := volume.domeCachedColumnView(context.Background(), columnID); viewErr != nil {
			panic(viewErr)
		}
	})
	if allocations > 1 {
		t.Fatalf("cached immutable view allocates %.1f objects per lookup", allocations)
	}
}

func TestDomePinnedColumnViewBypassesMutableLRULock(t *testing.T) {
	root := t.TempDir()
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), writeDomeVolumePublication(t, root),
		&domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	stencil, err := volume.HorizontalStencil(context.Background(), forecast.Location{
		Latitude: grid.MinLat + 2.5*grid.Increment, Longitude: grid.MinLon + 3.5*grid.Increment,
	})
	if err != nil {
		t.Fatal(err)
	}
	columnID := stencil.Supports[0].ColumnID
	if _, err = volume.Column(context.Background(), columnID); err != nil {
		t.Fatal(err)
	}
	volume.mu.Lock()
	cached := volume.cache[columnID].column
	volume.mu.Unlock()
	volume.pinnedColumns.Store(&domePinnedColumnSnapshot{
		columns: map[string]forecast.AstrodomePrimitiveColumn{columnID: cached},
	})

	done := make(chan error, 1)
	volume.mu.Lock()
	go func() {
		_, lookupErr := volume.domeCachedColumnView(context.Background(), columnID)
		done <- lookupErr
	}()
	select {
	case lookupErr := <-done:
		volume.mu.Unlock()
		if lookupErr != nil {
			t.Fatal(lookupErr)
		}
	case <-time.After(time.Second):
		volume.mu.Unlock()
		t.Fatal("pinned immutable lookup waited for the mutable LRU lock")
	}
}
