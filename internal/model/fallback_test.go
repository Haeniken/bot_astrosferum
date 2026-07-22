package model

import (
	"context"
	"testing"

	"bot_astrosferum/internal/forecast"
)

type taggedStore string

func (store taggedStore) Vertical(context.Context, forecast.Location) (forecast.VerticalSeries, error) {
	return forecast.VerticalSeries{Provider: string(store)}, nil
}
func (store taggedStore) Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error) {
	return forecast.SurfaceSeries{Provider: string(store)}, nil
}
func (store taggedStore) Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error) {
	return forecast.CloudSeries{Provider: string(store)}, nil
}

func TestCoverageFallbackRoutesOutsidePrimaryDomain(t *testing.T) {
	router := CoverageFallback{
		PrimaryCoverage: Coverage{MinLat: 29.5, MaxLat: 70.5, MinLon: -23.5, MaxLon: 62.5},
		Primary:         taggedStore("icon-eu"), Fallback: taggedStore("icon-global"),
	}
	inside, _ := forecast.NewLocation(59.9, 30.3, "Europe/Moscow")
	outside, _ := forecast.NewLocation(60, 90, "Asia/Krasnoyarsk")
	got, err := router.Vertical(context.Background(), inside)
	if err != nil || got.Provider != "icon-eu" {
		t.Fatalf("inside provider = %q, err=%v", got.Provider, err)
	}
	got, err = router.Vertical(context.Background(), outside)
	if err != nil || got.Provider != "icon-global" {
		t.Fatalf("outside provider = %q, err=%v", got.Provider, err)
	}
}
