package iconeu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const (
	DomeManifestSchemaVersion = 1
	DomeProductName           = "europe_regular-lat-lon-model-level-astrodome"
	domeFullLevelCount        = 74
	domeHalfLevelCount        = 75
	domeModelMessagesPerStep  = domeFullLevelCount*8 + domeHalfLevelCount*2
	domeBundleLayoutVersion   = "base-cloud-plus-dome-extension-v1"
)

var domeRunIDPattern = regexp.MustCompile(`^[0-9]{10}$`)

type DomeFieldStagger string

const (
	DomeStaggerFull DomeFieldStagger = "full-level"
	DomeStaggerHalf DomeFieldStagger = "half-level"
)

type DomeFieldSpec struct {
	Directory string           `json:"directory"`
	Code      string           `json:"code"`
	ShortName string           `json:"short_name"`
	Stagger   DomeFieldStagger `json:"stagger"`
}

// DomeModelFields is the exact native input inventory. Every returned slice
// is fresh so callers cannot mutate the versioned manifest contract.
func DomeModelFields() []DomeFieldSpec {
	return []DomeFieldSpec{
		{Directory: "p", Code: "P", ShortName: "pres", Stagger: DomeStaggerFull},
		{Directory: "t", Code: "T", ShortName: "t", Stagger: DomeStaggerFull},
		{Directory: "qv", Code: "QV", ShortName: "q", Stagger: DomeStaggerFull},
		{Directory: "qc", Code: "QC", ShortName: "qc", Stagger: DomeStaggerFull},
		{Directory: "qi", Code: "QI", ShortName: "qi", Stagger: DomeStaggerFull},
		{Directory: "clc", Code: "CLC", ShortName: "ccl", Stagger: DomeStaggerFull},
		{Directory: "u", Code: "U", ShortName: "u", Stagger: DomeStaggerFull},
		{Directory: "v", Code: "V", ShortName: "v", Stagger: DomeStaggerFull},
		{Directory: "w", Code: "W", ShortName: "wz", Stagger: DomeStaggerHalf},
		{Directory: "tke", Code: "TKE", ShortName: "tke", Stagger: DomeStaggerHalf},
	}
}

func DomeFullModelLevels() []int {
	levels := make([]int, domeFullLevelCount)
	for index := range levels {
		levels[index] = index + 1
	}
	return levels
}

func DomeHalfModelLevels() []int {
	levels := make([]int, domeHalfLevelCount)
	for index := range levels {
		levels[index] = index + 1
	}
	return levels
}

// DomeNativeForecastHours includes every hourly native term through f078 and
// the f081/f084 brackets. The Astrodome user product still contains exactly
// 72 valid times selected inside this immutable native interval.
func DomeNativeForecastHours() []int {
	hours := make([]int, 0, 81)
	for hour := 0; hour <= 78; hour++ {
		hours = append(hours, hour)
	}
	return append(hours, 81, 84)
}

// DomeInputContractDigest names the immutable acquisition layout before a
// run exists. It is distinct from the per-run manifest digest, which also
// binds concrete files and checksums.
func DomeInputContractDigest() string {
	contract := struct {
		SchemaVersion int             `json:"schema_version"`
		InputVersion  string          `json:"input_version"`
		FullLevels    []int           `json:"full_levels"`
		HalfLevels    []int           `json:"half_levels"`
		Fields        []DomeFieldSpec `json:"fields"`
		ForecastHours []int           `json:"forecast_hours"`
		SurfaceSchema int             `json:"surface_schema"`
		BundleLayout  string          `json:"bundle_layout"`
	}{
		SchemaVersion: DomeManifestSchemaVersion, InputVersion: forecast.AstrodomePrimitiveInputContractVersion,
		FullLevels: DomeFullModelLevels(), HalfLevels: DomeHalfModelLevels(), Fields: DomeModelFields(),
		ForecastHours: DomeNativeForecastHours(), SurfaceSchema: SurfaceBundleSchemaVersion,
		BundleLayout: domeBundleLayoutVersion,
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type DomeFileSource string

const (
	DomeFileSourceBaseRun DomeFileSource = "base-run"
	DomeFileSourceDomeRun DomeFileSource = "dome-run"
)

// DomeStepFile is a provider-root-relative immutable GRIB bundle reference.
// Base-run references reuse the existing cloud/surface acquisition; dome-run
// references contain only fields absent from that base contract.
type DomeStepFile struct {
	Source         DomeFileSource `json:"source"`
	ForecastHour   int            `json:"forecast_hour"`
	ValidAt        time.Time      `json:"valid_at"`
	File           string         `json:"file"`
	Bytes          int64          `json:"bytes"`
	AllocatedBytes int64          `json:"allocated_bytes"`
	SHA256         string         `json:"sha256"`
	Messages       int            `json:"messages"`
}

type DomeModelStep struct {
	ForecastHour int            `json:"forecast_hour"`
	ValidAt      time.Time      `json:"valid_at"`
	Parts        []DomeStepFile `json:"parts"`
	Messages     int            `json:"messages"`
}

type DomeManifest struct {
	SchemaVersion         int                  `json:"schema_version"`
	Provider              string               `json:"provider"`
	Product               string               `json:"product"`
	RunID                 string               `json:"run_id"`
	BaseTime              time.Time            `json:"base_time"`
	PublishedAt           time.Time            `json:"published_at"`
	BaseManifestSHA256    string               `json:"base_manifest_sha256"`
	InputContractVersion  string               `json:"input_contract_version"`
	InputContractSHA256   string               `json:"input_contract_sha256"`
	GridProfile           model.StorageProfile `json:"grid_profile"`
	Grid                  model.Coverage       `json:"grid"`
	FullModelLevels       []int                `json:"full_model_levels"`
	HalfModelLevels       []int                `json:"half_model_levels"`
	Fields                []DomeFieldSpec      `json:"fields"`
	Geometry              DomeStepFile         `json:"geometry"`
	ModelSteps            []DomeModelStep      `json:"model_steps"`
	SurfaceExtensionSteps []DomeStepFile       `json:"surface_extension_steps"`
	Complete              bool                 `json:"complete"`
}

type LoadedDomeManifest struct {
	DomeManifest
	Directory      string `json:"-"`
	ManifestSHA256 string `json:"-"`
}

func NewDomeManifest(runID string, baseTime time.Time, baseManifestSHA256 string) DomeManifest {
	return DomeManifest{
		SchemaVersion: DomeManifestSchemaVersion, Provider: "icon-eu", Product: DomeProductName,
		RunID: runID, BaseTime: baseTime.UTC(), BaseManifestSHA256: baseManifestSHA256,
		InputContractVersion: forecast.AstrodomePrimitiveInputContractVersion,
		InputContractSHA256:  DomeInputContractDigest(), GridProfile: model.StorageProfileDense,
		Grid: Coverage(), FullModelLevels: DomeFullModelLevels(), HalfModelLevels: DomeHalfModelLevels(),
		Fields: DomeModelFields(),
	}
}

func (manifest DomeManifest) Validate() error {
	if manifest.SchemaVersion != DomeManifestSchemaVersion || manifest.Provider != "icon-eu" || manifest.Product != DomeProductName {
		return errors.New("invalid ICON-EU Astrodome manifest identity")
	}
	if !domeRunIDPattern.MatchString(manifest.RunID) || manifest.BaseTime.IsZero() || manifest.BaseTime.Location() != time.UTC {
		return errors.New("invalid ICON-EU Astrodome run identity")
	}
	parsed, err := time.Parse("2006010215", manifest.RunID)
	if err != nil || !parsed.Equal(manifest.BaseTime) {
		return errors.New("ICON-EU Astrodome run ID does not match base time")
	}
	if manifest.PublishedAt.IsZero() || !manifest.Complete {
		return errors.New("ICON-EU Astrodome manifest is not published and complete")
	}
	if !validDomeDigest(manifest.BaseManifestSHA256) {
		return errors.New("ICON-EU Astrodome manifest has an invalid base-manifest digest")
	}
	if manifest.InputContractVersion != forecast.AstrodomePrimitiveInputContractVersion {
		return fmt.Errorf("unsupported Astrodome primitive contract %q", manifest.InputContractVersion)
	}
	if manifest.InputContractSHA256 != DomeInputContractDigest() {
		return errors.New("ICON-EU Astrodome manifest has an unexpected input-contract digest")
	}
	if manifest.GridProfile != model.StorageProfileDense && manifest.GridProfile != model.StorageProfileSparse {
		return fmt.Errorf("unsupported ICON-EU Astrodome grid profile %q", manifest.GridProfile)
	}
	if manifest.Grid != Coverage() {
		return errors.New("ICON-EU Astrodome manifest has an unexpected grid")
	}
	if !slices.Equal(manifest.FullModelLevels, DomeFullModelLevels()) || !slices.Equal(manifest.HalfModelLevels, DomeHalfModelLevels()) || !slices.Equal(manifest.Fields, DomeModelFields()) {
		return errors.New("ICON-EU Astrodome native field/level inventory differs from the versioned contract")
	}
	if err := validateDomeFile(manifest.Geometry, manifest.RunID, 0, manifest.BaseTime, domeHalfLevelCount, DomeFileSourceBaseRun, "geometry"); err != nil {
		return err
	}
	hours := DomeNativeForecastHours()
	if len(manifest.ModelSteps) != len(hours) {
		return fmt.Errorf("ICON-EU Astrodome manifest has %d model steps, want %d", len(manifest.ModelSteps), len(hours))
	}
	for index, hour := range hours {
		if err := validateDomeModelStep(manifest.ModelSteps[index], manifest.RunID, hour, manifest.BaseTime.Add(time.Duration(hour)*time.Hour)); err != nil {
			return err
		}
	}
	if len(manifest.SurfaceExtensionSteps) != 2 {
		return errors.New("ICON-EU Astrodome manifest needs surface brackets f081 and f084")
	}
	for index, hour := range []int{81, 84} {
		if err := validateDomeFile(manifest.SurfaceExtensionSteps[index], manifest.RunID, hour, manifest.BaseTime.Add(time.Duration(hour)*time.Hour), SurfaceBundleSchemaVersion, DomeFileSourceDomeRun, "surface extension"); err != nil {
			return err
		}
	}
	return nil
}

func validateDomeModelStep(step DomeModelStep, runID string, forecastHour int, validAt time.Time) error {
	if step.ForecastHour != forecastHour || !step.ValidAt.Equal(validAt) || step.Messages != domeModelMessagesPerStep {
		return fmt.Errorf("ICON-EU Astrodome model f%03d inventory is incomplete", forecastHour)
	}
	if forecastHour <= 78 {
		baseMessages := cloudStepMessageCount()
		if len(step.Parts) != 2 ||
			validateDomeFile(step.Parts[0], runID, forecastHour, validAt, baseMessages, DomeFileSourceBaseRun, "base model") != nil ||
			validateDomeFile(step.Parts[1], runID, forecastHour, validAt, domeModelMessagesPerStep-baseMessages, DomeFileSourceDomeRun, "model extension") != nil {
			return fmt.Errorf("ICON-EU Astrodome model f%03d must reuse the base cloud bundle and one extension", forecastHour)
		}
		return nil
	}
	if len(step.Parts) != 1 || validateDomeFile(step.Parts[0], runID, forecastHour, validAt, domeModelMessagesPerStep, DomeFileSourceDomeRun, "model bracket") != nil {
		return fmt.Errorf("ICON-EU Astrodome model f%03d needs one complete dome-run bracket", forecastHour)
	}
	return nil
}

func validateDomeFile(file DomeStepFile, runID string, forecastHour int, validAt time.Time, messages int, source DomeFileSource, kind string) error {
	wantPrefix := filepath.Join("dome-runs", runID) + string(filepath.Separator)
	if source == DomeFileSourceBaseRun {
		wantPrefix = filepath.Join("runs", runID) + string(filepath.Separator)
	}
	clean := filepath.Clean(file.File)
	if file.Source != source || file.ForecastHour != forecastHour || !file.ValidAt.Equal(validAt) || file.Bytes <= 0 || file.AllocatedBytes <= 0 ||
		file.Messages != messages || !validDomeDigest(file.SHA256) || unsafeRelativePath(file.File) || clean != file.File || !strings.HasPrefix(clean, wantPrefix) {
		return fmt.Errorf("ICON-EU Astrodome %s f%03d file is incomplete", kind, forecastHour)
	}
	return nil
}

func validDomeDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func LoadCurrentDomeManifest(dataRoot string) (LoadedDomeManifest, error) {
	current := filepath.Join(dataRoot, "models", "icon-eu", "dome-ready-current")
	directory, err := filepath.EvalSymlinks(current)
	if err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("resolve current ICON-EU Astrodome run: %w", err)
	}
	return LoadDomeManifest(filepath.Join(directory, "manifest.json"))
}

func LoadDomeManifest(path string) (LoadedDomeManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("open ICON-EU Astrodome manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(io.LimitReader(file, 16<<20), hash))
	decoder.DisallowUnknownFields()
	var manifest DomeManifest
	if err := decoder.Decode(&manifest); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("decode ICON-EU Astrodome manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return LoadedDomeManifest{}, errors.New("ICON-EU Astrodome manifest must contain one JSON value")
	}
	if err := manifest.Validate(); err != nil {
		return LoadedDomeManifest{}, err
	}
	return LoadedDomeManifest{
		DomeManifest: manifest, Directory: filepath.Dir(path), ManifestSHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func writeDomeManifest(path string, manifest DomeManifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary := path + fmt.Sprintf(".part-%d", time.Now().UnixNano())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	if err := syncDomeDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	failed = false
	return nil
}

func publishCurrentDomeManifest(dataRoot string, loaded LoadedDomeManifest) error {
	if err := loaded.Validate(); err != nil {
		return err
	}
	providerRoot := filepath.Join(dataRoot, "models", "icon-eu")
	relative, err := filepath.Rel(providerRoot, loaded.Directory)
	wantPrefix := filepath.Join("dome-runs", loaded.RunID) + string(filepath.Separator)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) || !strings.HasPrefix(relative, wantPrefix) {
		return errors.New("ICON-EU Astrodome run is outside the provider root")
	}
	temporary := filepath.Join(providerRoot, fmt.Sprintf(".dome-ready-current-%d", time.Now().UnixNano()))
	if err := os.Symlink(relative, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(providerRoot, "dome-ready-current")); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDomeDirectory(providerRoot)
}

func syncDomeDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
