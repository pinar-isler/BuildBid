package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	matchingbench "github.com/pinar-isler/BuildBid/validation/benchmark/matching"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func successfulReport(t *testing.T, args ...string) string {
	t.Helper()
	code, output, diagnostic := invoke(t, args...)
	if code != 0 || output == "" || diagnostic != "" {
		t.Fatalf("expected report, got code %d and diagnostic %q", code, diagnostic)
	}
	return output
}

func TestCLISplitSelection(t *testing.T) {
	t.Chdir(filepath.Join(repoRoot(t), "validation"))
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"development", []string{"--split", "development"}, []string{"development"}},
		{"held-out", []string{"--split", "held-out"}, []string{"held-out"}},
		{"both", []string{"--split", "both"}, []string{"development", "held-out"}},
		{"default", nil, []string{"development", "held-out"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := []byte(successfulReport(t, test.args...))
			reports := make([]matchingbench.Report, len(test.want))
			var err error
			if len(test.want) == 1 {
				err = json.Unmarshal(output, &reports[0])
			} else {
				err = json.Unmarshal(output, &reports)
			}
			if err != nil || len(reports) != len(test.want) {
				t.Fatalf("unexpected report shape, got %d splits and decode error %v", len(reports), err)
			}
			for i, report := range reports {
				phase := "development"
				if test.want[i] == "held-out" {
					phase = "post_adjustment_regression"
				}
				if report.Split != test.want[i] || report.EvaluationPhase != phase ||
					report.IndependentEvaluation || len(report.Rows) != 8 {
					t.Fatalf("unexpected provenance or row count for %q", test.want[i])
				}
			}
		})
	}
}

func TestCLIDiscoversCorpusFromRootAndValidation(t *testing.T) {
	root := repoRoot(t)
	t.Chdir(root)
	rootOutput := successfulReport(t)
	t.Chdir(filepath.Join(root, "validation"))
	if validationOutput := successfulReport(t); validationOutput != rootOutput {
		t.Fatal("default corpus changed with the working directory")
	}
}

func TestCLIExplicitCorpusUsesGivenPath(t *testing.T) {
	root := repoRoot(t)
	t.Chdir(root)
	want := successfulReport(t)
	if got := successfulReport(t, "--corpus", "validation/corpus/v1"); got != want {
		t.Fatal("relative corpus path produced a different report")
	}
	// An explicit missing directory must fail even when discovery could succeed.
	code, output, diagnostic := invoke(t, "--corpus", t.TempDir())
	if code != 1 || output != "" || !strings.Contains(diagnostic, "manifest.json") {
		t.Fatalf("missing explicit corpus got code %d and diagnostic %q", code, diagnostic)
	}
	t.Chdir(t.TempDir())
	if got := successfulReport(t, "--corpus", filepath.Join(root, "validation", "corpus", "v1")); got != want {
		t.Fatal("absolute corpus path produced a different report")
	}
}

func TestCLIMissingDefaultCorpusIsRuntimeError(t *testing.T) {
	t.Chdir(t.TempDir())
	code, output, diagnostic := invoke(t, "--split", "development")
	if code != 1 || output != "" || !strings.Contains(diagnostic, "supply --corpus") {
		t.Fatalf("missing default corpus got code %d and diagnostic %q", code, diagnostic)
	}
}

func TestCLIInvalidArgumentsReturnUsageError(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown split", []string{"--split", "unknown"}, "split must be"},
		{"empty split", []string{"--split="}, "split must be"},
		{"missing split value", []string{"--split"}, "needs an argument"},
		{"unknown flag", []string{"--unknown"}, "flag provided but not defined"},
		{"positional argument", []string{"development"}, "takes no positional arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, output, diagnostic := invoke(t, test.args...)
			if code != 2 || output != "" || !strings.Contains(diagnostic, test.want) {
				t.Fatalf("invalid arguments got code %d and diagnostic %q", code, diagnostic)
			}
		})
	}
}
