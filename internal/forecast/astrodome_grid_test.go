package forecast

import (
	"math"
	"testing"
	"time"
)

func TestAstrodomeGridProfilesHaveCanonicalOrderAndCardinality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		id               AstrodomeGridProfileID
		wantCount        int
		wantElevations   []float64
		wantAzimuthCount []int
		wantPrefixes     []int
	}{
		{
			name:             "dense",
			id:               AstrodomeGridDenseV1,
			wantCount:        353,
			wantElevations:   []float64{10, 11.343, 13.035, 15.236, 18.220, 20, 22.481, 30, 40, 50, 60, 70, 80},
			wantAzimuthCount: []int{60, 52, 44, 40, 32, 32, 28, 20, 16, 12, 8, 4, 4},
			wantPrefixes:     []int{0, 60, 112, 156, 196, 228, 260, 288, 308, 324, 336, 344, 348},
		},
		{
			name:             "sparse storage",
			id:               AstrodomeGridSparseStorageV1,
			wantCount:        97,
			wantElevations:   []float64{10, 20, 30, 45, 60, 75},
			wantAzimuthCount: []int{16, 16, 16, 16, 16, 16},
			wantPrefixes:     []int{0, 16, 32, 48, 64, 80},
		},
		{
			name:             "production v2",
			id:               AstrodomeGridProductionV2,
			wantCount:        129,
			wantElevations:   []float64{10, 20, 30, 40, 50, 60, 70, 80},
			wantAzimuthCount: []int{16, 16, 16, 16, 16, 16, 16, 16},
			wantPrefixes:     []int{0, 16, 32, 48, 64, 80, 96, 112},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile, err := NewAstrodomeGridProfile(test.id)
			if err != nil {
				t.Fatalf("NewAstrodomeGridProfile: %v", err)
			}
			if err := profile.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := profile.NodeCount(); got != test.wantCount {
				t.Fatalf("NodeCount = %d, want %d", got, test.wantCount)
			}
			if len(profile.Rings) != len(test.wantElevations) {
				t.Fatalf("ring count = %d, want %d", len(profile.Rings), len(test.wantElevations))
			}

			nodes, err := profile.Nodes()
			if err != nil {
				t.Fatalf("Nodes: %v", err)
			}
			if len(nodes) != test.wantCount {
				t.Fatalf("len(Nodes) = %d, want %d", len(nodes), test.wantCount)
			}
			for ringIndex, ring := range profile.Rings {
				if ring.ElevationDegrees != test.wantElevations[ringIndex] || ring.AzimuthCount != test.wantAzimuthCount[ringIndex] {
					t.Fatalf("ring %d = (%g,%d), want (%g,%d)", ringIndex, ring.ElevationDegrees, ring.AzimuthCount,
						test.wantElevations[ringIndex], test.wantAzimuthCount[ringIndex])
				}
				if got, want := ring.AzimuthStepDegrees, 360/float64(ring.AzimuthCount); got != want {
					t.Fatalf("ring %d step = %.17g, want %.17g", ringIndex, got, want)
				}
				prefix := test.wantPrefixes[ringIndex]
				for azimuthIndex := range ring.AzimuthCount {
					node := nodes[prefix+azimuthIndex]
					wantAzimuth := 360 * float64(azimuthIndex) / float64(ring.AzimuthCount)
					if node.Index != prefix+azimuthIndex || node.RingIndex != ringIndex || node.AzimuthIndex != azimuthIndex ||
						node.ElevationDegrees != ring.ElevationDegrees || node.AzimuthDegrees == nil || *node.AzimuthDegrees != wantAzimuth {
						t.Fatalf("node %d violates canonical ring-major order: %+v", prefix+azimuthIndex, node)
					}
				}
			}

			zenithCount := 0
			for _, node := range nodes {
				if node.ElevationDegrees == AstrodomeZenithElevationDegrees {
					zenithCount++
					if node.AzimuthDegrees != nil || node.RingIndex != -1 || node.AzimuthIndex != -1 {
						t.Fatalf("zenith has a defined azimuth or ring: %+v", node)
					}
				}
			}
			if zenithCount != 1 {
				t.Fatalf("zenith count = %d, want 1", zenithCount)
			}
			if zenith := nodes[len(nodes)-1]; zenith.Index != test.wantCount-1 || zenith.ElevationDegrees != 90 {
				t.Fatalf("last node is not the canonical zenith: %+v", zenith)
			}
		})
	}
}

func TestAstrodomeGridGeometryDigests(t *testing.T) {
	t.Parallel()
	for profileID, expected := range map[AstrodomeGridProfileID]string{
		AstrodomeGridDenseV1:         "sha256:e034cc4b4933ea503cd4c01c814885c8a21b2a5dd03706471885226fbdabe2bc",
		AstrodomeGridSparseStorageV1: "sha256:47f2412d8927ca7cf7691ad749cb20b4b03bc08da77a5353f0bb9459d0aa21ff",
		AstrodomeGridProductionV2:    "sha256:600524c0a0ea5c21f7aaa879d3967ef007b8d5804b2217f7345703010b62af3e",
	} {
		profile, err := NewAstrodomeGridProfile(profileID)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := profile.GeometryDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != expected {
			t.Fatalf("%s digest = %s, want %s", profileID, digest, expected)
		}
	}
}

func TestAstrodomeGridProfileIsFreshAndStrictlyVersioned(t *testing.T) {
	t.Parallel()

	profile, err := NewAstrodomeGridProfile(AstrodomeGridDenseV1)
	if err != nil {
		t.Fatal(err)
	}
	profile.Rings[0].AzimuthCount = 16
	if err := profile.Validate(); err == nil {
		t.Fatal("mutated dense ring contract was accepted")
	}
	fresh, err := NewAstrodomeGridProfile(AstrodomeGridDenseV1)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Rings[0].AzimuthCount != 60 {
		t.Fatal("profile constructor exposed mutable canonical storage")
	}
	if _, err := NewAstrodomeGridProfile("dense-latest"); err == nil {
		t.Fatal("unversioned grid alias was accepted")
	}
}

func TestAstrodomeValidTimesAreAtMost72UTCWholeHours(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 28, 16, 0, 0, 0, time.UTC)
	validTimes, err := NewAstrodomeValidTimes(start)
	if err != nil {
		t.Fatalf("NewAstrodomeValidTimes: %v", err)
	}
	if len(validTimes) != 72 {
		t.Fatalf("frame count = %d, want 72", len(validTimes))
	}
	if got, want := validTimes[len(validTimes)-1], start.Add(71*time.Hour); !got.Equal(want) {
		t.Fatalf("last valid time = %s, want %s", got, want)
	}
	if err := ValidateAstrodomeValidTimes(validTimes); err != nil {
		t.Fatalf("ValidateAstrodomeValidTimes: %v", err)
	}
	if err := ValidateAstrodomeValidTimes(validTimes[:71]); err != nil {
		t.Fatalf("ValidateAstrodomeValidTimes accepts a 71-hour native window: %v", err)
	}
	if err := ValidateAstrodomeValidTimes(nil); err == nil {
		t.Fatal("empty time axis was accepted")
	}

	legacy73 := append(append([]time.Time(nil), validTimes...), start.Add(72*time.Hour))
	if err := ValidateAstrodomeValidTimes(legacy73); err == nil {
		t.Fatal("legacy f000..f072 shape was accepted")
	}
	gap := append([]time.Time(nil), validTimes...)
	gap[31] = gap[31].Add(time.Hour)
	if err := ValidateAstrodomeValidTimes(gap); err == nil {
		t.Fatal("gapped time axis was accepted")
	}
	if _, err := NewAstrodomeValidTimes(start.Add(time.Minute)); err == nil {
		t.Fatal("sub-hour start was accepted")
	}
	nonUTC := time.Date(2026, time.July, 28, 19, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	if _, err := NewAstrodomeValidTimes(nonUTC); err == nil {
		t.Fatal("non-UTC start was accepted")
	}
}

func TestAstrodomeHorizontalStencilValidation(t *testing.T) {
	t.Parallel()

	stencil := AstrodomeHorizontalStencil{Supports: [4]AstrodomeHorizontalSupport{
		{ColumnID: "nw", Location: Location{Latitude: 60, Longitude: 30}, Weight: 0.2},
		{ColumnID: "ne", Location: Location{Latitude: 60, Longitude: 31}, Weight: 0.3},
		{ColumnID: "sw", Location: Location{Latitude: 59, Longitude: 30}, Weight: 0.2},
		{ColumnID: "se", Location: Location{Latitude: 59, Longitude: 31}, Weight: 0.3},
	}}
	if err := stencil.Validate(); err != nil {
		t.Fatalf("valid stencil: %v", err)
	}
	bad := stencil
	bad.Supports[3].Weight = math.NaN()
	if err := bad.Validate(); err == nil {
		t.Fatal("NaN stencil weight was accepted")
	}
	bad = stencil
	bad.Supports[3].ColumnID = bad.Supports[2].ColumnID
	if err := bad.Validate(); err == nil {
		t.Fatal("duplicate support column was accepted")
	}
}
