package model

import (
	"context"

	"bot_astrosferum/internal/forecast"
)

type ForecastStore interface {
	Vertical(context.Context, forecast.Location) (forecast.VerticalSeries, error)
	Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error)
	Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error)
}

// CoverageFallback keeps provider selection deliberately small: ICON-EU is
// used inside its published domain and ICON Global everywhere else.  Each
// provider owns its download, extraction, and cache lifecycle.
type CoverageFallback struct {
	PrimaryCoverage Coverage
	Primary         ForecastStore
	Fallback        ForecastStore
}

func (router CoverageFallback) store(location forecast.Location) ForecastStore {
	if router.Primary != nil && router.PrimaryCoverage.Contains(location) {
		return router.Primary
	}
	return router.Fallback
}

func (router CoverageFallback) Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error) {
	return router.store(location).Vertical(ctx, location)
}

func (router CoverageFallback) Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error) {
	return router.store(location).Surface(ctx, location)
}

func (router CoverageFallback) Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error) {
	return router.store(location).Cloud(ctx, location)
}
