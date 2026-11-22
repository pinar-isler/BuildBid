package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func save(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "predictions.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal("unable to save the test bundle")
	}
	return path
}

func TestCLIEmitsJSONAndConsoleFromRecordedAndMockInputs(t *testing.T) {
	path := save(t, `{"source":"live","predictions":{"Q-DEV-01":{"payloadJson":"{broken"}}}`)
	code, output, stderr := invoke(t, "--split", "development", "--predictions", path, "--format", "json")
	raw, err := strictjson.Decode([]byte(output))
	if code != 0 || err != nil || stderr != "" {
		t.Fatalf("recorded JSON invocation failed with exit %d", code)
	}
	report := raw.(map[string]any)
	ops := report["operations"].(map[string]any)
	quality := report["quality"].(map[string]any)
	if ops["invalidJsonRate"].(map[string]any)["count"] != json.Number("1") ||
		quality["fieldAccuracy"].(map[string]any)["count"] != json.Number("0") {
		t.Fatal("wrong recorded report counters")
	}
	code, output, stderr = invoke(t, "--split", "development", "--mock")
	if code != 0 || stderr != "" || !strings.Contains(output, "quality metrics are suppressed") ||
		strings.Contains(output, "Field accuracy") {
		t.Fatal("mock console report did not suppress quality")
	}
}

func TestCLIRejectsDuplicateOrNonfiniteBundleMetadata(t *testing.T) {
	for _, data := range []string{
		`{"source":"mock","source":"live","predictions":{}}`,
		`{"source":"mock","predictions":{"Q-DEV-01":{"latencyMs":NaN}}}`,
		`{"source":"mock","predictions":{},"provider":"\ud800"}`,
		"null", "{} []", "",
	} {
		code, output, stderr := invoke(t, "--predictions", save(t, data), "--format", "json")
		if code != 2 || output != "" || stderr == "" || strings.Contains(stderr, "Traceback") {
			t.Fatal("invalid bundle generated an unsafe or successful report")
		}
	}
}

func TestCLIRejectsConflictingConfigurationAndDecimalOverflow(t *testing.T) {
	path := save(t, `{"source":"live","predictions":{
		"Q-DEV-01":{"costUsd":9e999999999999999999},
		"Q-DEV-02":{"costUsd":9e999999999999999999}
	}}`)
	for _, args := range [][]string{
		nil, {"--mock", "--predictions", path}, {"--mock", "--split", "unknown"}, {"--mock", "--split", ""},
		{"--mock", "--format", "text"}, {"--predictions", filepath.Join(t.TempDir(), "missing.json")},
	} {
		code, output, stderr := invoke(t, args...)
		if code != 2 || output != "" || stderr == "" {
			t.Fatal("invalid options generated a report")
		}
	}
	code, output, stderr := invoke(t, "--predictions", path, "--format", "json")
	if code != 2 || output != "" || !strings.Contains(stderr, "decimal arithmetic range") {
		t.Fatal("arithmetic overflow did not fail safely")
	}
}
