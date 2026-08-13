package iconeu

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"

	"bot_astrosferum/internal/forecast"
)

var horizonRunIDPattern = regexp.MustCompile(`^[0-9]{10}$`)

// HorizonStore is deliberately separate from CachedStore. One request reads
// four immutable ICON-EU bundles at many locations in batches, while the
// ordinary store reads many times at one location. Separate command limits
// prevent this heavy spatial path from consuming ordinary forecast slots.
type HorizonStore struct {
	dataRoot    string
	extractor   batchExtractor
	workers     int
	loadCurrent func(string) (LoadedManifest, error)
	loadRun     func(string, string) (LoadedManifest, error)
	logf        func(string, ...any)
}

func NewHorizonStore(dataRoot, tempRoot string, cdoWorkers int, logf func(string, ...any)) *HorizonStore {
	if cdoWorkers < 1 {
		cdoWorkers = 1
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	runner := &limitedRunner{runner: execRunner{}, semaphore: make(chan struct{}, cdoWorkers)}
	return &HorizonStore{
		dataRoot:    dataRoot,
		extractor:   batchExtractor{runner: runner, tempRoot: tempRoot},
		workers:     cdoWorkers,
		loadCurrent: LoadCurrent,
		loadRun:     loadHorizonRun,
		logf:        logf,
	}
}

func loadHorizonRun(dataRoot, runID string) (LoadedManifest, error) {
	if !horizonRunIDPattern.MatchString(runID) {
		return LoadedManifest{}, fmt.Errorf("invalid ICON-EU horizon run ID")
	}
	manifest, err := LoadManifest(filepath.Join(dataRoot, "models", "icon-eu", "runs", runID, "manifest.json"))
	if err != nil {
		return LoadedManifest{}, err
	}
	if manifest.RunID != runID {
		return LoadedManifest{}, fmt.Errorf("ICON-EU horizon run identity mismatch")
	}
	return manifest, nil
}

func (store *HorizonStore) Supports(plan forecast.HorizonPlan) bool {
	return plan.AlgorithmVersion == forecast.HorizonStraightReferenceAlgorithmVersion &&
		forecast.HorizonFootprintCovered(plan, Coverage().Contains)
}

func (store *HorizonStore) CurrentRunID() (string, error) {
	manifest, err := store.loadCurrent(store.dataRoot)
	if err != nil {
		return "", err
	}
	return manifest.RunID, nil
}

func canonicalHorizonPoints(manifest LoadedManifest, locations []forecast.Location) ([]batchPoint, []int) {
	increment := manifest.Grid.Increment
	if increment <= 0 {
		increment = 0.0625
	}
	maxLatitudeIndex := int(math.Round((manifest.Grid.MaxLat - manifest.Grid.MinLat) / increment))
	maxLongitudeIndex := int(math.Round((manifest.Grid.MaxLon - manifest.Grid.MinLon) / increment))
	indices := make(map[string]int, len(locations))
	points := make([]batchPoint, 0, len(locations))
	lookup := make([]int, len(locations))
	for locationIndex, location := range locations {
		latitudeIndex := int(math.Round((location.Latitude - manifest.Grid.MinLat) / increment))
		longitudeIndex := int(math.Round((location.Longitude - manifest.Grid.MinLon) / increment))
		latitudeIndex = max(0, min(maxLatitudeIndex, latitudeIndex))
		longitudeIndex = max(0, min(maxLongitudeIndex, longitudeIndex))
		cellID := fmt.Sprintf("%d/%d", latitudeIndex, longitudeIndex)
		index, exists := indices[cellID]
		if !exists {
			index = len(points)
			indices[cellID] = index
			points = append(points, batchPoint{
				Latitude:  manifest.Grid.MinLat + float64(latitudeIndex)*increment,
				Longitude: manifest.Grid.MinLon + float64(longitudeIndex)*increment,
			})
		}
		lookup[locationIndex] = index
	}
	return points, lookup
}
