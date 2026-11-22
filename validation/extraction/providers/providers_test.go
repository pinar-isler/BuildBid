package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/strictjson"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaDir = "../../../Schemas"
	devDir    = "../../corpus/v1/development"
	quotePath = devDir + "/documents/Q-DEV-01.pdf"
	deedPath  = devDir + "/documents/D-DEV-02.png"
)

type capturedRequest struct {
	url, method string
	headers     http.Header
	body        []byte
	timeout     time.Duration
	hasDeadline bool
}

type fakeTransport struct {
	calls   []capturedRequest
	status  int
	headers http.Header
	body    string
	err     error
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	defer request.Body.Close()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	deadline, hasDeadline := request.Context().Deadline()
	f.calls = append(f.calls, capturedRequest{
		url: request.URL.String(), method: request.Method, headers: request.Header.Clone(),
		body: body, timeout: time.Until(deadline), hasDeadline: hasDeadline,
	})
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: f.headers, Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

type providerTestCase struct {
	name     string
	new      func(http.RoundTripper, fs.FS, Options) (extraction.Provider, error)
	envelope func(...any) map[string]any
}

var adapters = []providerTestCase{
	{"Gemini", func(transport http.RoundTripper, schemas fs.FS, options Options) (extraction.Provider, error) {
		return NewGeminiProvider(transport, schemas, options)
	}, geminiResponse},
	{"OpenAI", func(transport http.RoundTripper, schemas fs.FS, options Options) (extraction.Provider, error) {
		return NewOpenAIProvider(transport, schemas, options)
	}, openAIResponse},
}

func geminiResponse(fragments ...any) map[string]any {
	parts := []any{map[string]any{"thought": true, "text": "Ignored synthetic reasoning."}}
	for _, fragment := range fragments {
		parts = append(parts, map[string]any{"text": fragment})
	}
	return map[string]any{"candidates": []any{map[string]any{
		"finishReason": "STOP", "content": map[string]any{"parts": parts},
	}}}
}

func openAIResponse(fragments ...any) map[string]any {
	parts := []any{}
	for _, fragment := range fragments {
		parts = append(parts, map[string]any{"type": "output_text", "text": fragment})
	}
	return map[string]any{"status": "completed", "output": []any{
		map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "Ignored synthetic reasoning."}}},
		map[string]any{"type": "message", "status": "completed", "role": "assistant", "content": parts},
	}}
}

func fakeBody(t *testing.T, body any) string {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal("could not encode the synthetic provider response")
	}
	return string(data)
}

func fakeResponse(t *testing.T, body any) *fakeTransport {
	t.Helper()
	return &fakeTransport{body: fakeBody(t, body)}
}

func provider(t *testing.T, test providerTestCase, transport http.RoundTripper, options Options) extraction.Provider {
	t.Helper()
	adapter, err := test.new(transport, os.DirFS(schemaDir), options)
	if err != nil {
		t.Fatal("could not initialize the provider")
	}
	return adapter
}

func validators(t *testing.T) *extraction.Validators {
	t.Helper()
	validator, err := extraction.LoadValidators(os.DirFS(schemaDir))
	if err != nil {
		t.Fatal("could not load the canonical schemas")
	}
	return validator
}

func runPipeline(t *testing.T, validator *extraction.Validators, adapter extraction.Provider, path string, kind extraction.Kind) extraction.Result {
	t.Helper()
	pipeline, err := extraction.NewPipeline(adapter, validator, extraction.DefaultMaxBytes)
	if err != nil {
		t.Fatal("could not initialize the pipeline")
	}
	return pipeline.Run(t.Context(), path, kind)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("could not load the test fixture")
	}
	return data
}

func decodeObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	value, err := strictjson.Decode(data)
	object, ok := value.(map[string]any)
	if err != nil || !ok {
		t.Fatal("expected a JSON object")
	}
	return object
}

func oneRequest(t *testing.T, fake *fakeTransport, timeout time.Duration) capturedRequest {
	t.Helper()
	if len(fake.calls) != 1 {
		t.Fatalf("expected one fake HTTP call, got %d", len(fake.calls))
	}
	call := fake.calls[0]
	if call.method != http.MethodPost || call.headers.Get("Content-Type") != "application/json" ||
		!call.hasDeadline || call.timeout > timeout || call.timeout < timeout-time.Second {
		t.Fatal("unexpected HTTP method, media type, or deadline")
	}
	return call
}

func schemaInRequest(t *testing.T, request []byte, service string) json.RawMessage {
	t.Helper()
	root, err := readMembers(request)
	if err != nil {
		t.Fatal("could not read captured request")
	}
	if service == "Gemini" {
		settings, err := readMembers(root.value("generationConfig"))
		if err != nil {
			t.Fatal("could not read Gemini configuration")
		}
		return settings.value("responseJsonSchema")
	}
	text, err := readMembers(root.value("text"))
	if err != nil {
		t.Fatal("could not read OpenAI text configuration")
	}
	format, err := readMembers(text.value("format"))
	if err != nil {
		t.Fatal("could not read OpenAI schema format")
	}
	return format.value("schema")
}

func checkPropertyOrder(t *testing.T, raw json.RawMessage, row string, rootNames, rowNames []string) {
	t.Helper()
	members := func(raw json.RawMessage) schemaMembers {
		result, err := readMembers(raw)
		if err != nil {
			t.Fatal("expected an ordered schema object in the request")
		}
		return result
	}
	names := func(fields schemaMembers) []string {
		result := make([]string, 0, len(fields))
		for _, field := range fields {
			result = append(result, field.name)
		}
		return result
	}
	properties := members(members(raw).value("properties"))
	item := members(members(properties.value(row)).value("items"))
	if !slices.Equal(names(properties), rootNames) || !slices.Equal(names(members(item.value("properties"))), rowNames) {
		t.Fatal("request schema changed canonical property order")
	}
}

func compileSchema(t *testing.T, raw json.RawMessage) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	const location = "urn:buildbid:provider-request-test"
	if err := compiler.AddResource(location, decodeObject(t, raw)); err != nil {
		t.Fatal("could not load projected schema")
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal("request schema does not compile")
	}
	return schema
}

func requireQuoteRequestSchema(t *testing.T, raw json.RawMessage) {
	t.Helper()
	requested := decodeObject(t, raw)
	if _, exists := requested["$schema"]; exists {
		t.Fatal("request schema still contains $schema")
	}
	properties := requested["properties"].(map[string]any)
	rows := properties["extractedItems"].(map[string]any)["items"].(map[string]any)
	rowProperties := rows["properties"].(map[string]any)
	rootNames := []string{"vendorName", "quotationDate", "currency", "extractedItems"}
	rowNames := []string{"rawDescription", "quantity", "unitOfMeasure", "quotedUnitPrice", "isTaxIncluded"}
	checkPropertyOrder(t, raw, "extractedItems", rootNames, rowNames)
	if requested["type"] != "object" || requested["additionalProperties"] != false || rows["additionalProperties"] != false ||
		len(properties) != len(rootNames) || len(rowProperties) != len(rowNames) ||
		!reflect.DeepEqual(requested["required"], []any{"vendorName", "quotationDate", "currency", "extractedItems"}) ||
		!reflect.DeepEqual(rows["required"], []any{"rawDescription", "quantity", "unitOfMeasure", "quotedUnitPrice", "isTaxIncluded"}) ||
		properties["quotationDate"].(map[string]any)["format"] != "date" ||
		rowProperties["quotedUnitPrice"].(map[string]any)["minimum"] != json.Number("0.0") {
		t.Fatal("request schema changed canonical constraints")
	}
	schema := compileSchema(t, raw)
	const sample = `{"vendorName":"Vendor","quotationDate":null,"currency":null,"extractedItems":[{"rawDescription":"Concrete","quantity":0,"unitOfMeasure":"m3","quotedUnitPrice":null,"isTaxIncluded":false}]}`
	for _, test := range []struct {
		name, raw string
		valid     bool
	}{
		{"missing values", sample, true},
		{"known currency", strings.Replace(sample, `"currency":null`, `"currency":"TRY"`, 1), true},
		{"unavailable rows", `{"vendorName":"Vendor","quotationDate":null,"currency":null,"extractedItems":null}`, true},
		{"invalid currency", strings.Replace(sample, `"currency":null`, `"currency":"GBP"`, 1), false},
		{"invalid date", strings.Replace(sample, `"quotationDate":null`, `"quotationDate":"2026-02-30"`, 1), false},
		{"null row", `{"vendorName":"Vendor","quotationDate":null,"currency":null,"extractedItems":[null]}`, false},
		{"negative quantity", strings.Replace(sample, `"quantity":0`, `"quantity":-1`, 1), false},
		{"omitted key", strings.Replace(sample, `"currency":null,`, "", 1), false},
		{"extra field", strings.Replace(sample, `"currency":null,`, `"currency":null,"extra":false,`, 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if (schema.Validate(decodeObject(t, []byte(test.raw))) == nil) != test.valid {
				t.Fatal("projected schema accepted or rejected the wrong shape")
			}
		})
	}
}

func TestGeminiInlineMediaAndStructuredRequest(t *testing.T) {
	for _, mime := range []string{"application/pdf", "image/png", "image/jpeg"} {
		t.Run(mime, func(t *testing.T) {
			fake := fakeResponse(t, geminiResponse("{", "}"))
			adapter := provider(t, adapters[0], fake, Options{Model: "gemini-test", Timeout: 12 * time.Second})
			document := extraction.Document{Content: []byte("fixture bytes"), MediaType: mime}
			response, err := adapter.Extract(t.Context(), document, extraction.Quotation)
			if err != nil || response.PayloadJSON != "{}" {
				t.Fatal("Gemini failed to assemble the text fragments")
			}
			call := oneRequest(t, fake, 12*time.Second)
			if call.url != "https://generativelanguage.googleapis.com/v1beta/models/gemini-test:generateContent" {
				t.Fatal("unexpected Gemini endpoint")
			}
			body := decodeObject(t, call.body)
			messages := body["contents"].([]any)
			message := messages[0].(map[string]any)
			parts := message["parts"].([]any)
			requestPrompt := parts[0].(map[string]any)["text"].(string)
			attachment := parts[1].(map[string]any)["inlineData"].(map[string]any)
			if len(messages) != 1 || len(parts) != 2 || message["role"] != "user" ||
				!strings.Contains(requestPrompt, "vendor quotation") || !strings.Contains(requestPrompt, "Return null") ||
				attachment["mimeType"] != mime || attachment["data"] != base64.StdEncoding.EncodeToString(document.Content) ||
				body["generationConfig"].(map[string]any)["responseMimeType"] != "application/json" {
				t.Fatal("unexpected Gemini request structure")
			}
			requireQuoteRequestSchema(t, schemaInRequest(t, call.body, "Gemini"))
		})
	}
}

func TestOpenAIPDFImageAndStrictStructuredRequest(t *testing.T) {
	for _, mime := range []string{"application/pdf", "image/png", "image/jpeg"} {
		t.Run(mime, func(t *testing.T) {
			fake := fakeResponse(t, openAIResponse("{", "}"))
			adapter := provider(t, adapters[1], fake, Options{Model: "gpt-test", Timeout: 8 * time.Second})
			document := extraction.Document{Content: []byte("fixture bytes"), MediaType: mime}
			response, err := adapter.Extract(t.Context(), document, extraction.Quotation)
			if err != nil || response.PayloadJSON != "{}" {
				t.Fatal("OpenAI failed to assemble the text fragments")
			}
			call := oneRequest(t, fake, 8*time.Second)
			if call.url != "https://api.openai.com/v1/responses" {
				t.Fatal("unexpected OpenAI endpoint")
			}
			body := decodeObject(t, call.body)
			messages := body["input"].([]any)
			message := messages[0].(map[string]any)
			parts := message["content"].([]any)
			requestPrompt := parts[0].(map[string]any)
			attachment := parts[1].(map[string]any)
			format := body["text"].(map[string]any)["format"].(map[string]any)
			if body["model"] != "gpt-test" || body["store"] != false || len(messages) != 1 || len(parts) != 2 ||
				message["role"] != "user" || requestPrompt["type"] != "input_text" ||
				!strings.Contains(requestPrompt["text"].(string), "Return null") ||
				format["type"] != "json_schema" || format["strict"] != true || format["name"] != "VendorQuotationExtraction" {
				t.Fatal("unexpected OpenAI request structure")
			}
			dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(document.Content)
			if mime == "application/pdf" {
				if attachment["type"] != "input_file" || attachment["filename"] != "document.pdf" || attachment["file_data"] != dataURL {
					t.Fatal("unexpected OpenAI PDF attachment")
				}
			} else if attachment["type"] != "input_image" || attachment["image_url"] != dataURL {
				t.Fatal("unexpected OpenAI image attachment")
			}
			requireQuoteRequestSchema(t, schemaInRequest(t, call.body, "OpenAI"))
		})
	}
}

func TestBothAdaptersRunWithoutNetworkOrCredentials(t *testing.T) {
	source := readFile(t, devDir+"/ground-truth/Q-DEV-01.json")
	gold := decodeObject(t, source)
	validator := validators(t)
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			fake := fakeResponse(t, test.envelope(string(source[:50]), string(source[50:])))
			got := runPipeline(t, validator, provider(t, test, fake, Options{}), quotePath, extraction.Quotation)
			if got.Status != extraction.Complete || !reflect.DeepEqual(got.Payload, gold) {
				t.Fatal("provider did not return the complete gold-shaped fixture")
			}
			call := oneRequest(t, fake, 30*time.Second)
			if call.headers.Get("Authorization") != "" || call.headers.Get("x-goog-api-key") != "" {
				t.Fatal("an offline request unexpectedly contains credentials")
			}
			switch test.name {
			case "Gemini":
				if !strings.HasSuffix(call.url, "/gemini-3.5-flash-lite:generateContent") {
					t.Fatal("default Gemini model changed")
				}
			case "OpenAI":
				if decodeObject(t, call.body)["model"] != "gpt-6-luna" {
					t.Fatal("default OpenAI model changed")
				}
			}
		})
	}
}

func TestValidatedJPEGFlowsThroughEachAdapter(t *testing.T) {
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewGray(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal("could not generate a JPEG fixture")
	}
	path := filepath.Join(t.TempDir(), "quotation.JPG")
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal("could not save a JPEG fixture")
	}
	const payload = `{"vendorName":"Synthetic vendor","quotationDate":"2026-05-19","currency":"TRY","extractedItems":[{"rawDescription":"Test row","quantity":0,"unitOfMeasure":"kg","quotedUnitPrice":0,"isTaxIncluded":false}]}`
	want := decodeObject(t, []byte(payload))
	validator := validators(t)
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			fake := fakeResponse(t, test.envelope(payload))
			got := runPipeline(t, validator, provider(t, test, fake, Options{}), path, extraction.Quotation)
			if got.Status != extraction.Complete || !reflect.DeepEqual(got.Payload, want) {
				t.Fatal("validated JPEG did not produce the expected complete result")
			}
			call := oneRequest(t, fake, DefaultTimeout)
			if !bytes.Contains(call.body, []byte("image/jpeg")) {
				t.Fatal("validated JPEG was sent with the wrong media type")
			}
		})
	}
}

func requireReview(t *testing.T, result extraction.Result, pointer string) {
	t.Helper()
	if result.Status != extraction.NeedsReview || len(result.Errors) != 0 ||
		!slices.Equal(result.UnavailableFields, []extraction.UnavailableField{{
			Pointer: pointer, Reason: "The provider did not supply a value.",
		}}) {
		t.Fatal("provider response did not follow the review rules")
	}
}

func TestUnavailableDeedValuesKeepTheShareholderForReview(t *testing.T) {
	payload := decodeObject(t, readFile(t, devDir+"/ground-truth/D-DEV-02.json"))
	shareholders := payload["shareholders"].([]any)
	if len(shareholders) != 1 {
		t.Fatal("expected one shareholder in this fixture")
	}
	shareholders[0].(map[string]any)["nationalId"] = nil
	validator := validators(t)
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			fake := fakeResponse(t, test.envelope(fakeBody(t, payload)))
			got := runPipeline(t, validator, provider(t, test, fake, Options{}), deedPath, extraction.TitleDeed)
			requireReview(t, got, "/shareholders/0/nationalId")
			if !reflect.DeepEqual(got.Payload, payload) {
				t.Fatal("deed payload lost a row, its null, or a leading zero")
			}
			raw := schemaInRequest(t, oneRequest(t, fake, DefaultTimeout).body, test.name)
			requested := decodeObject(t, raw)
			if requested["title"] != "TitleDeedExtraction" || compileSchema(t, raw).Validate(payload) != nil {
				t.Fatal("deed request schema did not accept the incomplete fixture")
			}
			checkPropertyOrder(t, raw, "shareholders",
				[]string{"city", "district", "blockAda", "parcelParsel", "totalAreaM2", "shareholders"},
				[]string{"nationalId", "fullName", "shareFraction"})
		})
	}
}

func TestUnavailableQuotePriceKeepsTheLineForReview(t *testing.T) {
	payload := decodeObject(t, readFile(t, devDir+"/ground-truth/Q-DEV-01.json"))
	row := payload["extractedItems"].([]any)[0].(map[string]any)
	row["quotedUnitPrice"], row["quantity"], row["isTaxIncluded"] = nil, json.Number("0.00"), false
	validator := validators(t)
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			fake := fakeResponse(t, test.envelope(fakeBody(t, payload)))
			got := runPipeline(t, validator, provider(t, test, fake, Options{}), quotePath, extraction.Quotation)
			requireReview(t, got, "/extractedItems/0/quotedUnitPrice")
			if !reflect.DeepEqual(got.Payload, payload) {
				t.Fatal("quotation payload lost a row, null, precise zero, or false")
			}
		})
	}
}

func requireFailure(t *testing.T, result extraction.Result, code, message string) {
	t.Helper()
	if result.Status != extraction.Failed || result.Payload != nil || len(result.Errors) == 0 ||
		result.Errors[0].Code != code || (message != "" && result.Errors[0].Message != message) ||
		strings.Contains(result.Errors[0].Message, "private") {
		t.Fatal("failed result had the wrong status, code, or sanitized message")
	}
}

func TestModelPayloadStillUsesStrictJSONAndCanonicalValidation(t *testing.T) {
	payload := decodeObject(t, readFile(t, devDir+"/ground-truth/Q-DEV-01.json"))
	payload["quotationDate"] = "2026-02-30"
	validator := validators(t)
	for _, adapter := range adapters {
		for _, test := range []struct{ name, body, code string }{
			{"unfinished", "{", "invalid_json"},
			{"bad date", fakeBody(t, payload), "schema_violation"},
			{"duplicate", `{"x":1,"x":2}`, "invalid_json"},
			{"trailing", "{} []", "invalid_json"},
		} {
			t.Run(adapter.name+"/"+test.name, func(t *testing.T) {
				fake := fakeResponse(t, adapter.envelope(test.body))
				result := runPipeline(t, validator, provider(t, adapter, fake, Options{}), quotePath, extraction.Quotation)
				requireFailure(t, result, test.code, "")
			})
		}
	}
}

func TestTransportAndHTTPErrorsAreSafeFailures(t *testing.T) {
	validator := validators(t)
	for _, adapter := range adapters {
		for _, test := range []struct {
			name string
			fake fakeTransport
			want string
		}{
			{"transport", fakeTransport{err: errors.New("private server details")}, "The " + adapter.name + " request could not be sent."},
			{"rate limited", fakeTransport{status: 429, body: `{"error":"private server details"}`}, adapter.name + " returned HTTP 429."},
			{"unavailable", fakeTransport{status: 503, body: `{"error":"private server details"}`}, adapter.name + " returned HTTP 503."},
			{"malformed JSON", fakeTransport{body: "private invalid JSON"}, adapter.name + " returned an invalid JSON response."},
			{"wrong envelope", fakeTransport{body: `[{"status":"unexpected shape"}]`}, adapter.name + " returned an invalid JSON response."},
			{"duplicate envelope key", fakeTransport{body: `{"error":"private","error":null}`}, adapter.name + " returned an invalid JSON response."},
			{"error object", fakeTransport{body: `{"error":{"message":"private server details"}}`}, adapter.name + " reported a request failure."},
		} {
			t.Run(adapter.name+"/"+test.name, func(t *testing.T) {
				result := runPipeline(t, validator, provider(t, adapter, &test.fake, Options{}), quotePath, extraction.Quotation)
				requireFailure(t, result, "provider_error", test.want)
			})
		}
	}
}

func TestBlockedTruncatedRefusedAndMalformedResponsesFail(t *testing.T) {
	blocked, stopped, refusal, incomplete, truncated := geminiResponse("{}"), geminiResponse("{}"),
		openAIResponse("{}"), openAIResponse("{}"), openAIResponse("{}")
	blocked["promptFeedback"] = map[string]any{"blockReason": "SAFETY"}
	stopped["candidates"].([]any)[0].(map[string]any)["finishReason"] = "MAX_TOKENS"
	message := refusal["output"].([]any)[1].(map[string]any)
	message["content"] = append(message["content"].([]any), map[string]any{"type": "refusal", "refusal": "private refusal details"})
	incomplete["output"].([]any)[1].(map[string]any)["status"] = "incomplete"
	truncated["status"] = "incomplete"
	validator := validators(t)
	for _, test := range []struct {
		name, want string
		adapter    providerTestCase
		body       any
	}{
		{"blocked", "Gemini blocked the extraction request.", adapters[0], blocked},
		{"max tokens", "Gemini did not complete the extraction response.", adapters[0], stopped},
		{"malformed candidate", "Gemini returned invalid candidates.", adapters[0], map[string]any{"candidates": []any{nil}}},
		{"no candidate", "Gemini did not return exactly one extraction candidate.", adapters[0], map[string]any{"candidates": []any{}}},
		{"no fragments", "Gemini returned no extraction text.", adapters[0], geminiResponse()},
		{"blank fragments", "Gemini returned no extraction text.", adapters[0], geminiResponse(" \t\x1c")},
		{"non-string fragment", "Gemini returned an invalid extraction text part.", adapters[0], geminiResponse("{}", 123)},
		{"truncated", "OpenAI did not complete the extraction response.", adapters[1], truncated},
		{"incomplete message", "OpenAI returned an incomplete extraction message.", adapters[1], incomplete},
		{"refusal after text", "OpenAI refused the extraction request.", adapters[1], refusal},
		{"malformed output", "OpenAI returned invalid output items.", adapters[1], map[string]any{"status": "completed", "output": nil}},
		{"no fragments", "OpenAI returned no extraction text.", adapters[1], openAIResponse()},
		{"non-string fragment", "OpenAI returned an invalid extraction text part.", adapters[1], openAIResponse("{}", 123)},
	} {
		t.Run(test.adapter.name+"/"+test.name, func(t *testing.T) {
			fake := fakeResponse(t, test.body)
			got := runPipeline(t, validator, provider(t, test.adapter, fake, Options{}), quotePath, extraction.Quotation)
			requireFailure(t, got, "provider_error", test.want)
			if len(got.UnavailableFields) != 0 {
				t.Fatal("provider failure reached review")
			}
		})
	}
}

func TestInvalidKindOrMediaIsRejectedBeforeHTTP(t *testing.T) {
	badPDF := filepath.Join(t.TempDir(), "broken.pdf")
	if err := os.WriteFile(badPDF, []byte("invalid fixture"), 0o600); err != nil {
		t.Fatal("could not create invalid test fixture")
	}
	validator := validators(t)
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			fake := fakeResponse(t, map[string]any{})
			adapter := provider(t, test, fake, Options{})
			for _, input := range []struct {
				mime string
				kind extraction.Kind
			}{
				{"application/pdf", "other"}, {"text/plain", extraction.Quotation},
			} {
				_, err := adapter.Extract(t.Context(), extraction.Document{Content: []byte("unvalidated"), MediaType: input.mime}, input.kind)
				var safe *extraction.ProviderError
				if !errors.As(err, &safe) {
					t.Fatal("expected a provider input error")
				}
			}
			requireFailure(t, runPipeline(t, validator, adapter, badPDF, extraction.Quotation), "invalid_media", "")
			requireFailure(t, runPipeline(t, validator, adapter, quotePath, "other"), "unsupported_kind", "")
			if len(fake.calls) != 0 {
				t.Fatal("invalid input reached the transport")
			}
		})
	}
}

func TestOptionalEnvironmentLookupUsesHeadersAndGeminiEscapesModel(t *testing.T) {
	for _, test := range []struct {
		adapter   providerTestCase
		env       map[string]string
		wantNames []string
		header    string
		value     string
	}{
		{adapters[0], map[string]string{"GOOGLE_API_KEY": "synthetic-one", "GEMINI_API_KEY": "synthetic-two"},
			[]string{"GOOGLE_API_KEY"}, "x-goog-api-key", "synthetic-one"},
		{adapters[0], map[string]string{"GEMINI_API_KEY": "synthetic-two"},
			[]string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}, "x-goog-api-key", "synthetic-two"},
		{adapters[1], map[string]string{"OPENAI_API_KEY": "synthetic-three"},
			[]string{"OPENAI_API_KEY"}, "Authorization", "Bearer synthetic-three"},
	} {
		t.Run(test.adapter.name+"/"+test.wantNames[len(test.wantNames)-1], func(t *testing.T) {
			fake := fakeResponse(t, test.adapter.envelope("{}"))
			var names []string
			adapter := provider(t, test.adapter, fake, Options{Model: "qa/model:+ @", LookupEnv: func(name string) (string, bool) {
				names = append(names, name)
				value, ok := test.env[name]
				return value, ok
			}})
			document := extraction.Document{Content: []byte("offline fixture"), MediaType: "image/jpeg"}
			if _, err := adapter.Extract(t.Context(), document, extraction.Quotation); err != nil {
				t.Fatal("authenticated synthetic request failed")
			}
			call := oneRequest(t, fake, DefaultTimeout)
			if !slices.Equal(names, test.wantNames) || call.headers.Get(test.header) != test.value ||
				strings.Contains(call.url, "synthetic-") || bytes.Contains(call.body, []byte("synthetic-")) {
				t.Fatal("unexpected credential lookup or placement")
			}
			if test.adapter.name == "Gemini" && !strings.HasSuffix(call.url, "/qa%2Fmodel%3A%2B%20%40:generateContent") {
				t.Fatal("Gemini model was not escaped as one path segment")
			}
		})
	}
}

func TestContextDeadlineAndRedirectHandling(t *testing.T) {
	for _, test := range adapters {
		t.Run(test.name, func(t *testing.T) {
			document := extraction.Document{Content: []byte("offline fixture"), MediaType: "image/png"}
			redirect := &fakeTransport{status: 307, headers: http.Header{"Location": {"https://example.invalid/"}}}
			adapter := provider(t, test, redirect, Options{Timeout: 9 * time.Second})
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			_, err := adapter.Extract(ctx, document, extraction.Quotation)
			if err == nil || err.Error() != test.name+" returned HTTP 307." {
				t.Fatal("redirect did not fail safely")
			}
			oneRequest(t, redirect, 4*time.Second)
			cancel()
			_, err = adapter.Extract(ctx, document, extraction.Quotation)
			if err == nil || len(redirect.calls) != 1 {
				t.Fatal("cancelled context reached transport")
			}
		})
	}
}
