package iconeu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionCDONativeRemapProof(t *testing.T) {
	if os.Getenv("ASTRO_CDO_NATIVE_REMAP_PROOF") != "production-cdo-2.5-native-v1" {
		t.Skip("set ASTRO_CDO_NATIVE_REMAP_PROOF=production-cdo-2.5-native-v1 for the isolated real-CDO proof")
	}

	root := t.TempDir()
	cdlPath := filepath.Join(root, "native-index.cdl")
	sourcePath := filepath.Join(root, "native-index.nc")
	gridPath := filepath.Join(root, "targets.grid")
	weightsPath := filepath.Join(root, "weights.nc")
	cdl := `netcdf native_index {
dimensions:
  lon = 3 ;
  lat = 3 ;
  time = 1 ;
variables:
  double lon(lon) ;
    lon:units = "degrees_east" ;
  double lat(lat) ;
    lat:units = "degrees_north" ;
  double time(time) ;
    time:units = "hours since 2026-01-01 00:00:00" ;
  double native_index(time, lat, lon) ;
data:
  lon = 36, 37, 38 ;
  lat = 54, 55, 56 ;
  time = 0 ;
  native_index = 1, 2, 3, 4, 5, 6, 7, 8, 9 ;
}
`
	if err := os.WriteFile(cdlPath, []byte(cdl), 0o600); err != nil {
		t.Fatal(err)
	}
	points := []batchPoint{
		{Latitude: 55, Longitude: 37},
		{Latitude: 54, Longitude: 36},
		{Latitude: 56, Longitude: 38},
		{Latitude: 56, Longitude: 36},
	}
	if err := writeBatchGrid(gridPath, points); err != nil {
		t.Fatal(err)
	}
	runner := execRunner{}
	if output, err := runner.CombinedOutput(context.Background(), "ncgen", "-o", sourcePath, cdlPath); err != nil {
		t.Fatalf("ncgen manufactured native grid: %v: %s", err, output)
	}
	if output, err := runner.CombinedOutput(
		context.Background(), "cdo", "-s", "gennn,"+gridPath, "-selname,native_index", sourcePath, weightsPath,
	); err != nil {
		t.Fatalf("generate manufactured native weights: %v: %s", err, output)
	}
	sourceGrid := domeNativeSourceGrid{
		gridType: "regular_ll", ni: 3, nj: 3,
		firstLat: 54, firstLon: 36, lastLat: 56, lastLon: 38,
		iIncrement: 1, jIncrement: 1, iScansNegatively: 0, jScansPositively: 1,
	}
	proof, err := proveDomeRemapPlan(context.Background(), runner, weightsPath, sourceGrid, points)
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.validateTargets(points); err != nil {
		t.Fatal(err)
	}
	output, err := runner.CombinedOutput(
		context.Background(),
		"cdo", "-s", "--precision", "12",
		"-outputtab,name:16,lev:12,lon:16,lat:16,value:24",
		"-remap,"+gridPath+","+weightsPath,
		sourcePath,
	)
	if err != nil {
		t.Fatalf("remap manufactured native indices: %v", err)
	}
	values, err := parseBatchOutput(output, points, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{5, 1, 9, 7}
	for index := range points {
		if len(values[index]) != 1 {
			t.Fatalf("target %d returned %d manufactured fields", index, len(values[index]))
		}
		for key, got := range values[index] {
			if key.name != "native_index" || got != want[index] {
				t.Fatalf("target %d remapped %s/%.10g = %.17g, want native_index %.17g (%s)",
					index, key.name, key.level, got, want[index], fmt.Sprint(points[index]))
			}
		}
	}
}
