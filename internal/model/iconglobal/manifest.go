package iconglobal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bot_astrosferum/internal/model"
)

const manifestSchemaVersion = 1

type StepFile struct {
	ForecastHour int       `json:"forecast_hour"`
	ValidAt      time.Time `json:"valid_at"`
	File         string    `json:"file"`
	Bytes        int64     `json:"bytes"`
	SHA256       string    `json:"sha256"`
	Messages     int       `json:"messages"`
}

type BundleFile struct {
	File     string `json:"file"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Messages int    `json:"messages"`
}

type Manifest struct {
	SchemaVersion    int            `json:"schema_version"`
	Provider         string         `json:"provider"`
	Product          string         `json:"product"`
	RunID            string         `json:"run_id"`
	BaseTime         time.Time      `json:"base_time"`
	PublishedAt      time.Time      `json:"published_at"`
	Grid             model.Coverage `json:"grid"`
	PressureSteps    []StepFile     `json:"pressure_steps"`
	SurfaceSteps     []StepFile     `json:"surface_steps"`
	CloudVariables   []string       `json:"cloud_variables,omitempty"`
	CloudModelLevels []int          `json:"cloud_model_levels,omitempty"`
	CloudPublishedAt *time.Time     `json:"cloud_published_at,omitempty"`
	CloudGeometry    *BundleFile    `json:"cloud_geometry,omitempty"`
	CloudSteps       []StepFile     `json:"cloud_steps,omitempty"`
	Complete         bool           `json:"complete"`
}

type LoadedManifest struct {
	Manifest
	Directory string `json:"-"`
}

func LoadCurrent(dataRoot string) (LoadedManifest, error) {
	directory, err := filepath.EvalSymlinks(filepath.Join(dataRoot, "models", "icon-global", "current"))
	if err != nil {
		return LoadedManifest{}, fmt.Errorf("resolve current ICON Global run: %w", err)
	}
	return LoadManifest(filepath.Join(directory, "manifest.json"))
}

func LoadManifest(path string) (LoadedManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return LoadedManifest{}, fmt.Errorf("open ICON Global manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	var manifest Manifest
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return LoadedManifest{}, fmt.Errorf("decode ICON Global manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return LoadedManifest{}, err
	}
	return LoadedManifest{Manifest: manifest, Directory: filepath.Dir(path)}, nil
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != manifestSchemaVersion || manifest.Provider != "icon-global" || manifest.Product == "" || manifest.RunID == "" {
		return fmt.Errorf("invalid ICON Global manifest identity")
	}
	if manifest.BaseTime.IsZero() || manifest.PublishedAt.IsZero() || !manifest.Complete {
		return fmt.Errorf("ICON Global manifest is incomplete")
	}
	if len(manifest.PressureSteps) != 25 || len(manifest.SurfaceSteps) != 79 {
		return fmt.Errorf("ICON Global manifest has incomplete forecast steps")
	}
	for index, step := range manifest.PressureSteps {
		if step.ForecastHour != index*3 || invalidStep(step) {
			return fmt.Errorf("ICON Global pressure step %d is incomplete", index)
		}
	}
	for index, step := range manifest.SurfaceSteps {
		if step.ForecastHour != index || invalidStep(step) {
			return fmt.Errorf("ICON Global surface step %d is incomplete", index)
		}
	}
	hasCloudMetadata := len(manifest.CloudVariables) > 0 || len(manifest.CloudModelLevels) > 0 || manifest.CloudPublishedAt != nil
	hasCloudFiles := manifest.CloudGeometry != nil || len(manifest.CloudSteps) > 0
	if hasCloudMetadata || hasCloudFiles {
		if len(manifest.CloudVariables) == 0 || len(manifest.CloudModelLevels) < 2 || manifest.CloudPublishedAt == nil || manifest.CloudPublishedAt.IsZero() || manifest.CloudGeometry == nil || len(manifest.CloudSteps) != 79 {
			return fmt.Errorf("ICON Global manifest has a partial hourly cloud publication")
		}
		if manifest.CloudGeometry.File == "" || manifest.CloudGeometry.Bytes <= 0 || manifest.CloudGeometry.Messages <= 0 || manifest.CloudGeometry.SHA256 == "" || invalidRelativePath(manifest.CloudGeometry.File) {
			return fmt.Errorf("ICON Global manifest cloud geometry is incomplete")
		}
		for index, step := range manifest.CloudSteps {
			if step.ForecastHour != index || invalidStep(step) {
				return fmt.Errorf("ICON Global cloud step %d is incomplete", index)
			}
		}
	}
	return nil
}

func invalidStep(step StepFile) bool {
	if step.ValidAt.IsZero() || step.File == "" || step.Bytes <= 0 || step.Messages <= 0 || step.SHA256 == "" {
		return true
	}
	return invalidRelativePath(step.File)
}

func invalidRelativePath(path string) bool {
	return filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func (manifest Manifest) HasHourlyCloud() bool {
	if len(manifest.CloudVariables) != 9 || len(manifest.CloudModelLevels) != len(globalCloudModelLevels) || manifest.CloudPublishedAt == nil || manifest.CloudGeometry == nil || len(manifest.CloudSteps) != 79 {
		return false
	}
	if manifest.CloudGeometry.Messages != len(globalCloudGeometryLevels()) {
		return false
	}
	for _, step := range manifest.CloudSteps {
		if step.Messages != globalCloudStepMessageCount(step.ForecastHour) {
			return false
		}
	}
	return true
}
