package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeHistoricalRunV1RedactsLegacyPaths(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	run, historical, err := DecodeRunRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if !historical || !run.Historical {
		t.Fatal("historical marker missing")
	}
	if run.CompletionPath != "" {
		t.Fatalf("legacy paths leaked: %#v", run)
	}
	if run.Status != "succeeded" || run.ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("bad projection: %#v", run)
	}
}

func TestDecodeHistoricalRunV1RejectsUnknownAndTrailingData(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeRunRecord(append(data, []byte("{}")...)); err == nil {
		t.Fatal("trailing historical JSON accepted")
	}
	mutated := append([]byte{}, data...)
	mutated[len(mutated)-2] = ','
	mutated = append(mutated, []byte("\"unknown\":1}\n")...)
	if _, _, err := DecodeRunRecord(mutated); err == nil {
		t.Fatal("unknown historical field accepted")
	}
}
