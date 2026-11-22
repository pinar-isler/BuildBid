// Package extractionbench reports offline extraction quality and operational
// telemetry on the frozen BuildBid corpus.
package extractionbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pinar-isler/BuildBid/validation/extraction"
)

const (
	Development = "development"
	HeldOut     = "held-out"
	Both        = "both"
)

type Options struct {
	CorpusDir string
	Schemas   fs.FS
	Split     string
	// Predictions contains a complete recorded bundle. Nil selects mock replay.
	Predictions []byte
}

type Document struct {
	FixtureID                string                 `json:"fixtureId"`
	Split                    string                 `json:"split"`
	Kind                     extraction.Kind        `json:"documentKind"`
	SourceFormat             string                 `json:"sourceFormat"`
	ExpectedOutcome          string                 `json:"expectedOutcome"`
	Status                   extraction.Status      `json:"status"`
	ErrorCodes               []string               `json:"errorCodes"`
	UnavailableFieldPointers []string               `json:"unavailableFieldPointers"`
	Telemetry                map[string]json.Number `json:"telemetry"`
	CanonicalSchemaValid     *bool                  `json:"canonicalSchemaValid,omitempty"`
	// Quality holds a CompleteScore or IncompleteScore on caller-declared
	// live records. It is absent entirely on mock documents.
	Quality any `json:"quality,omitempty"`
}

type Summary struct {
	Operations Operations `json:"operations"`
	Quality    *Quality   `json:"quality"`
}

type Report struct {
	Source                string                     `json:"source"`
	Provider              *string                    `json:"provider"`
	Model                 *string                    `json:"model"`
	CorpusVersion         int                        `json:"corpusVersion"`
	Split                 string                     `json:"split"`
	IndependentEvaluation bool                       `json:"independentEvaluation"`
	ManifestSHA256        string                     `json:"manifestSha256"`
	SchemaSHA256          map[extraction.Kind]string `json:"schemaSha256"`
	LatencySource         string                     `json:"latencySource"`
	Summary
	Breakdowns map[string]map[string]Summary `json:"breakdowns"`
	Documents  []Document                    `json:"documents"`
}

// Evaluate reads local evidence only. The source label of a recorded run is
// declared by its caller and is not independently verified.
func Evaluate(ctx context.Context, options Options) (Report, error) {
	return evaluate(ctx, options, time.Now)
}

func evaluate(ctx context.Context, options Options, now func() time.Time) (Report, error) {
	splits := []string{options.Split}
	switch options.Split {
	case Both, "":
		options.Split = Both
		splits = []string{Development, HeldOut}
	case Development, HeldOut:
	default:
		return Report{}, errors.New("Choose development, held-out, or both.")
	}
	if options.Schemas == nil {
		return Report{}, errors.New("A local canonical schema filesystem is required.")
	}
	corpus := os.DirFS(options.CorpusDir)
	manifestBytes, err := fs.ReadFile(corpus, "manifest.json")
	if err != nil {
		return Report{}, err
	}
	if _, err := decodeJSON(manifestBytes); err != nil {
		return Report{}, err
	}
	var entries manifest
	if err := json.Unmarshal(manifestBytes, &entries); err != nil {
		return Report{}, err
	}
	knownIDs := make(map[string]bool)
	for _, group := range entries.Splits {
		for _, entry := range group.Documents {
			knownIDs[entry.ID] = true
		}
	}
	mock := options.Predictions == nil
	var bundle predictionBundle
	if mock {
		provider := "fixture_replay"
		bundle = predictionBundle{source: MockSource, provider: &provider}
	} else {
		bundle, err = readBundle(options.Predictions, knownIDs)
		if err != nil {
			return Report{}, err
		}
	}
	validators, err := extraction.LoadValidators(options.Schemas)
	if err != nil {
		return Report{}, err
	}
	var pipeline *extraction.Pipeline
	latencySource := "recorded_prediction_metadata"
	if mock {
		provider, err := extraction.NewMockProvider(corpus)
		if err != nil {
			return Report{}, err
		}
		pipeline, err = extraction.NewPipeline(provider, validators, extraction.DefaultMaxBytes)
		if err != nil {
			return Report{}, err
		}
		latencySource = "local_mock_pipeline"
	}
	schemaHashes := make(map[extraction.Kind]string)
	scoring := make(map[extraction.Kind]scoringSchema)
	for _, file := range []struct {
		kind extraction.Kind
		name string
	}{{extraction.Quotation, "Vendor_Quotation_Schema"}, {extraction.TitleDeed, "Title_deed_Schema"}} {
		schema, digest, err := readScoringSchema(options.Schemas, file.kind, file.name)
		if err != nil {
			return Report{}, err
		}
		scoring[file.kind], schemaHashes[file.kind] = schema, digest
	}
	report := Report{
		Source: bundle.source, Provider: bundle.provider, Model: bundle.model, CorpusVersion: entries.Version,
		Split: options.Split, ManifestSHA256: hash(manifestBytes), SchemaSHA256: schemaHashes,
		LatencySource: latencySource, Documents: []Document{},
	}
	for _, split := range splits {
		group, ok := entries.Splits[split]
		if !ok {
			return Report{}, fmt.Errorf("Corpus manifest is missing the %s split.", split)
		}
		for _, entry := range group.Documents {
			if err := ctx.Err(); err != nil {
				return Report{}, err
			}
			var doc Document
			if pipeline != nil {
				start := now()
				result := pipeline.Run(ctx, filepath.Join(options.CorpusDir, split, "documents", entry.File), entry.Kind)
				elapsed, err := decimalInt(now().Sub(start).Nanoseconds()).quo(decimalInt(1_000_000))
				if err != nil {
					return Report{}, err
				}
				doc = documentResult(split, entry, result, map[string]json.Number{"latencyMs": json.Number(elapsed.String())})
			} else {
				record, exists := bundle.records[entry.ID]
				result, raw := recordedResult(validators, entry.Kind, record, exists)
				doc = documentResult(split, entry, result, record.telemetry)
				if bundle.source == LiveSource {
					if err := scoreDocument(&doc, corpus, entry, result, raw, validators, scoring[entry.Kind]); err != nil {
						return Report{}, err
					}
				}
			}
			report.Documents = append(report.Documents, doc)
		}
	}
	report.Summary, err = summarize(report.Documents, bundle.source)
	if err != nil {
		return Report{}, err
	}
	report.Breakdowns, err = buildBreakdowns(report.Documents, bundle.source)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

func hash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func recordedResult(validators *extraction.Validators, kind extraction.Kind, record prediction, exists bool) (extraction.Result, any) {
	if !exists {
		return recordFailure(kind, "missing_prediction", "The fixture has no recorded prediction."), nil
	}
	if record.payload == nil {
		return recordFailure(kind, "no_response", "The recorded attempt produced no response text."), nil
	}
	raw, err := decodeJSON([]byte(*record.payload))
	if err != nil {
		return recordFailure(kind, "invalid_json", "The provider returned invalid or ambiguous JSON."), nil
	}
	if !record.validReasons {
		return recordFailure(kind, "invalid_provider_response", "Unavailable reasons must identify missing fields and contain text."), raw
	}
	result := validators.ValidateResponse(kind, extraction.ProviderResponse{
		PayloadJSON: *record.payload, UnavailableReasons: record.reasons,
	})
	return result, raw
}

func recordFailure(kind extraction.Kind, code, message string) extraction.Result {
	return extraction.Result{
		Status: extraction.Failed, Kind: kind, Errors: []extraction.ExtractionError{{Code: code, Message: message}},
	}
}

func documentResult(split string, entry manifestDocument, result extraction.Result, telemetry map[string]json.Number) Document {
	if telemetry == nil {
		telemetry = map[string]json.Number{}
	}
	doc := Document{
		FixtureID: entry.ID, Split: split, Kind: entry.Kind, SourceFormat: strings.TrimPrefix(path.Ext(entry.File), "."),
		ExpectedOutcome: entry.Outcome, Status: result.Status, ErrorCodes: []string{},
		UnavailableFieldPointers: []string{}, Telemetry: telemetry,
	}
	for _, issue := range result.Errors {
		doc.ErrorCodes = append(doc.ErrorCodes, issue.Code)
	}
	slices.Sort(doc.ErrorCodes)
	doc.ErrorCodes = slices.Compact(doc.ErrorCodes)
	for _, field := range result.UnavailableFields {
		doc.UnavailableFieldPointers = append(doc.UnavailableFieldPointers, field.Pointer)
	}
	return doc
}

func scoreDocument(doc *Document, corpus fs.FS, entry manifestDocument, result extraction.Result, raw any,
	validators *extraction.Validators, schema scoringSchema) error {
	if entry.Outcome != "complete" {
		doc.Quality = scoreIncomplete(entry, result)
		return nil
	}
	data, err := fs.ReadFile(corpus, path.Join(doc.Split, "ground-truth", entry.ID+".json"))
	if err != nil {
		return err
	}
	value, err := decodeJSON(data)
	if err != nil {
		return err
	}
	violations, err := validators.ValidateCanonical(entry.Kind, value)
	if err != nil {
		return err
	}
	if len(violations) > 0 {
		return fmt.Errorf("Ground truth for %s fails canonical validation.", entry.ID)
	}
	schemaValid := false
	if raw != nil {
		violations, err := validators.ValidateCanonical(entry.Kind, raw)
		if err != nil {
			return err
		}
		schemaValid = len(violations) == 0
	}
	doc.CanonicalSchemaValid = &schemaValid
	score, err := scoreComplete(value.(map[string]any), result, schema)
	if err != nil {
		return err
	}
	doc.Quality = score
	return nil
}

func summarize(documents []Document, source string) (Summary, error) {
	operations, err := summarizeOperations(documents)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{Operations: operations}
	if source == LiveSource {
		summary.Quality, err = summarizeQuality(documents)
	}
	return summary, err
}

func buildBreakdowns(documents []Document, source string) (map[string]map[string]Summary, error) {
	breakdowns := make(map[string]map[string]Summary)
	for _, field := range []struct {
		name string
		key  func(Document) string
	}{
		{"split", func(doc Document) string { return doc.Split }},
		{"documentKind", func(doc Document) string { return string(doc.Kind) }},
		{"sourceFormat", func(doc Document) string { return doc.SourceFormat }},
	} {
		groups := make(map[string][]Document)
		for _, doc := range documents {
			key := field.key(doc)
			groups[key] = append(groups[key], doc)
		}
		summaries := make(map[string]Summary, len(groups))
		for _, key := range slices.Sorted(maps.Keys(groups)) {
			summary, err := summarize(groups[key], source)
			if err != nil {
				return nil, err
			}
			summaries[key] = summary
		}
		breakdowns[field.name] = summaries
	}
	return breakdowns, nil
}
