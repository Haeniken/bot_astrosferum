package directional

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

func TestAstrodomeDatasetBuildEncodeDecodeBothProfiles(t *testing.T) {
	for _, profileID := range []forecast.AstrodomeGridProfileID{
		forecast.AstrodomeGridDenseV1,
		forecast.AstrodomeGridSparseStorageV1,
		forecast.AstrodomeGridProductionV2,
	} {
		t.Run(string(profileID), func(t *testing.T) {
			input := completeAstrodomeDatasetInput(t, profileID)
			dataset, err := BuildAstrodomeDataset(input)
			if err != nil {
				t.Fatalf("BuildAstrodomeDataset: %v", err)
			}
			if len(dataset.Frames) != forecast.AstrodomeFrameCount || dataset.Grid.NodeCount != input.Profile.NodeCount() {
				t.Fatalf("dataset cardinality = %d x %d", len(dataset.Frames), dataset.Grid.NodeCount)
			}
			if dataset.VacuumDirectionAvailable == nil || *dataset.VacuumDirectionAvailable {
				t.Fatalf("current apparent-direction metadata = %v", dataset.VacuumDirectionAvailable)
			}
			zenith := dataset.Frames[0].Nodes[len(dataset.Frames[0].Nodes)-1]
			if zenith.AzimuthDegrees != nil || zenith.ElevationDegrees != 90 {
				t.Fatalf("zenith = %+v", zenith)
			}
			encoded, err := EncodeAstrodomeDataset(dataset)
			if err != nil {
				t.Fatalf("EncodeAstrodomeDataset: %v", err)
			}
			decoded, err := DecodeAstrodomeDataset(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("DecodeAstrodomeDataset: %v", err)
			}
			if decoded.GridGeometryDigest != dataset.GridGeometryDigest || len(decoded.Frames[0].Nodes) != input.Profile.NodeCount() {
				t.Fatal("decoded dataset changed its geometry identity")
			}
			decodedBytes, err := DecodeAstrodomeDatasetBytes(encoded)
			if err != nil || decodedBytes.GridGeometryDigest != dataset.GridGeometryDigest {
				t.Fatalf("byte decoder changed dataset identity: %v", err)
			}
		})
	}
}

func TestAstrodomeDatasetAcceptsShortNativeHourlyWindow(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	input.Frames = input.Frames[:70]
	for index := range input.CelestialTracks {
		input.CelestialTracks[index].Samples = input.CelestialTracks[index].Samples[:70]
	}
	input.PolarisTrack.Samples = input.PolarisTrack.Samples[:70]
	dataset, err := BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatalf("BuildAstrodomeDataset: %v", err)
	}
	if len(dataset.ValidTimes) != 70 || len(dataset.Frames) != 70 || dataset.Grid.FrameCount != 70 {
		t.Fatalf("short dataset cardinality = %d/%d/%d", len(dataset.ValidTimes), len(dataset.Frames), dataset.Grid.FrameCount)
	}
}

func TestAstrodomeDatasetKeepsAtmosphericNodeBelowEmbeddedGLO30Skyline(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	samples := make([]forecast.TerrainSkylineSample, forecast.TerrainSkylineAzimuthCount)
	for index := range samples {
		samples[index] = forecast.TerrainSkylineSample{
			AzimuthDegrees: float64(index), ElevationDegrees: 20, ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := forecast.NewSyntheticTerrainSkyline(59.9, 30.2, 17, samples)
	if err != nil {
		t.Fatal(err)
	}
	input.TerrainSkyline = profile
	dataset, err := BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatal(err)
	}
	node := dataset.Frames[0].Nodes[0]
	if node.State != AstrodomeDatasetStateValid || node.Overall == nil ||
		node.TerrainObstructionSource != forecast.AstrodomeTerrainObstructionNone ||
		node.TerrainSkylineElevationDegrees == nil || *node.TerrainSkylineElevationDegrees != 20 ||
		node.TerrainSkylineHasObstructionAtEvaluationDirection == nil ||
		!*node.TerrainSkylineHasObstructionAtEvaluationDirection || node.LimitingFactor == "terrain_skyline" {
		t.Fatalf("GLO-30 skyline replaced the atmospheric result: %+v", node)
	}
}

func TestAstrodomeTerrainSkylineInformationBoundaryAndCyclicInterpolation(t *testing.T) {
	t.Parallel()
	samples := make([]forecast.TerrainSkylineSample, forecast.TerrainSkylineAzimuthCount)
	for index := range samples {
		samples[index] = forecast.TerrainSkylineSample{
			AzimuthDegrees: float64(index), ElevationDegrees: 1, ObstacleSurfaceDistanceM: 1000,
		}
	}
	samples[359].ElevationDegrees = 12
	samples[0].ElevationDegrees = 14
	profile, err := forecast.NewSyntheticTerrainSkyline(59.9, 30.2, 17, samples)
	if err != nil {
		t.Fatal(err)
	}
	azimuth := 359.5
	for _, test := range []struct {
		elevation float64
		blocked   bool
	}{{12, true}, {13, true}, {14, false}} {
		skyline, blocked, err := astrodomeTerrainSkylineNodeInformation(profile, forecast.AstrodomeGridNode{
			ElevationDegrees: test.elevation, AzimuthDegrees: &azimuth,
		})
		if err != nil || skyline == nil || *skyline != 13 || blocked != test.blocked {
			t.Fatalf("elevation %.1f skyline=%v blocked=%v err=%v", test.elevation, skyline, blocked, err)
		}
	}
	zenith, blocked, err := astrodomeTerrainSkylineNodeInformation(profile, forecast.AstrodomeGridNode{ElevationDegrees: 90})
	if err != nil || zenith != nil || blocked {
		t.Fatalf("zenith terrain information = %v, %v, %v", zenith, blocked, err)
	}
}

func TestAstrodomeDatasetRejectsMutatedTerrainSkylineNodeInformation(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	samples := make([]forecast.TerrainSkylineSample, forecast.TerrainSkylineAzimuthCount)
	for index := range samples {
		samples[index] = forecast.TerrainSkylineSample{
			AzimuthDegrees: float64(index), ElevationDegrees: 20, ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := forecast.NewSyntheticTerrainSkyline(59.9, 30.2, 17, samples)
	if err != nil {
		t.Fatal(err)
	}
	input.TerrainSkyline = profile
	dataset, err := BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AstrodomeDatasetNode){
		"missing obstruction flag": func(node *AstrodomeDatasetNode) {
			node.TerrainSkylineHasObstructionAtEvaluationDirection = nil
		},
		"wrong obstruction flag": func(node *AstrodomeDatasetNode) {
			value := false
			node.TerrainSkylineHasObstructionAtEvaluationDirection = &value
		},
		"wrong skyline elevation": func(node *AstrodomeDatasetNode) {
			value := 19.0
			node.TerrainSkylineElevationDegrees = &value
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := dataset
			candidate.Frames = append([]AstrodomeDatasetFrame(nil), dataset.Frames...)
			candidate.Frames[0].Nodes = append([]AstrodomeDatasetNode(nil), dataset.Frames[0].Nodes...)
			mutate(&candidate.Frames[0].Nodes[0])
			if err := candidate.Validate(); err == nil {
				t.Fatal("mutated terrain skyline information was accepted")
			}
		})
	}
}

func TestAstrodomeDatasetKeepsTerrainSkylineInformationOnUnavailableAndHHLNodes(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	samples := make([]forecast.TerrainSkylineSample, forecast.TerrainSkylineAzimuthCount)
	for index := range samples {
		samples[index] = forecast.TerrainSkylineSample{
			AzimuthDegrees: float64(index), ElevationDegrees: 20, ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := forecast.NewSyntheticTerrainSkyline(59.9, 30.2, 17, samples)
	if err != nil {
		t.Fatal(err)
	}
	input.TerrainSkyline = profile
	unavailable := &input.Frames[0].Nodes[0]
	unavailable.Available = false
	unavailable.State = forecast.AstrodomeScienceNodeUnavailable
	unavailable.Quality.Category = forecast.AstrodomeScienceQualityUnavailable
	hhl := &input.Frames[0].Nodes[1]
	hhl.Available = false
	hhl.State = forecast.AstrodomeScienceNodeTerrainBlocked
	hhl.TerrainObstructionSource = forecast.AstrodomeTerrainObstructionHHL
	hhl.Quality.Category = forecast.AstrodomeScienceQualityUnavailable
	dataset, err := BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatal(err)
	}
	for index, wantState := range []string{AstrodomeDatasetStateUnavailable, AstrodomeDatasetStateTerrainBlocked} {
		node := dataset.Frames[0].Nodes[index]
		if node.State != wantState || node.TerrainSkylineElevationDegrees == nil ||
			*node.TerrainSkylineElevationDegrees != 20 ||
			node.TerrainSkylineHasObstructionAtEvaluationDirection == nil ||
			!*node.TerrainSkylineHasObstructionAtEvaluationDirection {
			t.Fatalf("node %d did not retain independent terrain information: %+v", index, node)
		}
	}
}

func TestAstrodomeDatasetRejectsMisorderedAndMisalignedCelestialTracks(t *testing.T) {
	t.Parallel()
	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	dataset.CelestialTracks[0], dataset.CelestialTracks[1] = dataset.CelestialTracks[1], dataset.CelestialTracks[0]
	if err := dataset.Validate(); err == nil {
		t.Fatal("Astrodome dataset with reordered celestial tracks was accepted")
	}
	dataset, err = BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	dataset.CelestialTracks[4].Samples[2].ValidAt = dataset.CelestialTracks[4].Samples[2].ValidAt.Add(time.Hour)
	if err := dataset.Validate(); err == nil {
		t.Fatal("Astrodome dataset with a time-shifted celestial sample was accepted")
	}
}

func TestAstrodomeDatasetRejectsInvalidPolarisContract(t *testing.T) {
	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	dataset.PolarisTrack.CatalogVersion = "wrong"
	if err := dataset.Validate(); err == nil {
		t.Fatal("wrong Polaris catalog version was accepted")
	}
	dataset, err = BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	dataset.PolarisTrack.Samples[0].AzimuthDegrees = math.NaN()
	if err := dataset.Validate(); err == nil {
		t.Fatal("non-finite Polaris position was accepted")
	}
}

func TestAstrodomeDatasetPenaltyLossUsesFactorProductAtExactVeto(t *testing.T) {
	t.Parallel()

	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	node := &input.Frames[0].Nodes[0]
	node.Factors.Turbulence = 0.75
	node.Factors.Cloud = 1.2051727890562735e-12
	node.Factors.SurfaceWind = 1
	node.Factors.Fog = 1
	node.Factors.Precipitation = 0
	node.State = forecast.AstrodomeScienceNodeAvailable
	overall := 1.0
	node.Overall = &overall
	contributions, totalLoss, err := forecast.ShapleyMultiplicativeLoss([]forecast.OverallPenaltyFactor{
		{Key: forecast.OverallPenaltyOpticalTurbulence, Factor: node.Factors.Turbulence},
		{Key: forecast.OverallPenaltyCloudObstruction, Factor: node.Factors.Cloud},
		{Key: forecast.OverallPenaltySurfaceWind, Factor: node.Factors.SurfaceWind},
		{Key: forecast.OverallPenaltyFog, Factor: node.Factors.Fog},
		{Key: forecast.OverallPenaltyPrecipitation, Factor: node.Factors.Precipitation},
	})
	if err != nil || totalLoss != 1 {
		t.Fatalf("exact-veto reference loss = %.17g, %v", totalLoss, err)
	}
	node.PenaltyContributions = contributions

	dataset, err := BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatalf("BuildAstrodomeDataset: %v", err)
	}
	mapped := dataset.Frames[0].Nodes[0]
	if mapped.PenaltyLossFraction == nil || *mapped.PenaltyLossFraction != 1 {
		t.Fatalf("serialized exact-veto loss = %v", mapped.PenaltyLossFraction)
	}
	contributionTotal := 0.0
	for _, contribution := range mapped.PenaltyContributions {
		contributionTotal += contribution.LossFraction
	}
	if !(contributionTotal > 1) {
		t.Fatalf("production roundoff regression did not exceed one: %.17g", contributionTotal)
	}
}

func TestAstrodomeDatasetRejectsPenaltyLossOutsideUnitInterval(t *testing.T) {
	t.Parallel()

	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	dataset.Frames[0].Nodes[0].PenaltyLossFraction = floatPointer(math.Nextafter(1, math.Inf(1)))
	if err := dataset.Validate(); err == nil {
		t.Fatal("penalty loss above one was accepted")
	}
}

func TestCurrentAstrodomeDatasetRequiresCalibrationDigest(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	input.CalibrationSHA256 = ""
	if _, err := BuildAstrodomeDataset(input); err == nil {
		t.Fatal("current Astrodome dataset without calibration digest was accepted")
	}
}

func TestAstrodomeDatasetArchiveValidationRequiresCurrentContract(t *testing.T) {
	t.Parallel()
	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatalf("BuildAstrodomeDataset: %v", err)
	}
	if dataset.ScienceVersion != forecast.AstrodomeScienceVersion ||
		dataset.SciencePathVersion != forecast.AstrodomeSciencePathContractVersion {
		t.Fatalf("current contract = %q / %q", dataset.ScienceVersion, dataset.SciencePathVersion)
	}
	if err := dataset.ValidateArchived(); err != nil {
		t.Fatalf("current dataset rejected by archive validation: %v", err)
	}
	old := dataset
	old.ScienceVersion = "astrodome-science-kernel-v28"
	old.SciencePathVersion = "astrodome-science-path-v22"
	old.CalibrationVersion = old.ScienceVersion
	if err := old.ValidateArchived(); err == nil {
		t.Fatal("saved visualization with an older scientific contract was accepted")
	}
	encodedOld, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeArchivedAstrodomeDataset(bytes.NewReader(encodedOld)); err == nil {
		t.Fatal("archive decoder accepted an older scientific contract")
	}
}

func TestAstrodomeDatasetRequiresExplicitAbsenceOfVacuumDirection(t *testing.T) {
	t.Parallel()

	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	dataset.VacuumDirectionAvailable = nil
	if err := dataset.Validate(); err == nil {
		t.Fatal("current dataset without vacuum-direction availability metadata was accepted")
	}
	vacuumAvailable := true
	dataset.VacuumDirectionAvailable = &vacuumAvailable
	if err := dataset.Validate(); err == nil {
		t.Fatal("current dataset claiming an unavailable vacuum direction was accepted")
	}
}

func TestAstrodomeDatasetRejectsPreviousRefractionContract(t *testing.T) {
	t.Parallel()
	if err := validateAstrodomeGeometryVersions(
		forecast.AstrodomeRefractionGeometryVersion,
		"dormand-prince-5-4-event-v2",
		forecast.AstrodomeCiddorVersion,
	); err == nil {
		t.Fatal("saved visualization accepted a previous refraction contract")
	}
}

func TestAstrodomeDatasetMapsFullRefractionProvenance(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	input.RayGeometryVersion = forecast.AstrodomeRefractionGeometryVersion
	input.RefractionVersion = forecast.AstrodomeRefractionIntegratorVersion
	input.RefractivityVersion = forecast.AstrodomeCiddorVersion
	direction := forecast.AstrodomeECEFVector{X: 1}
	for frameIndex := range input.Frames {
		for nodeIndex := range input.Frames[frameIndex].Nodes {
			input.Frames[frameIndex].Nodes[nodeIndex].GeometryMode = forecast.AstrodomeScienceGeometryRefractionFull
			input.Frames[frameIndex].Nodes[nodeIndex].RayGeometryVersion = forecast.AstrodomeRefractionGeometryVersion
			input.Frames[frameIndex].Nodes[nodeIndex].RefractionVersion = forecast.AstrodomeRefractionIntegratorVersion
			input.Frames[frameIndex].Nodes[nodeIndex].RefractivityVersion = forecast.AstrodomeCiddorVersion
			input.Frames[frameIndex].Nodes[nodeIndex].DirectionAtModelTopECEF = &direction
		}
	}
	unavailable := &input.Frames[0].Nodes[0]
	unavailable.Available = false
	unavailable.State = forecast.AstrodomeScienceNodeUnavailable
	unavailable.Quality.Category = forecast.AstrodomeScienceQualityUnavailable
	unavailable.DirectionAtModelTopECEF = nil
	dataset, err := BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatalf("BuildAstrodomeDataset: %v", err)
	}
	node := dataset.Frames[0].Nodes[1]
	if node.GeometryMode != forecast.AstrodomeScienceGeometryRefractionFull ||
		node.DirectionAtModelTopECEF == nil || node.DirectionAtModelTopECEF.X != 1 {
		t.Fatalf("full-refraction node provenance = %+v", node)
	}
	if unavailableNode := dataset.Frames[0].Nodes[0]; unavailableNode.State != AstrodomeDatasetStateUnavailable ||
		unavailableNode.DirectionAtModelTopECEF != nil {
		t.Fatalf("unavailable refraction node fabricated top provenance = %+v", unavailableNode)
	}
}

func TestAstrodomeDatasetRequiresModelTopTangentOnlyForCompletedRefractionRay(t *testing.T) {
	t.Parallel()

	for _, state := range []string{AstrodomeDatasetStateUnavailable, AstrodomeDatasetStateTerrainBlocked} {
		node := AstrodomeDatasetNode{
			State: state, GeometryMode: forecast.AstrodomeScienceGeometryRefractionFull,
		}
		if err := validateAstrodomeNodeGeometry(node, forecast.AstrodomeRefractionGeometryVersion); err != nil {
			t.Fatalf("incomplete %s ray without fabricated model-top tangent: %v", state, err)
		}
	}
	node := AstrodomeDatasetNode{
		State: AstrodomeDatasetStateValid, GeometryMode: forecast.AstrodomeScienceGeometryRefractionFull,
	}
	if err := validateAstrodomeNodeGeometry(node, forecast.AstrodomeRefractionGeometryVersion); err == nil {
		t.Fatal("completed refraction ray without a model-top tangent was accepted")
	}
}

func TestAstrodomeDatasetRejectsOrderPartialUnknownAndFabricatedDiagnostics(t *testing.T) {
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	input.Frames[0].Nodes = input.Frames[0].Nodes[:len(input.Frames[0].Nodes)-1]
	if _, err := BuildAstrodomeDataset(input); err == nil {
		t.Fatal("partial frame was accepted")
	}

	input = completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	wrong := *input.Frames[0].Nodes[0].AzimuthDegrees + 0.5
	input.Frames[0].Nodes[0].AzimuthDegrees = &wrong
	if _, err := BuildAstrodomeDataset(input); err == nil {
		t.Fatal("out-of-order angular node was accepted")
	}

	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeAstrodomeDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := bytes.Replace(encoded,
		[]byte(fmt.Sprintf(`{"schema_version":%d`, AstrodomeDatasetSchemaVersion)),
		[]byte(fmt.Sprintf(`{"unknown":true,"schema_version":%d`, AstrodomeDatasetSchemaVersion)), 1)
	if bytes.Equal(withUnknown, encoded) {
		t.Fatal("unknown-field test did not mutate the current schema")
	}
	if _, err := DecodeAstrodomeDataset(bytes.NewReader(withUnknown)); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
	previousSchema := bytes.Replace(encoded,
		[]byte(fmt.Sprintf(`"schema_version":%d`, AstrodomeDatasetSchemaVersion)),
		[]byte(fmt.Sprintf(`"schema_version":%d`, AstrodomeDatasetSchemaVersion-1)), 1)
	if bytes.Equal(previousSchema, encoded) {
		t.Fatal("previous-schema test did not mutate the current dataset")
	}
	if _, err := DecodeArchivedAstrodomeDataset(bytes.NewReader(previousSchema)); err == nil {
		t.Fatal("saved-visualization decoder accepted the previous dataset schema")
	}

	node := &dataset.Frames[0].Nodes[0]
	node.NumericalError.OverallAbsolute = nil
	if err := dataset.Validate(); err == nil {
		t.Fatal("available node without an observed Overall residual was accepted")
	}

	node = &dataset.Frames[0].Nodes[0]
	node.State = AstrodomeDatasetStateUnavailable
	node.Overall = nil
	node.SeeingArcsec500NM = nil
	node.Tau0MS500NM = nil
	node.Tau0ConservativeMS500NM = nil
	node.IntegratedCn2 = nil
	node.WindWeightedCn2 = nil
	node.EffectiveCloudTransmission = nil
	node.CloudOpticalDepthLiquid = nil
	node.CloudOpticalDepthIce = nil
	node.SlantWaterVapourKgM2 = nil
	node.Factors = nil
	node.PenaltyContributions = []forecast.OverallPenaltyContribution{}
	node.PenaltyLossFraction = nil
	node.LimitingFactor = "unavailable_data"
	node.DataQuality = forecast.AstrodomeScienceQualityUnavailable
	node.QualityComponents.MandatoryComplete = false
	node.NumericalError = AstrodomeDatasetNumericalError{OverallAbsolute: floatPointer(0)}
	if err := dataset.Validate(); err == nil {
		t.Fatal("fabricated zero numerical error on an unavailable node was accepted")
	}
}

func TestValidatePreparedAstrodomeRequiresPrefixedLowercaseGeometryDigest(t *testing.T) {
	valid := PreparedAstrodome{
		ScienceCacheKey: "science", Source: SourceIdentity{
			Provider: "icon-eu", RunID: "2026072800", GridProfile: "dense-v1",
			GeometryDigest: "sha256:" + strings.Repeat("a", 64),
		}, Payload: json.RawMessage("null"),
	}
	prepared, err := validatePreparedAstrodome(valid)
	if err != nil || prepared.Source.Provider != "icon-eu" {
		t.Fatalf("canonical prepared source rejected: %+v, %v", prepared, err)
	}
	wrongProvider := valid
	wrongProvider.Source.Provider = "ICON-EU"
	if _, err := validatePreparedAstrodome(wrongProvider); err == nil {
		t.Fatal("non-canonical provider case was accepted")
	}
	for name, digest := range map[string]string{
		"bare":      strings.Repeat("a", 64),
		"uppercase": "sha256:" + strings.Repeat("A", 64),
		"short":     "sha256:" + strings.Repeat("a", 63),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Source.GeometryDigest = digest
			if _, err := validatePreparedAstrodome(candidate); err == nil {
				t.Fatalf("geometry digest %q was accepted", digest)
			}
		})
	}
}

func TestAstrodomeDatasetLimitedApproximationContract(t *testing.T) {
	t.Parallel()

	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	node := &dataset.Frames[0].Nodes[0]
	node.DataQuality = forecast.AstrodomeScienceQualityLimited
	node.QualityComponents.QuadratureConvergence = 0
	approximationLengthM := 0.0015
	node.QualityComponents.ApproximationLengthM = &approximationLengthM
	node.QualityComponents.ReasonCodes = append(
		node.QualityComponents.ReasonCodes,
		"quadrature_not_converged",
		"short_path_approximation",
	)
	if err := dataset.Validate(); err != nil {
		t.Fatalf("limited approximation rejected: %v", err)
	}
	aboveCeiling := math.Nextafter(
		forecast.AstrodomeScienceMaximumApproximatePathLengthM,
		math.Inf(1),
	)
	node.QualityComponents.ApproximationLengthM = &aboveCeiling
	if err := dataset.Validate(); err == nil {
		t.Fatal("above-ceiling approximation was accepted")
	}
}

func TestCurrentAstrodomeDatasetRequiresCurrentQualitySerialization(t *testing.T) {
	t.Parallel()

	dataset, err := BuildAstrodomeDataset(completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	withoutApproximation := bytes.Replace(
		encoded,
		[]byte(`"approximation_length_m":0,`),
		nil,
		1,
	)
	if bytes.Equal(withoutApproximation, encoded) {
		t.Fatal("current fixture did not serialize approximation_length_m")
	}
	if _, err := DecodeAstrodomeDataset(bytes.NewReader(withoutApproximation)); err == nil {
		t.Fatal("current dataset without approximation_length_m was accepted")
	}

	legacyQuality := bytes.Replace(
		encoded,
		[]byte(`"data_quality":"good"`),
		[]byte(`"data_quality":"good_coarse_model"`),
		1,
	)
	if bytes.Equal(legacyQuality, encoded) {
		t.Fatal("current fixture did not contain a good quality node")
	}
	if _, err := DecodeAstrodomeDataset(bytes.NewReader(legacyQuality)); err == nil {
		t.Fatal("current dataset with archived good_coarse_model was accepted")
	}

	if _, err := DecodeArchivedAstrodomeDataset(bytes.NewReader(legacyQuality)); err == nil {
		t.Fatal("saved-visualization decoder accepted the removed quality value")
	}
}

func completeAstrodomeDatasetInput(t *testing.T, profileID forecast.AstrodomeGridProfileID) AstrodomeDatasetInput {
	t.Helper()
	calibrationDigest, err := forecast.DefaultAstrodomeScienceCalibration().Digest()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := forecast.NewAstrodomeGridProfile(profileID)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := profile.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	run := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	identity := forecast.AstrodomePrimitiveVolumeIdentity{
		Provider: "icon-eu", Product: "icon-eu-regular-lat-lon", Grid: "0.0625deg",
		RunID: run.Format("2006010215"), RunBaseTime: run,
		RunManifestDigest:    "sha256:" + strings.Repeat("d", 64),
		InputContractVersion: forecast.AstrodomePrimitiveInputContractVersion,
	}
	frames := make([]AstrodomeDatasetFrameInput, forecast.AstrodomeFrameCount)
	validTimes := make([]time.Time, len(frames))
	for frameIndex := range frames {
		validAt := run.Add(time.Duration(frameIndex) * time.Hour)
		validTimes[frameIndex] = validAt
		leadQuality := max(0.65, 0.96-0.26*float64(frameIndex)/72)
		qualityCategory := forecast.AstrodomeScienceQualityLimited
		if leadQuality >= 0.85 {
			qualityCategory = forecast.AstrodomeScienceQualityGood
		} else if leadQuality >= 0.75 {
			qualityCategory = forecast.AstrodomeScienceQualityUsable
		}
		nodes := make([]forecast.AstrodomeScienceNode, len(definitions))
		for nodeIndex, definition := range definitions {
			nodes[nodeIndex] = completeAstrodomeScienceNode(identity, validAt, definition, leadQuality, qualityCategory)
		}
		frames[frameIndex] = AstrodomeDatasetFrameInput{
			ValidAt: validAt,
			Surface: forecast.AstrodomeScienceSiteInputs{
				SourceIdentity: identity, ValidAt: validAt, WindSpeed10MMS: 2, WindGust10MMS: 4,
				FogHeuristic: forecast.AstrodomeScienceFogNone, ForecastLeadHours: float64(frameIndex),
				PrecipitationIntervalStart: validAt.Add(-time.Hour), PrecipitationIntervalEnd: validAt,
			},
			SolarAltitudeDeg: -20, Nodes: nodes,
		}
	}
	location := forecast.Location{Latitude: 59.9, Longitude: 30.2, TimeZone: "Europe/Moscow"}
	celestialTracks, err := astronomy.ComputeCelestialTracks(location, validTimes)
	if err != nil {
		t.Fatal(err)
	}
	polarisTrack, err := astronomy.ComputePolarisTrack(location, validTimes)
	if err != nil {
		t.Fatal(err)
	}
	elevation := 17.0
	return AstrodomeDatasetInput{
		SourceIdentity: identity, GeneratedAt: run.Add(30 * time.Minute), Profile: profile,
		SourceColumnPlanDigest: "sha256:" + strings.Repeat("2", 64),
		RequestedLocation:      AstrodomeDatasetLocation{Latitude: 59.9, Longitude: 30.2, TimeZone: "Europe/Moscow"},
		ModelLocation:          AstrodomeDatasetLocation{Latitude: 59.875, Longitude: 30.1875, SurfaceElevationM: &elevation},
		RayGeometryVersion:     forecast.AstrodomeGeometryVersion,
		RefractionVersion:      "not-applicable",
		RefractivityVersion:    "not-applicable",
		CalibrationVersion:     forecast.DefaultAstrodomeScienceCalibration().Version,
		CalibrationSHA256:      calibrationDigest,
		CelestialTracks:        celestialTracks,
		PolarisTrack:           polarisTrack,
		Frames:                 frames,
	}
}

func completeAstrodomeScienceNode(
	identity forecast.AstrodomePrimitiveVolumeIdentity,
	validAt time.Time,
	definition forecast.AstrodomeGridNode,
	leadQuality float64,
	category forecast.AstrodomeScienceQualityCategory,
) forecast.AstrodomeScienceNode {
	integrated := &forecast.AstrodomeScienceIntegralEstimate{Value: 1e-13, EstimatedAbsoluteError: 1e-17}
	windWeighted := &forecast.AstrodomeScienceIntegralEstimate{Value: 1e-14, EstimatedAbsoluteError: 1e-18}
	water := &forecast.AstrodomeScienceIntegralEstimate{Value: 12, EstimatedAbsoluteError: 1e-4}
	liquid := &forecast.AstrodomeScienceIntegralEstimate{Value: 0, EstimatedAbsoluteError: 1e-8}
	ice := &forecast.AstrodomeScienceIntegralEstimate{Value: 0, EstimatedAbsoluteError: 1e-8}
	seeing, tau, conservative, transmission, overall := 1.0, 5.0, 4.9, 1.0, 10.0
	zero := 0.0
	return forecast.AstrodomeScienceNode{
		Available: true, State: forecast.AstrodomeScienceNodeAvailable,
		ScienceVersion: forecast.AstrodomeScienceVersion, SourceIdentity: identity, ValidAt: validAt,
		ElevationDegrees: definition.ElevationDegrees, AzimuthDegrees: cloneFloat(definition.AzimuthDegrees),
		GeometryMode:       forecast.AstrodomeScienceGeometryStraight,
		RayGeometryVersion: forecast.AstrodomeGeometryVersion,
		IntegratedCn2:      integrated, WindWeightedCn2: windWeighted, SlantWaterKgM2: water,
		LiquidOpticalDepth: liquid, IceOpticalDepth: ice, Seeing500Arcsec: &seeing,
		Tau0500MS: &tau, Tau0ConservativeScoreMS: &conservative, Tau0State: "finite",
		CloudTransmissionNominal: &transmission, CloudTransmissionConservative: &transmission,
		CloudTransmission: &transmission, Overall: &overall,
		Factors: &forecast.AstrodomeScienceFactors{
			SeeingQuality: 1, CoherenceQuality: 1, Turbulence: 1,
			Cloud: 1, SurfaceWind: 1, Fog: 1, Precipitation: 1,
		},
		PenaltyContributions: []forecast.OverallPenaltyContribution{
			{Key: forecast.OverallPenaltyOpticalTurbulence}, {Key: forecast.OverallPenaltyCloudObstruction},
			{Key: forecast.OverallPenaltySurfaceWind}, {Key: forecast.OverallPenaltyFog},
			{Key: forecast.OverallPenaltyPrecipitation},
		},
		NumericalError: &forecast.AstrodomeScienceNumericalError{
			OverallAbsolute: 0, CloudTransmissionAbsolute: 0, TurbulenceIntegralRelative: &zero,
			IntegratedCn2Relative: &zero, WindWeightedCn2Relative: &zero,
		},
		Quality: forecast.AstrodomeScienceQuality{
			LeadTimeQualityHeuristic: leadQuality, GeometryCoverage: 1, TurbulencePathCoverage: 1,
			CloudPathCoverage: 1, HumidityPathCoverage: floatPointer(1), TemporalResolutionHours: 1,
			QuadratureConverged: true, TopClosed: true, Category: category,
		},
	}
}
