package extraction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

const corpusDir = "../corpus/v1"
const schemaDir = "../../Schemas"

var (
	quotePDF = filepath.Join(corpusDir, "development", "documents", "Q-DEV-01.pdf")
	quotePNG = filepath.Join(corpusDir, "development", "documents", "Q-DEV-02.png")
)

const sampleQuote = `{"vendorName":"Sample vendor","quotationDate":"2026-05-19","currency":"TRY",
	"extractedItems":[{"rawDescription":"A sample row","quantity":2,"unitOfMeasure":"m3",
	"quotedUnitPrice":0,"isTaxIncluded":false}]}`

type providerFunc func(context.Context, Document, Kind) (ProviderResponse, error)

func (f providerFunc) Extract(ctx context.Context, doc Document, kind Kind) (ProviderResponse, error) {
	return f(ctx, doc, kind)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testValidators(t *testing.T) *Validators {
	t.Helper()
	validators, err := LoadValidators(os.DirFS(schemaDir))
	if err != nil {
		t.Fatal(err)
	}
	return validators
}

func testPipeline(t *testing.T, provider Provider, validators *Validators, limit int64) *Pipeline {
	t.Helper()
	pipeline, err := NewPipeline(provider, validators, limit)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline
}

func objectJSON(t *testing.T, data string) map[string]any {
	t.Helper()
	value, err := strictjson.Decode([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", value)
	}
	return object
}

func encodeObject(t *testing.T, object map[string]any) string {
	t.Helper()
	data, err := strictjson.Encode(object)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireFailure(t *testing.T, result Result, code, pointer string) {
	t.Helper()
	if result.Status != Failed || result.Payload != nil || len(result.Errors) == 0 ||
		result.Errors[0].Code != code || result.Errors[0].Pointer != pointer {
		t.Fatalf("want failed %q at %q, got %+v", code, pointer, result)
	}
}

func TestMockReplaysEveryCompleteAndIncompleteFixture(t *testing.T) {
	corpus := loadManifest(t)
	provider, err := NewMockProvider(os.DirFS(corpusDir))
	if err != nil {
		t.Fatal(err)
	}
	validators := testValidators(t)
	pipeline := testPipeline(t, provider, validators, DefaultMaxBytes)
	counts := map[Status]int{}
	for name, group := range corpus.Splits {
		for _, entry := range group.Documents {
			t.Run(entry.ID, func(t *testing.T) {
				input := filepath.Join(corpusDir, name, "documents", entry.File)
				result := pipeline.Run(t.Context(), input, entry.Kind)
				counts[result.Status]++
				if len(result.Errors) != 0 {
					t.Fatalf("unexpected replay errors: %+v", result.Errors)
				}
				if entry.Outcome == "complete" {
					gold := readFile(t, filepath.Join(corpusDir, name, "ground-truth", entry.ID+".json"))
					if result.Status != Complete || len(result.UnavailableFields) != 0 ||
						!reflect.DeepEqual(result.Payload, objectJSON(t, string(gold))) {
						t.Fatalf("complete result differed from gold: %+v", result)
					}
				} else {
					if result.Status != NeedsReview || !slices.Equal(result.UnavailableFields, entry.UnavailableFields) {
						t.Fatalf("review result differs from annotations: %+v", result)
					}
					for _, field := range result.UnavailableFields {
						tokens := strings.Split(field.Pointer, "/")
						rows := result.Payload[tokens[1]].([]any)
						if len(rows) != entry.SourceRowCount || tokens[2] != "0" {
							t.Fatalf("replay row count or pointer differs: %+v", result)
						}
						value, exists := rows[0].(map[string]any)[tokens[3]]
						if !exists || value != nil {
							t.Fatalf("replay must keep unavailable field null: %+v", result)
						}
					}
					invalid, err := validators.ValidateCanonical(entry.Kind, result.Payload)
					if err != nil || len(invalid) == 0 {
						t.Fatal("incomplete replay must fail raw canonical validation")
					}
				}
			})
		}
	}
	if !reflect.DeepEqual(counts, map[Status]int{Complete: 8, NeedsReview: 2}) {
		t.Fatalf("unexpected replay counts: %v", counts)
	}
}

func TestMockMatchesByBytesAndKind(t *testing.T) {
	provider, err := NewMockProvider(os.DirFS(corpusDir))
	if err != nil {
		t.Fatal(err)
	}
	pipeline := testPipeline(t, provider, testValidators(t), DefaultMaxBytes)
	dir := t.TempDir()
	renamed := writeFile(t, dir, "opaque.PDF", readFile(t, quotePDF))
	if !reflect.DeepEqual(pipeline.Run(t.Context(), renamed, Quotation), pipeline.Run(t.Context(), quotePDF, Quotation)) {
		t.Fatal("renaming the document changed fixture selection")
	}
	requireFailure(t, pipeline.Run(t.Context(), renamed, TitleDeed), "provider_error", "")
	jpeg := writeFile(t, dir, "unknown.jpeg", jpegBytes(t))
	requireFailure(t, pipeline.Run(t.Context(), jpeg, Quotation), "provider_error", "")
	quote, err := LoadDocument(renamed, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	quote.Content = readFile(t, jpeg)
	if _, err := provider.Extract(t.Context(), quote, Quotation); err == nil {
		t.Fatal("mock trusted a stale digest over the document bytes")
	}
}

func TestInjectedProviderReceivesValidatedBytesMIMEAndHash(t *testing.T) {
	var received Document
	var receivedKind Kind
	provider := providerFunc(func(_ context.Context, doc Document, kind Kind) (ProviderResponse, error) {
		received, receivedKind = doc, kind
		return ProviderResponse{PayloadJSON: sampleQuote}, nil
	})
	dir := t.TempDir()
	jpeg := jpegBytes(t)
	pipeline := testPipeline(t, provider, testValidators(t), DefaultMaxBytes)
	for _, input := range []struct{ path, mime string }{
		{quotePDF, "application/pdf"}, {quotePNG, "image/png"},
		{writeFile(t, dir, "view.JPG", jpeg), "image/jpeg"},
		{writeFile(t, dir, "view.jpeg", jpeg), "image/jpeg"},
	} {
		t.Run(filepath.Base(input.path), func(t *testing.T) {
			expected := readFile(t, input.path)
			result := pipeline.Run(t.Context(), input.path, Quotation)
			if result.Status != Complete || receivedKind != Quotation ||
				received.MediaType != input.mime || !bytes.Equal(received.Content, expected) ||
				received.SHA256 != fmt.Sprintf("%x", sha256.Sum256(expected)) {
				t.Fatalf("wrong provider input or result: %s, %s, %+v", receivedKind, received.MediaType, result)
			}
		})
	}
}

func TestInvalidInputsStopBeforeProvider(t *testing.T) {
	dir := t.TempDir()
	jpeg := jpegBytes(t)
	png := readFile(t, quotePNG)
	pdf := readFile(t, quotePDF)
	corruptPNG := slices.Clone(png)
	corruptPNG[29] ^= 1
	directory := filepath.Join(dir, "directory.pdf")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	called := 0
	provider := providerFunc(func(context.Context, Document, Kind) (ProviderResponse, error) {
		called++
		return ProviderResponse{}, nil
	})
	validators := testValidators(t)
	pipeline := testPipeline(t, provider, validators, DefaultMaxBytes)
	for _, input := range []struct{ path, code string }{
		{writeFile(t, dir, "unknown.txt", []byte("test")), "unsupported_format"},
		{filepath.Join(dir, "missing.pdf"), "input_unreadable"},
		{directory, "input_unreadable"},
		{writeFile(t, dir, "empty.png", nil), "invalid_media"},
		{writeFile(t, dir, "renamed.pdf", png), "invalid_media"},
		{writeFile(t, dir, ".pdf", pdf), "unsupported_format"},
		{writeFile(t, dir, "truncated.pdf", pdf[:len(pdf)-30]), "invalid_media"},
		{writeFile(t, dir, "damaged.png", corruptPNG), "invalid_media"},
		{writeFile(t, dir, "truncated.jpg", jpeg[:len(jpeg)-2]), "invalid_media"},
		{writeFile(t, dir, "markers.jpeg", []byte{0xff, 0xd8, 0xff, 0xd9}), "invalid_media"},
	} {
		t.Run(filepath.Base(input.path), func(t *testing.T) {
			requireFailure(t, pipeline.Run(t.Context(), input.path, Quotation), input.code, "")
		})
	}
	requireFailure(t, pipeline.Run(t.Context(), quotePDF, "other"), "unsupported_kind", "")
	path := writeFile(t, dir, "oversize.jpg", jpeg)
	limited := testPipeline(t, provider, validators, int64(len(jpeg)-1))
	requireFailure(t, limited.Run(t.Context(), path, Quotation), "input_too_large", "")
	if called != 0 {
		t.Fatalf("provider was invoked %d times for invalid input", called)
	}
	if _, err := LoadDocument(path, int64(len(jpeg))); err != nil {
		t.Fatalf("exact size limit should accept the file: %v", err)
	}
	if _, err := NewPipeline(provider, validators, 0); err == nil {
		t.Fatal("pipeline accepted a nonpositive limit")
	}
}

func TestReviewPreservesRowsOmissionsNullZeroAndFalse(t *testing.T) {
	validators := testValidators(t)
	payload := objectJSON(t, sampleQuote)
	first := payload["extractedItems"].([]any)[0].(map[string]any)
	second := objectJSON(t, encodeObject(t, first))
	second["quotedUnitPrice"] = nil
	delete(first, "unitOfMeasure")
	payload["extractedItems"] = []any{first, second}
	rows := ProviderResponse{PayloadJSON: encodeObject(t, payload), UnavailableReasons: map[string]string{
		"/extractedItems/1/quotedUnitPrice": "A dash appears in the price column.",
	}}
	review := validators.ValidateResponse(Quotation, rows)
	if review.Status != NeedsReview || !reflect.DeepEqual(review.Payload, payload) ||
		!slices.Equal(review.UnavailableFields, []UnavailableField{
			{"/extractedItems/0/unitOfMeasure", "The provider did not supply a value."},
			{"/extractedItems/1/quotedUnitPrice", "A dash appears in the price column."},
		}) || len(review.Errors) != 0 {
		t.Fatalf("unexpected review result: %+v", review)
	}
	rawIssues, err := validators.ValidateCanonical(Quotation, payload)
	if err != nil || len(rawIssues) != 2 || rawIssues[0].Pointer != "/extractedItems/0" ||
		rawIssues[1].Pointer != "/extractedItems/1/quotedUnitPrice" {
		t.Fatalf("raw validation did not distinguish omission and null: %+v, %v", rawIssues, err)
	}
}

func TestBlankStringsAndEmptyRowListsRequireReview(t *testing.T) {
	payload := objectJSON(t, sampleQuote)
	payload["vendorName"], payload["extractedItems"] = " \t\x1c", []any{}
	validators := testValidators(t)
	raw, err := validators.ValidateCanonical(Quotation, payload)
	if err != nil || len(raw) != 0 {
		t.Fatalf("schema allows blank text and empty lists: %v, %v", raw, err)
	}
	result := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: encodeObject(t, payload)})
	if result.Status != NeedsReview || result.Payload["vendorName"] != nil ||
		!reflect.DeepEqual(result.Payload["extractedItems"], []any{}) ||
		!slices.Equal(result.UnavailableFields, []UnavailableField{
			{"/vendorName", "The provider supplied a blank value."},
			{"/extractedItems", "The provider supplied no rows."},
		}) {
		t.Fatalf("unexpected blank-field review: %+v", result)
	}
}

func TestMissingRootPropertiesRemainAbsentAndKeepSchemaOrder(t *testing.T) {
	validators := testValidators(t)
	raw, err := validators.ValidateCanonical(Quotation, map[string]any{})
	wantError := ExtractionError{Code: "schema_violation", Message: "Canonical schema rule 'required' failed."}
	if err != nil || !slices.Equal(raw, []ExtractionError{wantError, wantError, wantError, wantError}) {
		t.Fatalf("raw validation must report each missing property: %+v, %v", raw, err)
	}
	projected := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: "{}"})
	if projected.Status != NeedsReview || projected.Payload == nil || len(projected.Payload) != 0 ||
		len(projected.Errors) != 0 || !slices.Equal(projected.UnavailableFields, []UnavailableField{
		{"/vendorName", "The provider did not supply a value."},
		{"/quotationDate", "The provider did not supply a value."},
		{"/currency", "The provider did not supply a value."},
		{"/extractedItems", "The provider did not supply a value."},
	}) {
		t.Fatalf("unexpected review of a wholly absent payload: %+v", projected)
	}
}

func TestReasonsCannotFlagPresentValuesOrUnknownPointers(t *testing.T) {
	validators := testValidators(t)
	payload := objectJSON(t, sampleQuote)
	payload["vendorName"] = nil
	for _, reasons := range []map[string]string{
		{"/currency": "Unknown."}, {"/unknown": "Unknown."}, {"/vendorName": " "}, {"/vendorName": "\xff"},
	} {
		result := validators.ValidateResponse(Quotation, ProviderResponse{
			PayloadJSON: encodeObject(t, payload), UnavailableReasons: reasons,
		})
		requireFailure(t, result, "invalid_provider_response", "")
		if len(result.UnavailableFields) != 0 {
			t.Fatal("invalid metadata must suppress unavailable fields")
		}
	}
}

func TestReviewChecksAllPresentValues(t *testing.T) {
	validators := testValidators(t)
	for _, entry := range []struct {
		key     string
		value   any
		pointer string
	}{
		{"quotationDate", "2026-02-30", "/quotationDate"},
		{"currency", "GBP", "/currency"},
		{"extractedItems", []any{nil}, "/extractedItems/0"},
		{"unexpected", nil, ""},
		{"currency", false, "/currency"},
	} {
		t.Run(entry.key, func(t *testing.T) {
			payload := objectJSON(t, sampleQuote)
			payload["vendorName"], payload[entry.key] = nil, entry.value
			result := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: encodeObject(t, payload)})
			requireFailure(t, result, "schema_violation", entry.pointer)
			if len(result.UnavailableFields) != 1 || result.UnavailableFields[0].Pointer != "/vendorName" {
				t.Fatalf("unavailable fields were lost: %+v", result)
			}
			if entry.value == false {
				want := []ExtractionError{
					{"schema_violation", "Canonical schema rule 'enum' failed.", "/currency"},
					{"schema_violation", "Canonical schema rule 'type' failed.", "/currency"},
				}
				if !slices.Equal(result.Errors, want) {
					t.Fatalf("expected both rules in reference order: %+v", result.Errors)
				}
			}
		})
	}
}

func TestMalformedResponsesFailWithSafeErrors(t *testing.T) {
	validators := testValidators(t)
	for _, raw := range []string{`{"x":{"key":1,"key":2}}`, `{"x":NaN}`, `{"x":Infinity}`,
		"\x60\x60\x60json\n{}\n\x60\x60\x60", `{"x":}`, `{} []`, `{"x":"\ud800"}`, "{\"x\":\"\xff\"}"} {
		result := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: raw})
		requireFailure(t, result, "invalid_json", "")
	}
	for _, raw := range []string{"null", "[]", "true", `"private data"`, "1e+99999999999"} {
		result := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: raw})
		requireFailure(t, result, "schema_violation", "")
	}
}

func TestProviderErrorsAndPanicsDoNotLeakDetails(t *testing.T) {
	validators := testValidators(t)
	for _, test := range []struct {
		name string
		give func() error
		want string
	}{
		{"ordinary error", func() error { return errors.New("private configuration details") }, "The extraction provider could not finish the request."},
		{"panic", func() error { panic("private configuration details") }, "The extraction provider could not finish the request."},
		{"safe error", func() error { return &ProviderError{Message: "A safe error."} }, "A safe error."},
		{"non-UTF-8 safe error", func() error { return &ProviderError{Message: "\xff"} }, "The extraction provider could not finish the request."},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := providerFunc(func(context.Context, Document, Kind) (ProviderResponse, error) {
				return ProviderResponse{}, test.give()
			})
			result := testPipeline(t, provider, validators, DefaultMaxBytes).Run(t.Context(), quotePDF, Quotation)
			requireFailure(t, result, "provider_error", "")
			if result.Errors[0].Message != test.want {
				t.Fatalf("unsafe or unexpected provider error: %+v", result.Errors)
			}
		})
	}
}

func TestValidatorNeverLoadsExternalSchemasAndRejectsUnsupportedRules(t *testing.T) {
	quotation := readFile(t, filepath.Join(schemaDir, "Vendor_Quotation_Schema"))
	deed := readFile(t, filepath.Join(schemaDir, "Title_deed_Schema"))
	for _, test := range []struct{ name, extra, want string }{
		{"external reference", `"$ref":"https://example.invalid/schema",`, "external schema loading is disabled"},
		{"projection rule", `"maxProperties":5,`, "unsupported extraction projection keyword"},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := fstest.MapFS{
				"Vendor_Quotation_Schema": {Data: []byte(strings.Replace(string(quotation), "{", "{"+test.extra, 1))},
				"Title_deed_Schema":       {Data: deed},
			}
			if _, err := LoadValidators(files); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected a local load failure containing %q, got %v", test.want, err)
			}
		})
	}
	if _, err := LoadValidators(fstest.MapFS{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing schemas should fail initialization: %v", err)
	}
}

func TestCanonicalExactNumberValidationAndCalendarDates(t *testing.T) {
	validators := testValidators(t)
	for _, number := range []string{
		"0", "-0", "-0E+999999999999999999999", "1.20e+003", "9007199254740993", "0.123456789012345678900",
		"1e+999999999999999999999", "1e-999999999999999999999", "-1e-999999999999999999999",
	} {
		t.Run(number, func(t *testing.T) {
			input := strings.Replace(sampleQuote, `"quantity":2`, `"quantity":`+number, 1)
			result := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: input})
			if number == "-1e-999999999999999999999" {
				requireFailure(t, result, "schema_violation", "/extractedItems/0/quantity")
				return
			}
			encoded, err := result.MarshalJSON()
			if err != nil || result.Status != Complete ||
				!strings.Contains(string(encoded), `"quantity":`+number+",") {
				t.Fatalf("numeric value was rejected or changed: %s, %v", encoded, err)
			}
		})
	}
	for _, date := range []string{"0000-01-01", "1900-02-29", "2026-2-03", "٢٠٢٦-05-19", "2026-05-19\n", "2000-02-29", "0001-01-01"} {
		t.Run(date, func(t *testing.T) {
			input := objectJSON(t, sampleQuote)
			input["quotationDate"] = date
			result := validators.ValidateResponse(Quotation, ProviderResponse{PayloadJSON: encodeObject(t, input)})
			if date == "2000-02-29" || date == "0001-01-01" {
				if result.Status != Complete {
					t.Fatalf("valid date rejected: %+v", result)
				}
			} else {
				requireFailure(t, result, "schema_violation", "/quotationDate")
			}
		})
	}
}
