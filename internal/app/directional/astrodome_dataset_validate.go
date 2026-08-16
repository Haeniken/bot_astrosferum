package directional

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

var astrodomeVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,127}$`)

// Validate checks schema, exact grid cardinality/order, hourly time identity,
// source identity, physical nullability, exact factor/Shapley closure, input
// quality, and observed numerical diagnostics.
func (dataset AstrodomeDataset) Validate() error {
	return dataset.validate()
}

// ValidateArchived validates an immutable saved visualization. Saved results
// deliberately use the same exact current scientific contract as newly
// calculated results; an older contract is rejected instead of reinterpreted.
func (dataset AstrodomeDataset) ValidateArchived() error {
	return dataset.validate()
}

func (dataset AstrodomeDataset) validate() error {
	if dataset.SchemaVersion != AstrodomeDatasetSchemaVersion {
		return fmt.Errorf("astrodome dataset schema version must be %d", AstrodomeDatasetSchemaVersion)
	}
	if err := dataset.TerrainSkyline.Validate(); err != nil {
		return fmt.Errorf("astrodome terrain skyline: %w", err)
	}
	if dataset.TerrainSkyline.Source == "pending" {
		return errors.New("astrodome terrain skyline preparation is incomplete")
	}
	if dataset.Provider != "icon-eu" || !validICONRunID(dataset.RunID) {
		return errors.New("astrodome dataset provider or run ID is invalid")
	}
	if strings.TrimSpace(dataset.ModelProduct) == "" || len(dataset.ModelProduct) > 256 ||
		strings.TrimSpace(dataset.ModelGrid) == "" || len(dataset.ModelGrid) > 256 {
		return errors.New("astrodome model product or grid provenance is invalid")
	}
	if !validPrefixedSHA256(dataset.RunManifestDigest) {
		return errors.New("astrodome run manifest digest is invalid")
	}
	runTime, err := time.Parse("2006010215", dataset.RunID)
	if err != nil || !dataset.RunBaseTime.Equal(runTime) || !wholeUTCHour(dataset.RunBaseTime) {
		return errors.New("astrodome run base time does not match the run ID")
	}
	if dataset.GeneratedAt.IsZero() || !isUTC(dataset.GeneratedAt) {
		return errors.New("astrodome generated time must be a UTC instant")
	}
	if err := validateAstrodomeDatasetLocation(dataset.RequestedLocation, true, false); err != nil {
		return fmt.Errorf("astrodome requested location: %w", err)
	}
	if err := validateAstrodomeDatasetLocation(dataset.ModelLocation, false, true); err != nil {
		return fmt.Errorf("astrodome model location: %w", err)
	}
	versions := []string{
		dataset.InputContractVersion, dataset.GeometryVersion, dataset.RayGeometryVersion,
		dataset.RefractionVersion, dataset.RefractivityVersion, dataset.ScienceVersion, dataset.CalibrationVersion,
	}
	for _, version := range versions {
		if !astrodomeVersionPattern.MatchString(version) {
			return errors.New("astrodome dataset contains an invalid or missing version")
		}
	}
	if dataset.CalibrationSHA256 != "" && !validPrefixedSHA256(dataset.CalibrationSHA256) {
		return errors.New("astrodome dataset science calibration digest is invalid")
	}
	if dataset.InputContractVersion != forecast.AstrodomePrimitiveInputContractVersion ||
		dataset.GeometryVersion != forecast.AstrodomeGridGeometryVersion {
		return errors.New("astrodome dataset uses an unsupported scientific contract")
	}
	if err := validateAstrodomeScienceContract(dataset); err != nil {
		return err
	}
	if err := validateAstrodomeDirectionContract(dataset); err != nil {
		return err
	}
	if err := validateAstrodomeSourceColumnPlanContract(dataset); err != nil {
		return err
	}
	if err := validateAstrodomeGeometryVersions(
		dataset.RayGeometryVersion,
		dataset.RefractionVersion,
		dataset.RefractivityVersion,
	); err != nil {
		return err
	}
	profile, err := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridProfileID(dataset.GridProfile))
	if err != nil {
		return err
	}
	digest, err := profile.GeometryDigest()
	if err != nil || dataset.GridGeometryDigest != digest {
		return errors.New("astrodome grid geometry digest does not match the pinned profile")
	}
	if err := forecast.ValidateAstrodomeValidTimes(dataset.ValidTimes); err != nil {
		return err
	}
	if err := validateAstrodomeCelestialTracks(dataset.CelestialEphemerisVersion, dataset.CelestialTracks, dataset.ValidTimes, forecast.Location{
		Latitude: dataset.RequestedLocation.Latitude, Longitude: dataset.RequestedLocation.Longitude, TimeZone: dataset.RequestedLocation.TimeZone,
	}); err != nil {
		return err
	}
	if err := astronomy.ValidatePolarisTrack(dataset.PolarisTrack, dataset.ValidTimes); err != nil {
		return fmt.Errorf("astrodome Polaris track: %w", err)
	}
	if err := validateAstrodomeDatasetGrid(dataset.Grid, profile, len(dataset.ValidTimes)); err != nil {
		return err
	}
	if len(dataset.Frames) != len(dataset.ValidTimes) {
		return fmt.Errorf("astrodome dataset has %d frames, want %d", len(dataset.Frames), len(dataset.ValidTimes))
	}
	definitions, err := profile.Nodes()
	if err != nil {
		return err
	}
	for frameIndex := range dataset.Frames {
		frame := dataset.Frames[frameIndex]
		if !frame.ValidAt.Equal(dataset.ValidTimes[frameIndex]) {
			return fmt.Errorf("astrodome frame %d time does not match valid_times", frameIndex)
		}
		leadHours := frame.ValidAt.Sub(dataset.RunBaseTime).Hours()
		if leadHours < 0 || math.Abs(frame.SurfaceCommon.ForecastLeadHours-leadHours) > 1e-9 {
			return fmt.Errorf("astrodome frame %d forecast lead does not match its run time", frameIndex)
		}
		if err := validateAstrodomeSurfaceCommon(frame.SurfaceCommon); err != nil {
			return fmt.Errorf("astrodome frame %d surface input: %w", frameIndex, err)
		}
		if !finiteDatasetNumber(frame.SolarAltitudeDeg) || frame.SolarAltitudeDeg < -90 || frame.SolarAltitudeDeg > 90 ||
			frame.TwilightBand != astrodomeTwilightBand(frame.SolarAltitudeDeg) {
			return fmt.Errorf("astrodome frame %d solar/twilight state is invalid", frameIndex)
		}
		if len(frame.Nodes) != len(definitions) {
			return fmt.Errorf("astrodome frame %d has %d nodes, want %d", frameIndex, len(frame.Nodes), len(definitions))
		}
		for nodeIndex := range frame.Nodes {
			if err := validateAstrodomeDatasetNode(
				frame.Nodes[nodeIndex],
				definitions[nodeIndex],
				dataset.RayGeometryVersion,
				dataset.ScienceVersion == forecast.AstrodomeScienceVersion,
			); err != nil {
				return fmt.Errorf("astrodome frame %d node %d: %w", frameIndex, nodeIndex, err)
			}
			if err := validateAstrodomeTerrainSkylineNode(
				frame.Nodes[nodeIndex], definitions[nodeIndex], dataset.TerrainSkyline,
			); err != nil {
				return fmt.Errorf("astrodome frame %d node %d terrain skyline: %w", frameIndex, nodeIndex, err)
			}
		}
	}
	return nil
}

func validateAstrodomeTerrainSkylineNode(
	node AstrodomeDatasetNode,
	definition forecast.AstrodomeGridNode,
	profile forecast.TerrainSkyline,
) error {
	wantElevation, wantObstruction, err := astrodomeTerrainSkylineNodeInformation(profile, definition)
	if err != nil {
		return err
	}
	if !sameNullableFloat(node.TerrainSkylineElevationDegrees, wantElevation, 1e-12) ||
		node.TerrainSkylineHasObstructionAtEvaluationDirection == nil ||
		*node.TerrainSkylineHasObstructionAtEvaluationDirection != wantObstruction {
		return errors.New("node terrain skyline information is inconsistent with the canonical GLO-30 profile")
	}
	if node.LimitingFactor == "terrain_skyline" {
		return errors.New("informational GLO-30 skyline cannot replace the atmospheric node state")
	}
	return nil
}

func validateAstrodomeCelestialTracks(version string, tracks []astronomy.CelestialTrack, validTimes []time.Time, location forecast.Location) error {
	if version != astronomy.CelestialEphemerisVersion {
		return fmt.Errorf("astrodome celestial ephemeris version must be %q", astronomy.CelestialEphemerisVersion)
	}
	return astronomy.ValidateCelestialTracksForLocation(tracks, validTimes, location)
}

func validateAstrodomeDirectionContract(dataset AstrodomeDataset) error {
	if dataset.DirectionCoordinate != forecast.AstrodomeDirectionCoordinate ||
		dataset.DirectionReferenceSurface != forecast.AstrodomeDirectionReferenceSurface ||
		dataset.DirectionReferenceWavelengthM != forecast.AstrodomeDirectionReferenceWavelengthM ||
		dataset.VacuumDirectionAvailable == nil || *dataset.VacuumDirectionAvailable {
		return errors.New("astrodome apparent-direction contract is incomplete")
	}
	return nil
}

func validateAstrodomeSourceColumnPlanContract(dataset AstrodomeDataset) error {
	if !validPrefixedSHA256(dataset.SourceColumnPlanDigest) {
		return errors.New("astrodome source-column plan digest is missing or invalid")
	}
	return nil
}

func validateAstrodomeScienceContract(dataset AstrodomeDataset) error {
	if dataset.ScienceVersion == forecast.AstrodomeScienceVersion &&
		dataset.SciencePathVersion == forecast.AstrodomeSciencePathContractVersion &&
		dataset.CalibrationVersion == forecast.AstrodomeScienceVersion &&
		validPrefixedSHA256(dataset.CalibrationSHA256) {
		return nil
	}
	return errors.New("astrodome dataset uses an unsupported scientific contract")
}

func validateAstrodomeDatasetGrid(grid AstrodomeDatasetGrid, profile forecast.AstrodomeGridProfile, frameCount int) error {
	if grid.FrameCount != frameCount || grid.NodeCount != profile.NodeCount() || len(grid.Rings) != len(profile.Rings) {
		return errors.New("astrodome dataset grid cardinality is invalid")
	}
	for index, expected := range profile.Rings {
		actual := grid.Rings[index]
		if math.Abs(actual.ElevationDegrees-expected.ElevationDegrees) > 1e-6 ||
			actual.AzimuthCount != expected.AzimuthCount ||
			math.Abs(actual.AzimuthStepDegrees-expected.AzimuthStepDegrees) > 1e-9 {
			return fmt.Errorf("astrodome dataset grid ring %d differs from the profile", index)
		}
	}
	if grid.Zenith.AzimuthDegrees != nil || math.Abs(grid.Zenith.ElevationDegrees-forecast.AstrodomeZenithElevationDegrees) > 1e-12 {
		return errors.New("astrodome dataset zenith must be one azimuth-free 90-degree node")
	}
	return nil
}

func validateAstrodomeDatasetNode(
	node AstrodomeDatasetNode,
	definition forecast.AstrodomeGridNode,
	rayGeometryVersion string,
	currentScience bool,
) error {
	if !sameNullableFloat(node.AzimuthDegrees, definition.AzimuthDegrees, 1e-7) ||
		!finiteDatasetNumber(node.ElevationDegrees) || math.Abs(node.ElevationDegrees-definition.ElevationDegrees) > 1e-6 {
		return errors.New("node is outside canonical grid order")
	}
	if err := validateAstrodomeNodeGeometry(node, rayGeometryVersion); err != nil {
		return err
	}
	if err := validateAstrodomeDatasetQuality(node.QualityComponents, node.DataQuality, currentScience); err != nil {
		return err
	}
	switch node.State {
	case AstrodomeDatasetStateUnavailable, AstrodomeDatasetStateTerrainBlocked:
		return validateUnavailableAstrodomeNode(node)
	case AstrodomeDatasetStateValid, AstrodomeDatasetStatePrecipitationVeto:
		return validateAvailableAstrodomeNode(node)
	default:
		return fmt.Errorf("unsupported astrodome node state %q", node.State)
	}
}

func validateUnavailableAstrodomeNode(node AstrodomeDatasetNode) error {
	values := []*float64{
		node.Overall, node.SeeingArcsec500NM, node.Tau0MS500NM, node.Tau0ConservativeMS500NM,
		node.IntegratedCn2, node.WindWeightedCn2, node.NominalCloudTransmission,
		node.ConservativeCloudTransmission, node.EffectiveCloudTransmission,
		node.CloudOpticalDepthLiquid, node.CloudOpticalDepthIce, node.SlantWaterVapourKgM2,
		node.PenaltyLossFraction, node.NumericalError.OverallAbsolute,
		node.NumericalError.TurbulenceIntegralRelative, node.NumericalError.IntegratedCn2Relative,
		node.NumericalError.WindWeightedCn2Relative, node.NumericalError.CloudTransmissionAbsolute,
	}
	for _, value := range values {
		if value != nil {
			return errors.New("unavailable astrodome node contains a derived numeric value")
		}
	}
	if node.Factors != nil || len(node.PenaltyContributions) != 0 || node.Tau0UnboundedAbove ||
		node.DataQuality != forecast.AstrodomeScienceQualityUnavailable || node.QualityComponents.MandatoryComplete {
		return errors.New("unavailable astrodome node has inconsistent factors or quality")
	}
	if node.State == AstrodomeDatasetStateTerrainBlocked {
		if node.TerrainObstructionSource != forecast.AstrodomeTerrainObstructionHHL || node.LimitingFactor != "coarse_terrain" {
			return errors.New("terrain-blocked node needs the ICON HHL terrain limiting factor")
		}
	} else if node.LimitingFactor != "unavailable_data" || node.TerrainObstructionSource != forecast.AstrodomeTerrainObstructionNone {
		return errors.New("unavailable node needs the unavailable-data limiting factor")
	}
	return nil
}

func validateAvailableAstrodomeNode(node AstrodomeDatasetNode) error {
	if node.TerrainObstructionSource != forecast.AstrodomeTerrainObstructionNone {
		return errors.New("available astrodome node cannot claim a terrain obstruction")
	}
	if node.Overall == nil || node.SeeingArcsec500NM == nil || node.IntegratedCn2 == nil ||
		node.WindWeightedCn2 == nil || node.NominalCloudTransmission == nil ||
		node.ConservativeCloudTransmission == nil || node.EffectiveCloudTransmission == nil ||
		node.CloudOpticalDepthLiquid == nil || node.CloudOpticalDepthIce == nil ||
		node.Factors == nil || node.PenaltyLossFraction == nil ||
		node.NumericalError.OverallAbsolute == nil || node.NumericalError.CloudTransmissionAbsolute == nil {
		return errors.New("available astrodome node is missing mandatory physical output")
	}
	if node.DataQuality == forecast.AstrodomeScienceQualityUnavailable || !node.QualityComponents.MandatoryComplete {
		return errors.New("available astrodome node has unavailable input quality")
	}
	checks := []struct {
		value        *float64
		minimum, max float64
	}{
		{node.Overall, 1, 10}, {node.SeeingArcsec500NM, 0, 60},
		{node.IntegratedCn2, 0, math.MaxFloat64}, {node.WindWeightedCn2, 0, math.MaxFloat64},
		{node.NominalCloudTransmission, 0, 1},
		{node.ConservativeCloudTransmission, 0, 1},
		{node.EffectiveCloudTransmission, 0, 1},
		{node.CloudOpticalDepthLiquid, 0, math.MaxFloat64}, {node.CloudOpticalDepthIce, 0, math.MaxFloat64},
		{node.SlantWaterVapourKgM2, 0, 1000}, {node.Tau0MS500NM, 0, 10000},
		{node.Tau0ConservativeMS500NM, 0, 10000}, {node.NumericalError.OverallAbsolute, 0, 10},
		{node.PenaltyLossFraction, 0, 1},
		{node.NumericalError.TurbulenceIntegralRelative, 0, 1},
		{node.NumericalError.IntegratedCn2Relative, 0, 1},
		{node.NumericalError.WindWeightedCn2Relative, 0, 1},
		{node.NumericalError.CloudTransmissionAbsolute, 0, 1},
	}
	for _, check := range checks {
		if check.value != nil && (!finiteDatasetNumber(*check.value) || *check.value < check.minimum || *check.value > check.max) {
			return errors.New("available astrodome node contains a non-finite or out-of-range value")
		}
	}
	if *node.ConservativeCloudTransmission > *node.NominalCloudTransmission+1e-15 ||
		math.Abs(*node.EffectiveCloudTransmission-*node.ConservativeCloudTransmission) > 1e-15 {
		return errors.New("astrodome nominal/conservative cloud transmission contract failed")
	}
	maximumTurbulenceResidual := maximumNullableDatasetNumber(
		node.NumericalError.IntegratedCn2Relative,
		node.NumericalError.WindWeightedCn2Relative,
	)
	if !sameNullableFloat(node.NumericalError.TurbulenceIntegralRelative, maximumTurbulenceResidual, 1e-12) {
		return errors.New("aggregate turbulence diagnostic does not match its component diagnostics")
	}
	if node.Tau0UnboundedAbove {
		if node.Tau0MS500NM != nil {
			return errors.New("unbounded tau0 must not contain a finite point value")
		}
	} else if node.Tau0MS500NM == nil {
		return errors.New("finite tau0 node is missing its point value")
	}
	factors := []float64{
		node.Factors.OpticalTurbulence, node.Factors.CloudObstruction, node.Factors.SurfaceWind,
		node.Factors.Fog, node.Factors.Precipitation,
	}
	product := 1.0
	for _, factor := range factors {
		if !finiteDatasetNumber(factor) || factor < 0 || factor > 1 {
			return errors.New("astrodome node contains an invalid factor")
		}
		product *= factor
	}
	if len(node.PenaltyContributions) != len(astrodomePenaltyKeys) {
		return errors.New("astrodome node needs all five Shapley contributions")
	}
	contributionTotal := 0.0
	for index, expectedKey := range astrodomePenaltyKeys {
		contribution := node.PenaltyContributions[index]
		if contribution.Key != expectedKey || !finiteDatasetNumber(contribution.LossFraction) ||
			contribution.LossFraction < 0 || contribution.LossFraction > 1 {
			return errors.New("astrodome Shapley contributions are not in canonical order")
		}
		contributionTotal += contribution.LossFraction
	}
	if math.Abs(contributionTotal-*node.PenaltyLossFraction) > 2e-6 ||
		math.Abs(product-(1-*node.PenaltyLossFraction)) > 2e-6 ||
		math.Abs(*node.Overall-(1+9*(1-*node.PenaltyLossFraction))) > 2e-5 {
		return errors.New("astrodome factor, Shapley, loss, and Overall closure failed")
	}
	if node.LimitingFactor != astrodomeLimitingFactor(node.PenaltyContributions) {
		return errors.New("astrodome limiting factor does not match its largest Shapley contribution")
	}
	if node.State == AstrodomeDatasetStatePrecipitationVeto {
		if node.Factors.Precipitation != 0 || math.Abs(*node.Overall-1) > 2e-5 {
			return errors.New("precipitation-veto node does not contain an exact veto")
		}
	} else if node.Factors.Precipitation == 0 {
		return errors.New("zero precipitation factor must use precipitation_veto state")
	}
	return nil
}

func validateAstrodomeDatasetQuality(
	quality AstrodomeDatasetQuality,
	category forecast.AstrodomeScienceQualityCategory,
	currentScience bool,
) error {
	bounded := []float64{
		quality.LeadTimeQualityHeuristic, quality.GeometryCoverage, quality.TurbulencePathCoverage,
		quality.CloudPathCoverage, quality.QuadratureConvergence, quality.TopClosure,
	}
	for _, value := range bounded {
		if !finiteDatasetNumber(value) || value < 0 || value > 1 {
			return errors.New("astrodome quality component is outside [0,1]")
		}
	}
	if quality.HumidityPathCoverage != nil && (!finiteDatasetNumber(*quality.HumidityPathCoverage) ||
		*quality.HumidityPathCoverage < 0 || *quality.HumidityPathCoverage > 1) {
		return errors.New("astrodome humidity quality is outside [0,1]")
	}
	if !finiteDatasetNumber(quality.TemporalResolutionHours) || quality.TemporalResolutionHours <= 0 || quality.TemporalResolutionHours > 24 ||
		(quality.QuadratureConvergence != 0 && quality.QuadratureConvergence != 1) ||
		(quality.TopClosure != 0 && quality.TopClosure != 1) {
		return errors.New("astrodome temporal/convergence quality is invalid")
	}
	if currentScience && quality.ApproximationLengthM == nil {
		return errors.New("current astrodome quality is missing approximation_length_m")
	}
	approximationLengthM := 0.0
	if quality.ApproximationLengthM != nil {
		approximationLengthM = *quality.ApproximationLengthM
		if !finiteDatasetNumber(approximationLengthM) || approximationLengthM < 0 ||
			approximationLengthM > forecast.AstrodomeScienceMaximumApproximatePathLengthM {
			return errors.New("astrodome approximation length is invalid")
		}
	}
	switch category {
	case forecast.AstrodomeScienceQualityUnavailable, forecast.AstrodomeScienceQualityLimited,
		forecast.AstrodomeScienceQualityUsable, forecast.AstrodomeScienceQualityGood:
	default:
		return fmt.Errorf("unsupported astrodome quality category %q", category)
	}
	if (category == forecast.AstrodomeScienceQualityUnavailable) == quality.MandatoryComplete {
		return errors.New("astrodome mandatory-completeness flag contradicts its quality category")
	}
	seen := make(map[string]struct{}, len(quality.ReasonCodes))
	for _, reason := range quality.ReasonCodes {
		if !astrodomeVersionPattern.MatchString(reason) {
			return errors.New("astrodome quality reason code is invalid")
		}
		if _, exists := seen[reason]; exists {
			return errors.New("astrodome quality reason code is duplicated")
		}
		seen[reason] = struct{}{}
	}
	_, hasApproximationReason := seen["short_path_approximation"]
	if approximationLengthM > 0 {
		if !hasApproximationReason ||
			(quality.MandatoryComplete &&
				(category != forecast.AstrodomeScienceQualityLimited || quality.QuadratureConvergence != 0)) {
			return errors.New("approximated astrodome node needs limited quality and an explicit reason")
		}
	} else if hasApproximationReason {
		return errors.New("astrodome approximation reason has zero path length")
	}
	return nil
}

func validateAstrodomeGeometryVersions(
	rayGeometryVersion,
	refractionVersion,
	refractivityVersion string,
) error {
	switch rayGeometryVersion {
	case forecast.AstrodomeRefractionGeometryVersion:
		if refractionVersion != forecast.AstrodomeRefractionIntegratorVersion ||
			refractivityVersion != forecast.AstrodomeCiddorVersion {
			return errors.New("full-refraction astrodome versions are inconsistent")
		}
	case forecast.AstrodomeGeometryVersion:
		if refractionVersion != "not-applicable" || refractivityVersion != "not-applicable" {
			return errors.New("straight-compat astrodome must not claim refraction versions")
		}
	default:
		return errors.New("astrodome ray geometry version is unsupported")
	}
	return nil
}

func validateAstrodomeNodeGeometry(node AstrodomeDatasetNode, rayGeometryVersion string) error {
	switch node.GeometryMode {
	case forecast.AstrodomeScienceGeometryStraight:
		if rayGeometryVersion != forecast.AstrodomeGeometryVersion || node.DirectionAtModelTopECEF != nil {
			return errors.New("straight-compat node has inconsistent geometry provenance")
		}
	case forecast.AstrodomeScienceGeometryRefractionFull:
		if rayGeometryVersion != forecast.AstrodomeRefractionGeometryVersion {
			return errors.New("full-refraction node has inconsistent geometry provenance")
		}
		if node.DirectionAtModelTopECEF == nil {
			if node.State == AstrodomeDatasetStateUnavailable || node.State == AstrodomeDatasetStateTerrainBlocked {
				return nil
			}
			return errors.New("full-refraction node is missing its model-top tangent")
		}
		direction := node.DirectionAtModelTopECEF
		if !finiteDatasetNumber(direction.X) || !finiteDatasetNumber(direction.Y) || !finiteDatasetNumber(direction.Z) ||
			math.Abs(math.Sqrt(direction.X*direction.X+direction.Y*direction.Y+direction.Z*direction.Z)-1) > 1e-8 {
			return errors.New("full-refraction model-top tangent is not a finite unit ECEF vector")
		}
	default:
		return errors.New("astrodome node geometry mode is unsupported")
	}
	return nil
}

func validateAstrodomeDatasetLocation(location AstrodomeDatasetLocation, requireTimeZone, requireElevation bool) error {
	if !finiteBetween(location.Latitude, -90, 90) || !finiteBetween(location.Longitude, -180, 180) {
		return errors.New("coordinates are invalid")
	}
	if requireTimeZone {
		if strings.TrimSpace(location.TimeZone) == "" {
			return errors.New("time zone is required")
		}
		if _, err := time.LoadLocation(location.TimeZone); err != nil {
			return errors.New("time zone is invalid")
		}
	} else if location.TimeZone != "" {
		return errors.New("model location must not carry a presentation time zone")
	}
	if requireElevation && location.SurfaceElevationM == nil {
		return errors.New("model surface elevation is required")
	}
	if location.SurfaceElevationM != nil && (!finiteDatasetNumber(*location.SurfaceElevationM) ||
		*location.SurfaceElevationM < -500 || *location.SurfaceElevationM > 9000) {
		return errors.New("surface elevation is invalid")
	}
	return nil
}

func validateAstrodomeSurfaceCommon(surface AstrodomeDatasetSurfaceCommon) error {
	values := []float64{
		surface.WindSpeed10MMS, surface.WindGust10MMS,
		surface.PrecipitationRateMMPerHour, surface.ForecastLeadHours,
	}
	for _, value := range values {
		if !finiteDatasetNumber(value) || value < 0 {
			return errors.New("surface input must be finite and non-negative")
		}
	}
	switch surface.FogHeuristic {
	case forecast.AstrodomeScienceFogUnavailable, forecast.AstrodomeScienceFogNone,
		forecast.AstrodomeScienceFogPossible, forecast.AstrodomeScienceFogHigh:
		return nil
	default:
		return errors.New("surface fog state is invalid")
	}
}

func validPrefixedSHA256(value string) bool {
	return value == strings.ToLower(value) && strings.HasPrefix(value, "sha256:") &&
		validSHA256Hex(strings.TrimPrefix(value, "sha256:"))
}

func finiteDatasetNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func maximumNullableDatasetNumber(values ...*float64) *float64 {
	var maximum *float64
	for _, value := range values {
		if value == nil {
			continue
		}
		if maximum == nil || *value > *maximum {
			maximum = floatPointer(*value)
		}
	}
	return maximum
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

func wholeUTCHour(value time.Time) bool {
	return !value.IsZero() && isUTC(value) && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}

func astrodomeIdentityEqual(first, second forecast.AstrodomePrimitiveVolumeIdentity) bool {
	return first.Provider == second.Provider && first.Product == second.Product && first.Grid == second.Grid &&
		first.RunID == second.RunID && first.RunBaseTime.Equal(second.RunBaseTime) &&
		first.RunManifestDigest == second.RunManifestDigest && first.InputContractVersion == second.InputContractVersion
}
