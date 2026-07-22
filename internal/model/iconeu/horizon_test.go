package iconeu

import (
	"testing"

	"bot_astrosferum/internal/forecast"
)

func TestCanonicalHorizonPointsDeduplicatesModelCells(t *testing.T) {
	manifest := LoadedManifest{Manifest: Manifest{Grid: Coverage()}}
	locations := []forecast.Location{
		{Latitude: 59.93860, Longitude: 30.31410},
		{Latitude: 59.93861, Longitude: 30.31411},
		{Latitude: 60.1, Longitude: 30.5},
	}
	points, lookup := canonicalHorizonPoints(manifest, locations)
	if len(points) != 2 || lookup[0] != lookup[1] || lookup[2] == lookup[0] {
		t.Fatalf("points=%v lookup=%v", points, lookup)
	}
}

func TestHorizonStoreRejectsGlobalOrBoundaryFootprint(t *testing.T) {
	store := NewHorizonStore("/unused", t.TempDir(), 1, nil)
	inside, err := forecast.NewLocation(59.9386, 30.3141, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	insidePlan, err := forecast.NewHorizonPlan(inside, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Supports(insidePlan) {
		t.Fatal("central ICON-EU footprint was rejected")
	}
	boundary, err := forecast.NewLocation(70.49, 30, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	boundaryPlan, err := forecast.NewHorizonPlan(boundary, 10)
	if err != nil {
		t.Fatal(err)
	}
	if store.Supports(boundaryPlan) {
		t.Fatal("footprint crossing the ICON-EU boundary was accepted")
	}
	global, err := forecast.NewLocation(0, 0, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	globalPlan, err := forecast.NewHorizonPlan(global, 10)
	if err != nil {
		t.Fatal(err)
	}
	if store.Supports(globalPlan) {
		t.Fatal("ICON Global location was accepted")
	}
}
