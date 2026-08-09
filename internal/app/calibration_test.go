package app

import (
	"reflect"
	"testing"

	"bot_astrosferum/internal/config"
	"bot_astrosferum/internal/forecast"
)

func TestDefaultOverallCalibrationMatchesScientificDefault(t *testing.T) {
	got := OverallCalibration(config.Defaults().Algorithms)
	want := forecast.DefaultOverallIndexCalibration()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped default calibration differs from scientific default:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestAstrodomeScienceCalibrationBindsConfiguredOverallAndCloudRadii(t *testing.T) {
	defaults := config.Defaults().Algorithms
	baseline, err := AstrodomeScienceCalibration(defaults)
	if err != nil {
		t.Fatal(err)
	}
	baselineDigest, err := baseline.Digest()
	if err != nil {
		t.Fatal(err)
	}

	modified := defaults
	modified.OverallCloudWeight = 2.5
	modified.CloudLiquidRadiusMicrometers = 12
	modified.CloudIceRadiusMicrometers = 30
	custom, err := AstrodomeScienceCalibration(modified)
	if err != nil {
		t.Fatal(err)
	}
	customDigest, err := custom.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if custom.Overall.CloudWeight != 2.5 ||
		custom.LiquidEffectiveRadiusM != modified.CloudLiquidRadiusMicrometers*1e-6 ||
		custom.IceEffectiveRadiusM != modified.CloudIceRadiusMicrometers*1e-6 ||
		customDigest == baselineDigest {
		t.Fatalf("custom Astrodome calibration was not bound: %+v, %s == %s", custom, customDigest, baselineDigest)
	}
}
