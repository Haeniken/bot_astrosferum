package forecast

import (
	"math"
	"testing"
)

func TestKastenYoungRelativeOpticalAirmassReferenceValues(t *testing.T) {
	tests := []struct {
		altitudeDegrees float64
		want            float64
	}{
		{altitudeDegrees: 90, want: 0.9997119918558381},
		{altitudeDegrees: 30, want: 1.9942928525292494},
		{altitudeDegrees: 0, want: 37.91960837783621},
	}
	for _, test := range tests {
		got, err := KastenYoungRelativeOpticalAirmass(test.altitudeDegrees)
		if err != nil {
			t.Fatalf("altitude %.1f degrees: %v", test.altitudeDegrees, err)
		}
		if math.Abs(got-test.want) > 1e-12 {
			t.Fatalf("altitude %.1f degrees: got %.16g, want %.16g", test.altitudeDegrees, got, test.want)
		}
	}
}

func TestKastenYoungRelativeOpticalAirmassRejectsOutsideDomain(t *testing.T) {
	for _, altitude := range []float64{-0.001, 90.001, math.NaN(), math.Inf(1)} {
		if _, err := KastenYoungRelativeOpticalAirmass(altitude); err == nil {
			t.Fatalf("altitude %v unexpectedly accepted", altitude)
		}
	}
}

func TestScaleZenithSeeingFWHMArcsec(t *testing.T) {
	got, err := ScaleZenithSeeingFWHMArcsec(1, 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 2^(3/5) from air mass and 2^(-1/5) from wavelength leave 2^(2/5).
	want := math.Pow(2, 2.0/5.0)
	if math.Abs(got-want) > 1e-14 {
		t.Fatalf("scaled seeing = %.16g, want %.16g", got, want)
	}

	identity, err := ScaleZenithSeeingFWHMArcsec(0.8, 500, 1)
	if err != nil {
		t.Fatal(err)
	}
	if identity != 0.8 {
		t.Fatalf("reference-condition seeing = %.16g, want 0.8", identity)
	}
}

func TestScaleZenithSeeingFWHMArcsecRejectsInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		seeing, wavelength, airmass float64
	}{
		{0, 500, 1},
		{-1, 500, 1},
		{1, 0, 1},
		{1, -500, 1},
		{1, 500, 0},
		{1, 500, -1},
		{math.NaN(), 500, 1},
		{1, math.Inf(1), 1},
		{1, 500, math.NaN()},
	} {
		if _, err := ScaleZenithSeeingFWHMArcsec(test.seeing, test.wavelength, test.airmass); err == nil {
			t.Fatalf("inputs %+v unexpectedly accepted", test)
		}
	}
}

func TestDeliveredImageQualityFWHMArcsec(t *testing.T) {
	got, err := DeliveredImageQualityFWHMArcsec(3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Fatalf("delivered FWHM = %g, want 5", got)
	}

	identity, err := DeliveredImageQualityFWHMArcsec(0.7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if identity != 0.7 {
		t.Fatalf("zero non-atmospheric term changed FWHM: got %g", identity)
	}
}

func TestDeliveredImageQualityFWHMArcsecRejectsInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		atmospheric, nonAtmospheric float64
	}{
		{0, 0},
		{-1, 0},
		{1, -1},
		{math.NaN(), 1},
		{1, math.Inf(1)},
	} {
		if _, err := DeliveredImageQualityFWHMArcsec(test.atmospheric, test.nonAtmospheric); err == nil {
			t.Fatalf("inputs %+v unexpectedly accepted", test)
		}
	}
}
