package directional

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"bot_astrosferum/internal/forecast"
)

func TestAstrodomeDatasetRunnerPublishesStrictGzipFromComputerInterface(t *testing.T) {
	t.Parallel()
	input := completeAstrodomeDatasetInput(t, forecast.AstrodomeGridSparseStorageV1)
	digest, err := input.Profile.GeometryDigest()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"point":{"latitude":59.9,"longitude":30.2}}`)
	computer := AstrodomeDatasetComputerFunc(func(_ context.Context, source SourceIdentity, received json.RawMessage) (AstrodomeDatasetInput, error) {
		if source.RunID != input.SourceIdentity.RunID || !bytes.Equal(received, payload) {
			t.Fatalf("computer received a different immutable request: source=%+v payload=%s", source, received)
		}
		received[0] = 'x'
		return input, nil
	})
	runner, err := NewAstrodomeDatasetRunner(computer)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	result, err := runner.Run(context.Background(), Execution{
		JobID: "job_runner_contract_test", Kind: KindAstrodome,
		Source: SourceIdentity{
			Provider: "icon-eu", RunID: input.SourceIdentity.RunID,
			GridProfile: string(input.Profile.ID), GeometryDigest: digest,
		},
		Payload: payload, Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if payload[0] != '{' {
		t.Fatal("computer mutated the coordinator payload snapshot")
	}
	if result.ContentEncoding != "gzip" || result.Provider != "icon-eu" || result.GeometryDigest != digest {
		t.Fatalf("result metadata = %+v", result)
	}
	file, err := os.Open(result.DatasetPath)
	if err != nil {
		t.Fatal(err)
	}
	decompressor, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(decompressor)
	closeErr := decompressor.Close()
	fileErr := file.Close()
	if err != nil || closeErr != nil || fileErr != nil {
		t.Fatalf("read gzip: read=%v gzip-close=%v file-close=%v", err, closeErr, fileErr)
	}
	dataset, err := DecodeAstrodomeDataset(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeAstrodomeDataset: %v", err)
	}
	if len(dataset.Frames) != forecast.AstrodomeFrameCount || dataset.Grid.NodeCount != input.Profile.NodeCount() {
		t.Fatalf("decoded dimensions = %d frames x %d nodes", len(dataset.Frames), dataset.Grid.NodeCount)
	}
}
