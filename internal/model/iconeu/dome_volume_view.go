package iconeu

import (
	"context"
	"fmt"

	"bot_astrosferum/internal/forecast"
)

// domeCachedColumnView returns an internal immutable view. Unlike public
// Column it deliberately does not deep-copy the 81-frame native arrays. The
// value must never leave package-internal calculation code or be mutated.
func (volume *DomeVolume) domeCachedColumnView(
	ctx context.Context,
	columnID string,
) (forecast.AstrodomePrimitiveColumn, error) {
	if err := ctx.Err(); err != nil {
		return forecast.AstrodomePrimitiveColumn{}, err
	}
	if snapshot := volume.pinnedColumns.Load(); snapshot != nil {
		if column, ok := snapshot.columns[columnID]; ok {
			return column, nil
		}
	}
	volume.mu.Lock()
	if cached, ok := volume.cache[columnID]; ok {
		volume.clock++
		cached.used = volume.clock
		volume.cache[columnID] = cached
		volume.mu.Unlock()
		return cached.column, nil
	}
	volume.mu.Unlock()
	// Preserve the existing on-demand guard when no explicit footprint was
	// installed. Column performs extraction and validation, then the immutable
	// cached object is re-read without another defensive copy.
	if _, err := volume.Column(ctx, columnID); err != nil {
		return forecast.AstrodomePrimitiveColumn{}, err
	}
	volume.mu.Lock()
	cached, ok := volume.cache[columnID]
	volume.mu.Unlock()
	if !ok {
		return forecast.AstrodomePrimitiveColumn{}, fmt.Errorf("ICON-EU Astrodome cache omitted column %q", columnID)
	}
	return cached.column, nil
}
