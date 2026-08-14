package directional

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AstrodomeDatasetComputer is the only acquisition/calculation dependency of
// the canonical runner. A concrete implementation may use an in-process
// immutable primitive volume or an isolated worker adapter without changing
// the DTO, publication, or coordinator contracts.
type AstrodomeDatasetComputer interface {
	ComputeAstrodomeDataset(context.Context, SourceIdentity, json.RawMessage) (AstrodomeDatasetInput, error)
}

type AstrodomeDatasetComputerFunc func(context.Context, SourceIdentity, json.RawMessage) (AstrodomeDatasetInput, error)

func (function AstrodomeDatasetComputerFunc) ComputeAstrodomeDataset(ctx context.Context, source SourceIdentity, payload json.RawMessage) (AstrodomeDatasetInput, error) {
	return function(ctx, source, payload)
}

// NewAstrodomeDatasetRunner returns a coordinator runner that validates and
// losslessly gzips the language-neutral JSON into its private workspace. The
// coordinator remains responsible for hashing and atomic publication.
func NewAstrodomeDatasetRunner(computer AstrodomeDatasetComputer) (Runner, error) {
	if computer == nil {
		return nil, errors.New("astrodome dataset computer is required")
	}
	return RunnerFunc(func(ctx context.Context, execution Execution) (RunnerResult, error) {
		if execution.Kind != KindAstrodome {
			return RunnerResult{}, CodedError{Code: "invalid_kind", Err: errors.New("astrodome runner received another calculation kind")}
		}
		input, err := computer.ComputeAstrodomeDataset(ctx, execution.Source, append(json.RawMessage(nil), execution.Payload...))
		if err != nil {
			return RunnerResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return RunnerResult{}, err
		}
		admissionScienceCacheKey := strings.TrimSpace(input.AdmissionScienceCacheKey)
		if admissionScienceCacheKey == "" {
			admissionScienceCacheKey = execution.ScienceCacheKey
		}
		finalScienceCacheKey := strings.TrimSpace(input.FinalScienceCacheKey)
		if finalScienceCacheKey == "" {
			finalScienceCacheKey = execution.ScienceCacheKey
		}
		if admissionScienceCacheKey != execution.ScienceCacheKey {
			return RunnerResult{}, CodedError{Code: "science_identity_mismatch", Err: errors.New("astrodome calculation returned inconsistent science identities")}
		}
		dataset, err := BuildAstrodomeDataset(input)
		if err != nil {
			return RunnerResult{}, CodedError{Code: "invalid_dataset", Err: err}
		}
		if dataset.RunID != execution.Source.RunID || dataset.GridProfile != execution.Source.GridProfile ||
			dataset.GridGeometryDigest != execution.Source.GeometryDigest || execution.Source.Provider != "icon-eu" {
			return RunnerResult{}, CodedError{Code: "source_mismatch", Err: errors.New("astrodome dataset differs from the pinned source")}
		}
		encoded, err := EncodeAstrodomeDataset(dataset)
		if err != nil {
			return RunnerResult{}, CodedError{Code: "invalid_dataset", Err: err}
		}
		destination := filepath.Join(execution.Workspace, "astrodome.json.gz")
		if err := writeGzipAstrodomeDataset(ctx, destination, encoded); err != nil {
			return RunnerResult{}, err
		}
		return RunnerResult{
			DatasetPath: destination, FinalScienceCacheKey: finalScienceCacheKey,
			Provider: execution.Source.Provider, RunID: execution.Source.RunID,
			GridProfile: execution.Source.GridProfile, GeometryDigest: execution.Source.GeometryDigest,
			ContentEncoding: "gzip",
		}, nil
	}), nil
}

func writeGzipAstrodomeDataset(ctx context.Context, destination string, encoded []byte) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create astrodome dataset: %w", err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(destination)
		}
	}()
	compressed, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		_ = file.Close()
		return err
	}
	_, copyErr := io.Copy(compressed, contextReader{ctx: ctx, reader: bytes.NewReader(encoded)})
	closeGzipErr := compressed.Close()
	syncErr := error(nil)
	if copyErr == nil && closeGzipErr == nil {
		syncErr = file.Sync()
	}
	closeFileErr := file.Close()
	if err := errors.Join(copyErr, closeGzipErr, syncErr, closeFileErr, ctx.Err()); err != nil {
		return fmt.Errorf("write astrodome dataset: %w", err)
	}
	remove = false
	return nil
}
