package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

const sampleResponse = `{"vendorName":"Sample vendor","quotationDate":"2026-05-19","currency":"TRY",
	"extractedItems":[{"rawDescription":"A sample row","quantity":2,"unitOfMeasure":"m3",
	"quotedUnitPrice":0,"isTaxIncluded":false}]}`

func fixturePath(t *testing.T, split, file string) string {
	t.Helper()
	name := filepath.Join("../../corpus/v1", split, "documents", file)
	abs, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func write(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runJSON(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), args, &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected diagnostic: %s", stderr.String())
	}
	return code, decodeEnvelope(t, stdout.Bytes())
}

func decodeEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	value, err := strictjson.Decode(raw)
	if err != nil {
		t.Fatalf("CLI returned invalid JSON: %s (%v)", raw, err)
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 5 {
		t.Fatalf("CLI returned an invalid envelope: %v", value)
	}
	for _, name := range []string{"status", "documentKind", "payload", "unavailableFields", "errors"} {
		if _, exists := object[name]; !exists {
			t.Fatalf("missing envelope field %q", name)
		}
	}
	return object
}

func requireCLIError(t *testing.T, exit int, result map[string]any, code, pointer string) {
	t.Helper()
	if exit != 2 || result["status"] != "failed" || result["payload"] != nil {
		t.Fatalf("want a failed envelope and exit 2, got %d %v", exit, result)
	}
	issues, ok := result["errors"].([]any)
	if !ok || len(issues) == 0 {
		t.Fatalf("failed envelope has no issue: %v", result)
	}
	issue := issues[0].(map[string]any)
	if issue["code"] != code || issue["pointer"] != pointer {
		t.Fatalf("issue = %v, want %q at %q", issue, code, pointer)
	}
}

func TestCLIEmitsJSONAndTheExpectedExitStatus(t *testing.T) {
	for _, entry := range []struct {
		document, kind, status string
		exit                   int
	}{
		{fixturePath(t, "development", "Q-DEV-01.pdf"), "quotation", "complete", 0},
		{fixturePath(t, "development", "Q-DEV-03.pdf"), "quotation", "needs_review", 1},
		{fixturePath(t, "held-out", "D-HOLD-03.png"), "title_deed", "needs_review", 1},
		{filepath.Join(t.TempDir(), "missing.png"), "title_deed", "failed", 2},
	} {
		t.Run(filepath.Base(entry.document), func(t *testing.T) {
			exit, result := runJSON(t, entry.document, "--kind", entry.kind)
			if exit != entry.exit || result["status"] != entry.status || result["documentKind"] != entry.kind {
				t.Fatalf("outcome mismatch: %d %v", exit, result)
			}
			if entry.exit == 0 {
				first := result["payload"].(map[string]any)["extractedItems"].([]any)[0].(map[string]any)
				if first["quantity"] != json.Number("1250.50") {
					t.Fatalf("quantity spelling changed: %v", first)
				}
			}
		})
	}
}

func TestExplicitResponseSupportsUnknownDocumentsAndOverridesReplay(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewGray(image.Rect(0, 0, 1, 2)), nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	unknown := write(t, dir, "unknown.jpeg", jpg.Bytes())
	exit, result := runJSON(t, unknown, "--kind", "quotation")
	requireCLIError(t, exit, result, "provider_error", "")
	response := write(t, dir, "response.json", []byte(sampleResponse))
	want, err := strictjson.Decode([]byte(sampleResponse))
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []string{unknown, fixturePath(t, "development", "Q-DEV-01.pdf")} {
		exit, result := runJSON(t, "--mock-response", response, document, "--kind=quotation")
		if exit != 0 || !reflect.DeepEqual(result["payload"], want) {
			t.Fatalf("saved response did not override replay: %d %v", exit, result)
		}
	}
}

func TestIncompleteExplicitResponseUsesTheReviewPipeline(t *testing.T) {
	raw := strings.Replace(sampleResponse, `"unitOfMeasure":"m3",`, "", 1)
	raw = strings.Replace(raw, `"quotedUnitPrice":0`, `"quotedUnitPrice":null`, 1)
	response := write(t, t.TempDir(), "partial.json", []byte(raw))
	exit, result := runJSON(t, fixturePath(t, "development", "Q-DEV-01.pdf"), "--kind", "quotation", "--mock-response", response)
	want, err := strictjson.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if exit != 1 || result["status"] != "needs_review" || !reflect.DeepEqual(result["payload"], want) {
		t.Fatalf("unexpected incomplete result: %d %v", exit, result)
	}
	fields := result["unavailableFields"].([]any)
	pointers := []string{"/extractedItems/0/unitOfMeasure", "/extractedItems/0/quotedUnitPrice"}
	if len(fields) != len(pointers) {
		t.Fatalf("unexpected missing fields: %v", fields)
	}
	for i, field := range fields {
		field := field.(map[string]any)
		if field["pointer"] != pointers[i] || field["reason"] != "The provider did not supply a value." {
			t.Fatalf("unexpected missing field: %v", field)
		}
	}
}

func TestInvalidExplicitResponsesUseStrictJSONAndSchemaValidation(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct{ raw, code, pointer string }{
		{`{"unfinished":`, "invalid_json", ""},
		{`{"vendorName":"First","vendorName":"Second"}`, "invalid_json", ""},
		{strings.Replace(sampleResponse, "2026-05-19", "2026-02-30", 1), "schema_violation", "/quotationDate"},
	} {
		response := write(t, dir, "invalid.json", []byte(test.raw))
		exit, result := runJSON(t, fixturePath(t, "development", "Q-DEV-01.pdf"), "--kind", "quotation", "--mock-response", response)
		requireCLIError(t, exit, result, test.code, test.pointer)
	}
}

func TestExplicitResponseStillRequiresReadableFilesAndValidMedia(t *testing.T) {
	dir := t.TempDir()
	document := fixturePath(t, "development", "Q-DEV-01.pdf")
	for _, response := range []string{filepath.Join(dir, "missing.json"), dir,
		write(t, dir, "invalid-encoding.json", []byte{0xff}), ""} {
		exit, result := runJSON(t, document, "--kind", "quotation", "--mock-response="+response)
		requireCLIError(t, exit, result, "provider_error", "")
	}
	response := write(t, dir, "response.json", []byte(sampleResponse))
	for _, test := range []struct{ document, code string }{
		{filepath.Join(dir, "missing.pdf"), "input_unreadable"},
		{write(t, dir, "broken.png", []byte("broken")), "invalid_media"},
	} {
		exit, result := runJSON(t, test.document, "--kind", "quotation", "--mock-response", response)
		requireCLIError(t, exit, result, test.code, "")
	}
}

func TestCLIProcessWorksWithEmptyEnvironment(t *testing.T) {
	response := write(t, t.TempDir(), "response.json", []byte(sampleResponse))
	quote := fixturePath(t, "development", "Q-DEV-01.pdf")
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range [][]string{nil, {"--mock-response", response}} {
		base := []string{"-test.run=^TestCLIHelperProcess$", "--", quote, "--kind", "quotation", "--repo-root", root}
		cmd := exec.CommandContext(t.Context(), executable, append(base, opts...)...)
		cmd.Env = []string{}
		cmd.Dir = t.TempDir()
		output, err := cmd.CombinedOutput()
		if err != nil || decodeEnvelope(t, output)["status"] != string(extraction.Complete) {
			t.Fatalf("offline CLI failed: %s %v", output, err)
		}
	}
}

func TestCLIHelperProcess(t *testing.T) {
	if i := slices.Index(os.Args, "--"); i >= 0 {
		os.Exit(run(context.Background(), os.Args[i+1:], os.Stdout, os.Stderr))
	}
}

func TestCLIUsageAndSetupErrorsAreSafe(t *testing.T) {
	for _, args := range [][]string{
		nil, {"test.pdf"}, {"test.pdf", "--kind", "private-kind"},
		{"test.pdf", "--kind", "quotation", "--mock-response"},
		{"test.pdf", "--kind", "quotation", "--private-option"},
		{"test.pdf", "--kind", "quotation", "--repo-root", filepath.Join(t.TempDir(), "private-path")},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(t.Context(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 ||
			stderr.Len() == 0 || strings.Contains(stderr.String(), "private-") {
			t.Fatalf("unsafe usage or setup error: %d %s %s", code, stdout.String(), stderr.String())
		}
	}
}
