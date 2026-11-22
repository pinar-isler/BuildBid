package extractionbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/strictjson"
	"golang.org/x/text/cases"
)

type manifest struct {
	Version int                      `json:"version"`
	Splits  map[string]manifestSplit `json:"splits"`
}

type manifestSplit struct {
	Documents []manifestDocument `json:"documents"`
}

type manifestDocument struct {
	ID                string                        `json:"id"`
	Kind              extraction.Kind               `json:"kind"`
	File              string                        `json:"file"`
	Outcome           string                        `json:"outcome"`
	SourceRowCount    int                           `json:"sourceRowCount"`
	UnavailableFields []extraction.UnavailableField `json:"unavailableFields"`
}

const (
	MockSource = "mock"
	LiveSource = "live"
)

var tokenFields = [...]string{"inputTokens", "outputTokens", "totalTokens"}

type predictionBundle struct {
	source          string
	provider, model *string
	records         map[string]prediction
}

type prediction struct {
	payload      *string
	reasons      map[string]string
	validReasons bool
	telemetry    map[string]json.Number
}

func decodeJSON(data []byte) (any, error) {
	value, err := strictjson.Decode(data)
	if err != nil {
		return nil, err
	}
	// Python parses fractional and exponent tokens as Decimal at input.
	// Apply its constructor bounds here while leaving integer tokens exact.
	if err := validateDecimalTokens(value); err != nil {
		return nil, err
	}
	return value, nil
}

func validateDecimalTokens(value any) error {
	switch value := value.(type) {
	case json.Number:
		if strings.ContainsAny(value.String(), ".eE") {
			_, err := parseDecimal(value)
			return err
		}
	case []any:
		for _, item := range value {
			if err := validateDecimalTokens(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range value {
			if err := validateDecimalTokens(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func isMockProvider(name string) bool {
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return -1
	}, cases.Fold().String(name))
	switch name {
	case "mock", "fixturereplay", "mockprovider", "mockresponseprovider":
		return true
	default:
		return false
	}
}

func readBundle(data []byte, knownIDs map[string]bool) (predictionBundle, error) {
	value, err := decodeJSON(data)
	if err != nil {
		if errors.Is(err, errDecimalRange) {
			return predictionBundle{}, err
		}
		return predictionBundle{}, errors.New("Invalid prediction bundle JSON.")
	}
	if value == nil {
		return predictionBundle{}, errors.New("A prediction bundle must be a JSON object.")
	}
	object, objectOK := value.(map[string]any)
	source, _ := object["source"].(string)
	if !objectOK || (source != LiveSource && source != MockSource) {
		return predictionBundle{}, errors.New("A prediction bundle must declare source as live or mock.")
	}
	records, recordsOK := object["predictions"].(map[string]any)
	if !recordsOK {
		return predictionBundle{}, errors.New("A prediction bundle needs a predictions object keyed by fixture ID.")
	}
	var unknown []string
	for _, id := range slices.Sorted(maps.Keys(records)) {
		if !knownIDs[id] {
			unknown = append(unknown, quoteASCII(id))
		}
	}
	if len(unknown) > 0 {
		return predictionBundle{}, fmt.Errorf("Unknown fixture IDs in predictions: %s.", strings.Join(unknown, ", "))
	}
	bundle := predictionBundle{source: source, records: make(map[string]prediction, len(records))}
	for _, field := range []struct {
		name   string
		target **string
	}{{"provider", &bundle.provider}, {"model", &bundle.model}} {
		if raw, exists := object[field.name]; exists {
			text, ok := raw.(string)
			if !ok || strings.TrimFunc(text, whitespace) == "" {
				return predictionBundle{}, fmt.Errorf("%s must be a nonempty string when supplied.", field.name)
			}
			*field.target = &text
		}
	}
	if source == LiveSource && bundle.provider != nil && isMockProvider(*bundle.provider) {
		return predictionBundle{}, errors.New("Mock providers require a mock prediction bundle.")
	}
	for _, id := range slices.Sorted(maps.Keys(records)) {
		record, err := readPrediction(id, object, source, records[id])
		if err != nil {
			return predictionBundle{}, err
		}
		bundle.records[id] = record
	}
	return bundle, nil
}

func readPrediction(id string, bundle map[string]any, source string, raw any) (prediction, error) {
	record, ok := raw.(map[string]any)
	recSource, hasSource := record["source"]
	if !ok || (hasSource && recSource != source) {
		return prediction{}, fmt.Errorf("Prediction %s must be an object from the declared source.", id)
	}
	for _, field := range []string{"provider", "model"} {
		if value, specified := record[field]; specified {
			expected, declared := bundle[field]
			if !declared || value != expected {
				return prediction{}, fmt.Errorf("Prediction %s has a %s that differs from the bundle declaration.", id, field)
			}
		}
	}
	var result prediction
	if raw := record["payloadJson"]; raw != nil {
		text, ok := raw.(string)
		if !ok {
			return prediction{}, fmt.Errorf("Prediction %s needs raw JSON text or null in payloadJson.", id)
		}
		result.payload = &text
	}
	if raw, exists := record["unavailableReasons"]; exists {
		result.reasons, result.validReasons = readReasons(raw)
	} else {
		result.validReasons = true
	}
	telemetry, err := readTelemetry(id, record)
	result.telemetry = telemetry
	return result, err
}

func readReasons(raw any) (map[string]string, bool) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	reasons := make(map[string]string, len(object))
	for pointer, item := range object {
		reason, ok := item.(string)
		if !ok {
			return nil, false
		}
		reasons[pointer] = reason
	}
	return reasons, true
}

func readTelemetry(id string, record map[string]any) (map[string]json.Number, error) {
	telemetry := make(map[string]json.Number)
	for _, field := range []string{"latencyMs", tokenFields[0], tokenFields[1], tokenFields[2], "costUsd"} {
		raw := record[field]
		if raw == nil {
			continue
		}
		number, ok := raw.(json.Number)
		invalid := fmt.Errorf("Prediction %s has an invalid %s.", id, field)
		if !ok {
			return nil, invalid
		}
		if slices.Contains(tokenFields[:], field) {
			if strings.ContainsAny(number.String(), ".eE") {
				return nil, invalid
			}
			if number == "-0" {
				number = "0"
			}
			if strings.HasPrefix(number.String(), "-") {
				return nil, invalid
			}
		} else {
			decimal, err := parseDecimal(number)
			if err != nil {
				return nil, err
			}
			if decimal.negative && !decimal.zero() {
				return nil, invalid
			}
			number = json.Number(decimal.String())
		}
		telemetry[field] = number
	}
	return telemetry, nil
}

func readScoringSchema(schemas fs.FS, kind extraction.Kind, filename string) (scoringSchema, string, error) {
	raw, err := fs.ReadFile(schemas, filename)
	if err != nil {
		return scoringSchema{}, "", err
	}
	rowKey, identity := rowIdentity(kind)
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Items struct {
				Required []string `json:"required"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return scoringSchema{}, "", err
	}
	fields := slices.DeleteFunc(schema.Required, func(field string) bool { return field == rowKey })
	items := schema.Properties[rowKey].Items.Required
	if len(fields) == 0 || len(items) == 0 {
		return scoringSchema{}, "", errors.New("Canonical schema lacks required benchmark fields.")
	}
	return scoringSchema{rowKey, identity, fields, items}, hash(raw), nil
}
