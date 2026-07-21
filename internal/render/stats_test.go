package render

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/store"
)

func TestUsageStatsRendersEmptyThirtyDayWindow(t *testing.T) {
	days := make([]store.DailyUsage, 30)
	for i := range days {
		days[i].Day = time.Date(2026, 7, 1+i, 0, 0, 0, 0, time.UTC)
	}
	days[28].Requests, days[28].Successful = 3, 3
	days[29].Requests, days[29].Successful, days[29].Failed = 5, 4, 1
	path := filepath.Join(t.TempDir(), "stats.png")
	if err := UsageStats(path, days, "ru"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 1000 {
		t.Fatalf("chart too small: %d", info.Size())
	}
}
