package forecast

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAstrodomePreparedScienceFrameMatchesDirectReconstructionExactly(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Hour)
	validAt := start.Add(time.Hour)
	volume := newAstrodomeTestVolume([]time.Time{start, end})
	for supportIndex, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for level := range column.Frames[0].FullLevels {
			left := &column.Frames[0].FullLevels[level]
			right := &column.Frames[1].FullLevels[level]
			left.PressurePa += float64(30*supportIndex + level)
			right.PressurePa += float64(900 + 60*supportIndex + 2*level)
			left.TemperatureK += float64(supportIndex + level)
			right.TemperatureK += float64(5 + 2*supportIndex + level)
			left.SpecificHumidityKgKg += float64(supportIndex+level) * 1e-5
			right.SpecificHumidityKgKg += float64(4+supportIndex+level) * 1e-5
			left.CloudLiquidKgKg += float64(supportIndex+level) * 1e-7
			right.CloudLiquidKgKg += float64(3+supportIndex+level) * 1e-7
			left.CloudIceKgKg += float64(supportIndex+level) * 1e-8
			right.CloudIceKgKg += float64(2+supportIndex+level) * 1e-8
			left.CloudFraction = 0.1 + float64(supportIndex+level)*0.01
			right.CloudFraction = 0.2 + float64(supportIndex+level)*0.01
			left.EastwardWindMS += float64(supportIndex + level)
			right.EastwardWindMS += float64(3 + supportIndex + level)
			left.NorthwardWindMS -= float64(supportIndex + level)
			right.NorthwardWindMS -= float64(2 + supportIndex + level)
		}
		for level := range column.Frames[0].HalfLevels {
			left := &column.Frames[0].HalfLevels[level]
			right := &column.Frames[1].HalfLevels[level]
			left.TKEJkg += float64(supportIndex+level) * 0.01
			right.TKEJkg += float64(3+supportIndex+level) * 0.01
			left.VerticalWindMS += float64(supportIndex+level) * 0.001
			right.VerticalWindMS += float64(2+supportIndex+level) * 0.001
		}
		column.Frames[0].Surface.SurfacePressurePa += float64(20 * supportIndex)
		column.Frames[1].Surface.SurfacePressurePa += float64(300 + 10*supportIndex)
		column.Frames[0].Surface.Temperature2MK += float64(supportIndex)
		column.Frames[1].Surface.Temperature2MK += float64(3 + supportIndex)
		column.Frames[0].Surface.SpecificHumidity2MKgKg += float64(supportIndex) * 1e-5
		column.Frames[1].Surface.SpecificHumidity2MKgKg += float64(3+supportIndex) * 1e-5
		volume.columns[support.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := NewAstrodomePreparedScienceFrame(reconstructor, validAt)
	if err != nil {
		t.Fatal(err)
	}
	location := Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}
	for _, heightM := range []float64{1000, 1251, 1500, 1999, 2000, 2500, 3000} {
		query := AstrodomeReconstructionQuery{ValidAt: validAt, Location: location, HeightM: heightM}
		want, directErr := reconstructor.Reconstruct(context.Background(), query)
		if directErr != nil {
			t.Fatalf("direct reconstruction at %.3f m: %v", heightM, directErr)
		}
		got, preparedErr := prepared.Reconstruct(context.Background(), query)
		if preparedErr != nil {
			t.Fatalf("prepared reconstruction at %.3f m: %v", heightM, preparedErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prepared reconstruction at %.3f m differs:\n got: %#v\nwant: %#v", heightM, got, want)
		}
	}
}

func TestAstrodomePreparedScienceFrameRejectsAnotherHour(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	reconstructor, err := NewAstrodomePrimitiveReconstructor(newAstrodomeTestVolume([]time.Time{validAt}))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := NewAstrodomePreparedScienceFrame(reconstructor, validAt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepared.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt.Add(time.Hour),
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}, HeightM: 2000,
	})
	if err == nil {
		t.Fatal("prepared science frame served another forecast hour")
	}
}
