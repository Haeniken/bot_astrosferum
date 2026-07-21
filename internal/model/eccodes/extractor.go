package eccodes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

var chosenCellPattern = regexp.MustCompile(`Grid Point chosen #[0-9]+ index=([0-9]+) latitude=([^ ]+) longitude=([^ ]+) distance=([^ ]+)`)

type Runner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

type CommandRunner struct{}

func (CommandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Extractor struct {
	Runner Runner
}

func NewExtractor() Extractor {
	return Extractor{Runner: CommandRunner{}}
}

func (e Extractor) Extract(ctx context.Context, file string, latitude, longitude float64) (model.GridCell, model.Sample, error) {
	if err := forecast.ValidateCoordinates(latitude, longitude); err != nil {
		return model.GridCell{}, model.Sample{}, err
	}
	absolute, err := validateGRIBFile(file)
	if err != nil {
		return model.GridCell{}, model.Sample{}, err
	}
	if e.Runner == nil {
		return model.GridCell{}, model.Sample{}, fmt.Errorf("ecCodes runner is nil")
	}

	coordinates := strconv.FormatFloat(latitude, 'f', 6, 64) + "," +
		strconv.FormatFloat(longitude, 'f', 6, 64) + ",1"
	cellOutput, err := e.Runner.CombinedOutput(ctx, "grib_ls", "-l", coordinates, absolute)
	if err != nil {
		return model.GridCell{}, model.Sample{}, commandError("grib_ls", cellOutput, err)
	}
	cell, err := parseCell(string(cellOutput))
	if err != nil {
		return model.GridCell{}, model.Sample{}, err
	}

	sampleOutput, err := e.Runner.CombinedOutput(ctx, "grib_get", "-f", "-F", "%.10g",
		"-p", "shortName,typeOfLevel,level,step,stepUnits:s,stepRange,units",
		"-l", coordinates, absolute)
	if err != nil {
		return model.GridCell{}, model.Sample{}, commandError("grib_get", sampleOutput, err)
	}
	sample, err := parseSample(absolute, string(sampleOutput))
	if err != nil {
		return model.GridCell{}, model.Sample{}, err
	}
	return cell, sample, nil
}

func validateGRIBFile(file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("GRIB file path is empty")
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("resolve GRIB path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat GRIB file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("GRIB path is not a regular file: %s", absolute)
	}
	return absolute, nil
}

func parseCell(output string) (model.GridCell, error) {
	match := chosenCellPattern.FindStringSubmatch(output)
	if len(match) != 5 {
		return model.GridCell{}, fmt.Errorf("grib_ls did not report a chosen grid cell")
	}
	index, err := strconv.Atoi(match[1])
	if err != nil {
		return model.GridCell{}, fmt.Errorf("parse grid index: %w", err)
	}
	latitude, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return model.GridCell{}, fmt.Errorf("parse grid latitude: %w", err)
	}
	longitude, err := strconv.ParseFloat(match[3], 64)
	if err != nil {
		return model.GridCell{}, fmt.Errorf("parse grid longitude: %w", err)
	}
	distance, err := strconv.ParseFloat(match[4], 64)
	if err != nil {
		return model.GridCell{}, fmt.Errorf("parse grid distance: %w", err)
	}
	return model.GridCell{Index: index, Latitude: latitude, Longitude: longitude, DistanceKM: distance}, nil
}

func parseSample(file, output string) (model.Sample, error) {
	fields := strings.Fields(output)
	if len(fields) < 8 {
		return model.Sample{}, fmt.Errorf("unexpected grib_get output: %q", strings.TrimSpace(output))
	}
	level, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return model.Sample{}, fmt.Errorf("parse GRIB level: %w", err)
	}
	step, err := strconv.Atoi(fields[3])
	if err != nil {
		return model.Sample{}, fmt.Errorf("parse GRIB step: %w", err)
	}
	value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	if err != nil {
		return model.Sample{}, fmt.Errorf("parse GRIB value: %w", err)
	}
	return model.Sample{
		File:        filepath.Base(file),
		ShortName:   fields[0],
		TypeOfLevel: fields[1],
		Level:       level,
		Step:        step,
		StepUnits:   fields[4],
		StepRange:   fields[5],
		Units:       strings.Join(fields[6:len(fields)-1], " "),
		Value:       value,
	}, nil
}

func commandError(name string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if len(detail) > 500 {
		detail = detail[:500] + "…"
	}
	if detail == "" {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return fmt.Errorf("%s failed: %w: %s", name, err, detail)
}
