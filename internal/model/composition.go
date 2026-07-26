package model

import (
	"context"
	"fmt"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model/geoscf"
)

// GEOSCFCompositionStore adapts the source-specific NASA response to the
// provider-neutral forecast contract. It performs no scientific conversion.
type GEOSCFCompositionStore struct {
	Client *geoscf.Client
}

func (store GEOSCFCompositionStore) AtmosphericComposition(ctx context.Context, location forecast.Location, validTimes []time.Time) (forecast.AtmosphericCompositionSeries, error) {
	if store.Client == nil {
		return forecast.AtmosphericCompositionSeries{}, fmt.Errorf("GEOS-CF composition client is required")
	}
	source, err := store.Client.AtmosphericComposition(ctx, location, validTimes)
	if err != nil {
		return forecast.AtmosphericCompositionSeries{}, err
	}
	grid := fmt.Sprintf("NASA GEOS-CF %.4f°", source.Source.GridResolutionDegrees)
	frames := make([]forecast.AtmosphericCompositionFrame, len(source.Frames))
	for index, frame := range source.Frames {
		frames[index] = forecast.AtmosphericCompositionFrame{
			ValidAt:                   frame.ValidTimeUTC,
			AerosolOpticalDepth550:    frame.AOD550,
			TotalColumnOzoneDU:        frame.TotalColumnOzoneDU,
			Provider:                  geoscf.ProviderName,
			RunID:                     source.Source.RunID,
			BaseTime:                  source.Source.RunTimeUTC,
			Grid:                      grid,
			AerosolSpectralAssumption: "Bird-Riordan SPECTRL2 rural Angstrom alpha=1.14",
		}
	}
	return forecast.AtmosphericCompositionSeries{
		Location: location, Provider: geoscf.ProviderName, Product: geoscf.ProductName,
		RunID: source.Source.RunID, BaseTime: source.Source.RunTimeUTC,
		RetrievedAt: source.Source.RetrievedAtUTC, Grid: grid,
		DatasetURL:   source.Source.Provenance.DatasetURL,
		FreshnessAge: source.Source.Freshness.Age,
		Frames:       frames,
	}, nil
}
