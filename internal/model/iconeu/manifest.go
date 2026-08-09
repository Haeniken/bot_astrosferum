package iconeu

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bot_astrosferum/internal/model"
)

const (
	ManifestSchemaVersion      = 1
	SurfaceLegacySchemaVersion = 17
	SurfaceBundleSchemaVersion = 19
	HourlySurfaceStepCount     = 79 // ICON-EU is hourly through forecast hour +78.
)

type Manifest struct {
	SchemaVersion      int               `json:"schema_version"`
	Provider           string            `json:"provider"`
	Product            string            `json:"product"`
	RunID              string            `json:"run_id"`
	BaseTime           time.Time         `json:"base_time"`
	PublishedAt        time.Time         `json:"published_at"`
	Grid               model.Coverage    `json:"grid"`
	PressureLevelsHPA  []float64         `json:"pressure_levels_hpa"`
	Variables          []string          `json:"variables"`
	SurfaceVariables   []string          `json:"surface_variables,omitempty"`
	SurfacePublishedAt *time.Time        `json:"surface_published_at,omitempty"`
	SurfaceSteps       []SurfaceStepFile `json:"surface_steps,omitempty"`
	CloudVariables     []string          `json:"cloud_variables,omitempty"`
	CloudModelLevels   []int             `json:"cloud_model_levels,omitempty"`
	CloudPublishedAt   *time.Time        `json:"cloud_published_at,omitempty"`
	CloudGeometry      *BundleFile       `json:"cloud_geometry,omitempty"`
	CloudSteps         []SurfaceStepFile `json:"cloud_steps,omitempty"`
	Steps              []StepFile        `json:"steps"`
	Complete           bool              `json:"complete"`
}

type StepFile struct {
	ForecastHour int         `json:"forecast_hour"`
	ValidAt      time.Time   `json:"valid_at"`
	File         string      `json:"file"`
	Bytes        int64       `json:"bytes"`
	SHA256       string      `json:"sha256"`
	Messages     int         `json:"messages"`
	Surface      *BundleFile `json:"surface,omitempty"`
}

type BundleFile struct {
	File     string `json:"file"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Messages int    `json:"messages"`
}

type SurfaceStepFile struct {
	ForecastHour int       `json:"forecast_hour"`
	ValidAt      time.Time `json:"valid_at"`
	File         string    `json:"file"`
	Bytes        int64     `json:"bytes"`
	SHA256       string    `json:"sha256"`
	Messages     int       `json:"messages"`
}

type LoadedManifest struct {
	Manifest
	Directory string `json:"-"`
}

func LoadCurrent(dataRoot string) (LoadedManifest, error) {
	current := filepath.Join(dataRoot, "models", "icon-eu", "current")
	directory, err := filepath.EvalSymlinks(current)
	if err != nil {
		return LoadedManifest{}, fmt.Errorf("resolve current ICON-EU run: %w", err)
	}
	return LoadManifest(filepath.Join(directory, "manifest.json"))
}

func LoadManifest(path string) (LoadedManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return LoadedManifest{}, fmt.Errorf("open ICON-EU manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	var manifest Manifest
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return LoadedManifest{}, fmt.Errorf("decode ICON-EU manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return LoadedManifest{}, err
	}
	return LoadedManifest{Manifest: manifest, Directory: filepath.Dir(path)}, nil
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported ICON-EU manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.Provider != "icon-eu" || manifest.Product == "" || manifest.RunID == "" {
		return fmt.Errorf("invalid ICON-EU manifest identity")
	}
	if manifest.BaseTime.IsZero() || manifest.PublishedAt.IsZero() || !manifest.Complete {
		return fmt.Errorf("ICON-EU manifest is incomplete")
	}
	if len(manifest.PressureLevelsHPA) < 2 || len(manifest.Steps) < 2 {
		return fmt.Errorf("ICON-EU manifest has insufficient levels or steps")
	}
	surfaceSteps := 0
	for index, step := range manifest.Steps {
		if step.File == "" || step.ValidAt.IsZero() || step.Bytes <= 0 || step.Messages <= 0 || step.SHA256 == "" {
			return fmt.Errorf("ICON-EU manifest step %d is incomplete", index)
		}
		if filepath.IsAbs(step.File) || filepath.Clean(step.File) != step.File || step.File == ".." || strings.HasPrefix(step.File, ".."+string(filepath.Separator)) {
			return fmt.Errorf("ICON-EU manifest step %d has unsafe path", index)
		}
		if step.Surface != nil {
			surfaceSteps++
			if step.Surface.File == "" || step.Surface.Bytes <= 0 || step.Surface.Messages <= 0 || step.Surface.SHA256 == "" {
				return fmt.Errorf("ICON-EU manifest surface step %d is incomplete", index)
			}
			if unsafeRelativePath(step.Surface.File) {
				return fmt.Errorf("ICON-EU manifest surface step %d has unsafe path", index)
			}
		}
	}
	for index, step := range manifest.SurfaceSteps {
		if step.ForecastHour != index || step.ValidAt.IsZero() || step.File == "" || step.Bytes <= 0 || step.Messages <= 0 || step.SHA256 == "" {
			return fmt.Errorf("ICON-EU manifest hourly surface step %d is incomplete", index)
		}
		if unsafeRelativePath(step.File) {
			return fmt.Errorf("ICON-EU manifest hourly surface step %d has unsafe path", index)
		}
	}
	hasSurfaceMetadata := len(manifest.SurfaceVariables) > 0 || manifest.SurfacePublishedAt != nil
	hasSurfaceFiles := surfaceSteps > 0 || len(manifest.SurfaceSteps) > 0
	if hasSurfaceMetadata || hasSurfaceFiles {
		metadataComplete := len(manifest.SurfaceVariables) > 0 && manifest.SurfacePublishedAt != nil && !manifest.SurfacePublishedAt.IsZero()
		legacyComplete := surfaceSteps == len(manifest.Steps) && len(manifest.SurfaceSteps) == 0
		hourlyComplete := surfaceSteps == 0 && len(manifest.SurfaceSteps) == HourlySurfaceStepCount
		if !metadataComplete || (!legacyComplete && !hourlyComplete) {
			return fmt.Errorf("ICON-EU manifest has a partial surface publication")
		}
	}
	hasCloudMetadata := len(manifest.CloudVariables) > 0 || len(manifest.CloudModelLevels) > 0 || manifest.CloudPublishedAt != nil
	hasCloudFiles := manifest.CloudGeometry != nil || len(manifest.CloudSteps) > 0
	if hasCloudMetadata || hasCloudFiles {
		if len(manifest.CloudVariables) == 0 || len(manifest.CloudModelLevels) < 2 || manifest.CloudPublishedAt == nil || manifest.CloudPublishedAt.IsZero() || manifest.CloudGeometry == nil || len(manifest.CloudSteps) != HourlySurfaceStepCount {
			return fmt.Errorf("ICON-EU manifest has a partial hourly cloud publication")
		}
		if manifest.CloudGeometry.File == "" || manifest.CloudGeometry.Bytes <= 0 || manifest.CloudGeometry.Messages <= 0 || manifest.CloudGeometry.SHA256 == "" || unsafeRelativePath(manifest.CloudGeometry.File) {
			return fmt.Errorf("ICON-EU manifest cloud geometry is incomplete")
		}
		for index, step := range manifest.CloudSteps {
			if step.ForecastHour != index || step.ValidAt.IsZero() || step.File == "" || step.Bytes <= 0 || step.Messages <= 0 || step.SHA256 == "" || unsafeRelativePath(step.File) {
				return fmt.Errorf("ICON-EU manifest cloud step %d is incomplete", index)
			}
		}
	}
	return nil
}

func (manifest Manifest) HasSurface() bool {
	if len(manifest.SurfaceVariables) == 0 || manifest.SurfacePublishedAt == nil {
		return false
	}
	if len(manifest.SurfaceSteps) > 0 {
		return len(manifest.SurfaceSteps) == HourlySurfaceStepCount
	}
	for _, step := range manifest.Steps {
		if step.Surface == nil {
			return false
		}
	}
	return true
}

func (manifest Manifest) HasHourlySurface() bool {
	if !manifest.HasSurface() || len(manifest.SurfaceSteps) != HourlySurfaceStepCount ||
		!hasOperationalSurfaceVariables(manifest.SurfaceVariables) {
		return false
	}
	for _, step := range manifest.SurfaceSteps {
		if step.Messages != len(manifest.SurfaceVariables) || step.Messages < SurfaceLegacySchemaVersion {
			return false
		}
	}
	return true
}

// HasAstrodomeSurface is deliberately stricter than HasHourlySurface. An
// already published legacy bundle stays usable by ordinary forecasts and
// Horizon while PS/QV_2M are downloaded and atomically published, but it must
// never be admitted as the lower boundary of an Astrodome refraction field.
func (manifest Manifest) HasAstrodomeSurface() bool {
	if !manifest.HasHourlySurface() || !hasAllSurfaceVariables(manifest.SurfaceVariables) {
		return false
	}
	for _, step := range manifest.SurfaceSteps {
		if step.Messages != SurfaceBundleSchemaVersion {
			return false
		}
	}
	return true
}

func (manifest Manifest) HasWindHeights() bool {
	seen := make(map[string]bool, len(manifest.Variables))
	for _, variable := range manifest.Variables {
		seen[variable] = true
	}
	if !seen["u"] || !seen["v"] || !seen["z"] {
		return false
	}
	for _, step := range manifest.Steps {
		if step.Messages != len(DefaultPressureLevelsHPA)*3 {
			return false
		}
	}
	return true
}

func (manifest Manifest) HasWindThermodynamics() bool {
	seen := make(map[string]bool, len(manifest.Variables))
	for _, variable := range manifest.Variables {
		seen[variable] = true
	}
	if !seen["u"] || !seen["v"] || !seen["z"] || !seen["t"] {
		return false
	}
	for _, step := range manifest.Steps {
		if step.Messages != len(DefaultPressureLevelsHPA)*4 {
			return false
		}
	}
	return true
}

func (manifest Manifest) HasHourlyCloud() bool {
	seen := make(map[string]bool, len(manifest.CloudVariables))
	for _, variable := range manifest.CloudVariables {
		seen[variable] = true
	}
	if !seen["ccl"] || !seen["pres"] || !seen["qc"] || !seen["qi"] ||
		!seen["t"] || !seen["u"] || !seen["v"] || !seen["tke"] || !seen["HHL"] ||
		len(manifest.CloudModelLevels) != len(DefaultCloudModelLevels) || manifest.CloudPublishedAt == nil || manifest.CloudGeometry == nil || len(manifest.CloudSteps) != HourlySurfaceStepCount {
		return false
	}
	for index, level := range DefaultCloudModelLevels {
		if manifest.CloudModelLevels[index] != level {
			return false
		}
	}
	if manifest.CloudGeometry.Messages != len(cloudGeometryLevels()) {
		return false
	}
	for _, step := range manifest.CloudSteps {
		if step.Messages != cloudStepMessageCount() {
			return false
		}
	}
	return true
}

func (manifest Manifest) SurfaceForecastSteps() []SurfaceStepFile {
	if len(manifest.SurfaceSteps) > 0 {
		return manifest.SurfaceSteps
	}
	steps := make([]SurfaceStepFile, 0, len(manifest.Steps))
	for _, step := range manifest.Steps {
		if step.Surface == nil {
			return nil
		}
		steps = append(steps, SurfaceStepFile{
			ForecastHour: step.ForecastHour, ValidAt: step.ValidAt, File: step.Surface.File,
			Bytes: step.Surface.Bytes, SHA256: step.Surface.SHA256, Messages: step.Surface.Messages,
		})
	}
	return steps
}

func unsafeRelativePath(path string) bool {
	return filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}
