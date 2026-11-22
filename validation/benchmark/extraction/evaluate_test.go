package extractionbench

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

func testOptions(split string, recorded []byte) Options {
	return Options{CorpusDir: corpusDir, Schemas: os.DirFS(schemaDir), Split: split, Predictions: recorded}
}

func reportFor(t *testing.T, split string, recorded []byte) Report {
	t.Helper()
	report, err := Evaluate(t.Context(), testOptions(split, recorded))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func reportJSON(t *testing.T, report Report) []byte {
	t.Helper()
	data, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMockRunsNeverScoreReplayOrSavedMockQuality(t *testing.T) {
	gold := testGold(t, "Q-DEV-01", Development)
	recorded := encode(t, map[string]any{"source": "mock", "predictions": map[string]any{
		"Q-DEV-01": map[string]any{"payloadJson": string(encode(t, gold))},
	}})
	replay := reportFor(t, Both, nil)
	for _, report := range []Report{replay, reportFor(t, Development, recorded)} {
		if report.Source != MockSource || report.Quality != nil || report.IndependentEvaluation ||
			strings.Contains(string(reportJSON(t, report)), "fieldAccuracy") || strings.Contains(ConsoleReport(report), "Field accuracy") {
			t.Fatal("mock results exposed quality or independent evidence")
		}
		for _, doc := range report.Documents {
			if doc.Quality != nil || doc.CanonicalSchemaValid != nil {
				t.Fatal("mock document has quality evidence")
			}
		}
		for _, breakdown := range report.Breakdowns {
			for _, summary := range breakdown {
				if summary.Quality != nil {
					t.Fatal("mock breakdown has quality evidence")
				}
			}
		}
	}
	if replay.Operations.Statuses != (StatusCounts{8, 2, 0}) ||
		replay.Operations.LatencyMs.ObservedDocuments != 10 || replay.Operations.APICostUSD.Total != nil {
		t.Fatal("unexpected mock replay results")
	}
}

func TestSavedRunCountsFailuresAndUsesOnlyRecordedTelemetry(t *testing.T) {
	invalidSchema := testGold(t, "Q-DEV-02", Development)
	invalidSchema["currency"], invalidSchema["vendorName"] = "GBP", "Private raw value"
	predictions := map[string]any{
		"Q-DEV-01": map[string]any{"payloadJson": `{"duplicate":1,"duplicate":2}`, "latencyMs": 300,
			"inputTokens": 100, "outputTokens": 3, "totalTokens": 103, "costUsd": json.Number("0.001")},
		"Q-DEV-02": map[string]any{"payloadJson": string(encode(t, invalidSchema)), "latencyMs": 100, "inputTokens": 200},
		"Q-DEV-03": map[string]any{"payloadJson": nil, "latencyMs": 200, "outputTokens": 0},
		"D-DEV-01": map[string]any{"payloadJson": string(encode(t, testGold(t, "D-DEV-01", Development)))},
	}
	report := reportFor(t, Development, encode(t, map[string]any{
		"source": "live", "provider": "test-provider", "predictions": predictions,
	}))
	ops := report.Operations
	if report.IndependentEvaluation || ops.Statuses != (StatusCounts{1, 0, 4}) ||
		ops.MissingPredictions != 1 || ops.NoResponses != 1 || ops.LatencyMs.ObservedDocuments != 3 {
		t.Fatal("incorrect recorded operational counts")
	}
	checkRate(t, ops.InvalidJSONRate, 1, 5)
	checkDecimal(t, ops.LatencyMs.Mean, "200")
	checkDecimal(t, ops.LatencyMs.P50, "200")
	checkDecimal(t, ops.LatencyMs.P95, "300")
	checkTotal(t, ops.Tokens["inputTokens"], 2, "300")
	checkTotal(t, ops.Tokens["outputTokens"], 2, "3")
	checkTotal(t, ops.Tokens["totalTokens"], 1, "103")
	checkTotal(t, ops.APICostUSD, 1, "0.001")
	checkRate(t, report.Quality.CompleteSchemaValidRate, 1, 4)
	checkRate(t, report.Quality.IncompleteReviewPassRate, 0, 1)
	if report.Breakdowns["documentKind"]["quotation"].Operations.Documents != 3 ||
		!strings.Contains(ConsoleReport(report), "Field accuracy") ||
		strings.Contains(string(reportJSON(t, report)), "Private raw value") {
		t.Fatal("report broke breakdowns, presentation, or payload privacy")
	}
}

func checkTotal(t *testing.T, got TelemetryTotal, count int, expected string) {
	t.Helper()
	if got.ObservedDocuments != count || got.Total == nil || got.Total.String() != expected {
		t.Fatalf("expected total %s across %d records, got %+v", expected, count, got)
	}
}

func TestMixedSourcesAndInvalidBundleMetadataAreRejected(t *testing.T) {
	for _, raw := range []string{
		`{"source":"unknown","predictions":{}}`,
		`{"source":"live","provider":"MockProvider","predictions":{}}`,
		`{"source":"live","provider":"fixture-replay","predictions":{}}`,
		`{"source":"live","provider":"validation.extraction.mock.MockProvider","predictions":{}}`,
		`{"source":"live","provider":"MOCK_RESPONSE_PROVIDER","predictions":{}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"source":"mock"}}}`,
		`{"source":"mock","predictions":{"Q-DEV-01":{"source":"live"}}}`,
		`{"source":"live","provider":"test-provider","predictions":{"Q-DEV-01":{"provider":"mock"}}}`,
		`{"source":"live","model":"test-model","predictions":{"Q-DEV-01":{"model":"other-model"}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"provider":"mock"}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"model":null}}}`,
		`{"source":"live","predictions":{"unknown-id":{}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"payloadJson":{}}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"inputTokens":true}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"inputTokens":1.0}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"inputTokens":1e0}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"inputTokens":-1}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"latencyMs":-1}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"costUsd":Infinity}}}`,
		`{"source":"live","predictions":{"Q-DEV-01":{"costUsd":"0"}}}`,
		`{"source":"mock","model":" \u001f ","predictions":{}}`,
		`{"source":"mock","provider":null,"predictions":{}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Evaluate(t.Context(), testOptions(Development, []byte(raw))); err == nil {
				t.Fatal("invalid bundle was accepted")
			}
		})
	}
}

func TestReportFormatsEscapeControlMetadata(t *testing.T) {
	report := reportFor(t, Development, []byte(`{"source":"mock","provider":"replay\nField accuracy  100%","model":"\u001b[2J\u007f\u009b","predictions":{}}`))
	console := ConsoleReport(report)
	if strings.Contains(console, "\nField accuracy") || !strings.Contains(console, `\nField accuracy`) ||
		report.Quality != nil {
		t.Fatal("mock label injected quality output")
	}
	for _, result := range []string{console, string(reportJSON(t, report))} {
		if strings.ContainsAny(result, "\x1b\x7f\u009b") || !strings.Contains(result, `\u001b`) ||
			!strings.Contains(result, `\u007f`) || !strings.Contains(result, `\u009b`) {
			t.Fatal("report contains unescaped terminal control metadata")
		}
	}
}

func findDocument(t *testing.T, report Report, id string) Document {
	t.Helper()
	for _, doc := range report.Documents {
		if doc.FixtureID == id {
			return doc
		}
	}
	t.Fatalf("missing fixture %s", id)
	return Document{}
}

func TestCanonicalValidityUsesRawJSONAndQualityUsesPipelineResult(t *testing.T) {
	placeholder := testGold(t, "Q-DEV-01", Development)
	placeholder["vendorName"] = " "
	report := reportFor(t, Development, encode(t, map[string]any{"source": "live", "predictions": map[string]any{
		"Q-DEV-01": map[string]any{"payloadJson": string(encode(t, placeholder))},
		"Q-DEV-02": map[string]any{"payloadJson": string(encode(t, testGold(t, "Q-DEV-02", Development))),
			"unavailableReasons": map[string]string{"/vendorName": "Conflicts with the observed name."}},
	}}))
	checkRate(t, report.Quality.CompleteSchemaValidRate, 2, 4)
	review, failed := findDocument(t, report, "Q-DEV-01"), findDocument(t, report, "Q-DEV-02")
	if review.Status != extraction.NeedsReview ||
		!slices.Equal(review.Quality.(*CompleteScore).MissingFieldPointers, []string{"/vendorName"}) ||
		failed.Status != extraction.Failed || failed.Quality.(*CompleteScore).Fields.Predicted != 0 ||
		!slices.Equal(failed.ErrorCodes, []string{"invalid_provider_response"}) {
		t.Fatal("raw validity and pipeline quality were conflated")
	}
}

func TestEmptyLiveBundleCountsAllGoldFieldsAndRowsAsMissing(t *testing.T) {
	report := reportFor(t, Development, []byte(`{"source":"live","predictions":{}}`))
	ops, quality := report.Operations, report.Quality
	if ops.MissingPredictions != 5 || ops.LatencyMs != (Latency{}) ||
		quality.Fields.Expected == 0 || quality.Fields.Missing != quality.Fields.Expected ||
		quality.Rows.Expected == 0 || quality.Rows.Missing != quality.Rows.Expected {
		t.Fatal("empty predictions did not count as missing")
	}
	checkRate(t, ops.InvalidJSONRate, 0, 5)
	for _, group := range []struct {
		expected int
		prf      PRF
	}{{quality.Fields.Expected, quality.Fields.PRF}, {quality.Rows.Expected, quality.Rows.PRF}} {
		checkRate(t, group.prf.Precision, 0, 0)
		checkRate(t, group.prf.Recall, 0, group.expected)
		checkRate(t, group.prf.F1, 0, group.expected)
	}
	for _, group := range quality.NumericErrors {
		if group.MeanAbsoluteError != nil {
			t.Fatal("missing comparisons produced numerical error")
		}
	}
}

func TestRecordedTelemetryPreservesZeroDecimalTotalsAndNearestRank(t *testing.T) {
	report := reportFor(t, Development, []byte(`{"source":"live","predictions":{
		"Q-DEV-01":{"latencyMs":0,"inputTokens":0,"costUsd":0.1},
		"Q-DEV-02":{"latencyMs":14.25,"outputTokens":0,"costUsd":0.2},
		"D-DEV-01":{"totalTokens":0,"costUsd":0}
	}}`))
	ops := report.Operations
	if ops.LatencyMs.ObservedDocuments != 2 {
		t.Fatal("latency zero was not retained")
	}
	checkDecimal(t, ops.LatencyMs.Mean, "7.125")
	checkDecimal(t, ops.LatencyMs.P50, "0")
	checkDecimal(t, ops.LatencyMs.P95, "14.25")
	checkTotal(t, ops.APICostUSD, 3, "0.3")
	for _, tokens := range ops.Tokens {
		checkTotal(t, tokens, 1, "0")
	}
}

func TestReportArithmeticPreservesLargeFiniteValuesAndPrecision(t *testing.T) {
	report := reportFor(t, Development, []byte(`{"source":"live","predictions":{
		"Q-DEV-01":{"latencyMs":1e1000000,"costUsd":1.00000000000000000000000001,"inputTokens":1000000000000000000000000000000000000000000000000000000000},
		"Q-DEV-02":{"costUsd":0.00000000000000000000000001,"inputTokens":1}
	}}`))
	checkDecimal(t, report.Operations.LatencyMs.Mean, "1e1000000")
	checkTotal(t, report.Operations.APICostUSD, 2, "1.00000000000000000000000002")
	checkTotal(t, report.Operations.Tokens["inputTokens"], 2, "1000000000000000000000000000000000000000000000000000000001")
}

func TestMockTimingConvertsNanosecondsToMilliseconds(t *testing.T) {
	tick := 0
	// The clock returns 0, then 1234567, for each pair of reads.
	now := func() time.Time {
		start := tick%2 == 0
		tick++
		if start {
			return time.Unix(0, 0)
		}
		return time.Unix(0, 1_234_567)
	}
	report, err := evaluate(t.Context(), testOptions(Development, nil), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.LatencySource != "local_mock_pipeline" || tick != 10 {
		t.Fatal("mock timing was not measured per document")
	}
	for _, doc := range report.Documents {
		if doc.Telemetry["latencyMs"] != json.Number("1.234567") {
			t.Fatal("wrong nanosecond to millisecond conversion")
		}
	}
	checkDecimal(t, report.Operations.LatencyMs.P95, "1.234567")
}

func TestStrictJSONRoundTripsUnicodeAndDecimalPrecision(t *testing.T) {
	value := object(t, []byte(`{"Ölçü":[0.1000000000000000000000000000000500,"\ud83d\ude00","\n"]}`))
	encoded, err := strictjson.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	again := object(t, encoded)
	if !reflect.DeepEqual(value, again) || again["Ölçü"].([]any)[0] != json.Number("0.1000000000000000000000000000000500") {
		t.Fatal("precision or Unicode changed")
	}
}

func TestStrictJSONRejectsNestedDuplicatesNonfiniteNumbersAndSurrogates(t *testing.T) {
	for _, raw := range []string{`{"a":[{"x":1,"x":2}]}`, `{"x":NaN}`, `{"\ud800":1}`, `["\udc00"]`} {
		if _, err := decodeJSON([]byte(raw)); err == nil {
			t.Fatal("invalid input was accepted")
		}
	}
	for _, value := range []any{map[int]string{1: "value"}, json.Number("Infinity"), math.NaN(), "\xed\xa0\x80"} {
		if _, err := strictjson.Encode(value); err == nil {
			t.Fatal("unsafe value was encoded")
		}
	}
}

type staticProvider struct{ response extraction.ProviderResponse }

func (p staticProvider) Extract(context.Context, extraction.Document, extraction.Kind) (extraction.ProviderResponse, error) {
	return p.response, nil
}

func TestPipelineAndSavedResponseValidationHaveMatchingOutcomes(t *testing.T) {
	gold := testGold(t, "Q-DEV-01", Development)
	blankVendor, wrongCurrency := clone(t, gold), clone(t, gold)
	blankVendor["vendorName"], wrongCurrency["currency"] = " ", "GBP"
	validators := testValidators(t)
	for _, test := range []struct {
		response extraction.ProviderResponse
		status   extraction.Status
		code     string
	}{
		{extraction.ProviderResponse{PayloadJSON: string(encode(t, gold))}, extraction.Complete, ""},
		{extraction.ProviderResponse{PayloadJSON: string(encode(t, blankVendor))}, extraction.NeedsReview, ""},
		{extraction.ProviderResponse{PayloadJSON: string(encode(t, wrongCurrency))}, extraction.Failed, "schema_violation"},
		{extraction.ProviderResponse{PayloadJSON: string(encode(t, gold)),
			UnavailableReasons: map[string]string{"/vendorName": "Unexpected."}}, extraction.Failed, "invalid_provider_response"},
		{extraction.ProviderResponse{PayloadJSON: `{"vendorName":"\ud800"}`}, extraction.Failed, "invalid_json"},
	} {
		t.Run(string(test.status)+"/"+test.code, func(t *testing.T) {
			pipeline, err := extraction.NewPipeline(staticProvider{test.response}, validators, extraction.DefaultMaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			viaPipeline := pipeline.Run(t.Context(), corpusDir+"/development/documents/Q-DEV-01.pdf", extraction.Quotation)
			viaRecord, _ := recordedResult(validators, extraction.Quotation, prediction{
				payload: &test.response.PayloadJSON, reasons: test.response.UnavailableReasons, validReasons: true,
			}, true)
			code := ""
			if len(viaRecord.Errors) != 0 {
				code = viaRecord.Errors[0].Code
			}
			if !reflect.DeepEqual(viaRecord, viaPipeline) || viaRecord.Status != test.status || code != test.code {
				t.Fatal("recorded output did not follow pipeline behavior")
			}
		})
	}
}
