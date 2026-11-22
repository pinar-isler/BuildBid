package extractionbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
)

func formatRate(metric Rate) string {
	result := fmt.Sprintf("%d/%d", metric.Count, metric.Total)
	if metric.Rate != nil {
		result += fmt.Sprintf(" (%.1f%%)", *metric.Rate*100)
	}
	return result
}

func formatValue[T fmt.Stringer](value *T) string {
	if value == nil {
		return "unavailable"
	}
	return (*value).String()
}

func formatLabel(label *string) string {
	if label == nil {
		return "unavailable"
	}
	for _, r := range *label {
		if !unicode.IsPrint(r) {
			return quoteASCII(*label)
		}
	}
	return *label
}

func quoteASCII(value string) string {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return asciiJSON(strings.TrimSuffix(encoded.String(), "\n"))
}

func asciiJSON(encoded string) string {
	var output strings.Builder
	for _, r := range encoded {
		switch {
		case r < 0x7f:
			output.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&output, "\\u%04x", r)
		default:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&output, "\\u%04x\\u%04x", hi, lo)
		}
	}
	return output.String()
}

// MarshalReport writes JSON with non-ASCII code points escaped, including
// control characters that a terminal can interpret while showing the report.
func MarshalReport(report Report) ([]byte, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	return []byte(asciiJSON(string(encoded))), nil
}

// ConsoleReport formats the same report without exposing prediction payloads.
func ConsoleReport(report Report) string {
	var output strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&output, format+"\n", args...) }
	ops := report.Operations
	line("BuildBid extraction benchmark — %s — %s", report.Source, report.Split)
	if report.Provider != nil || report.Model != nil {
		line("Provider %s  Model %s", formatLabel(report.Provider), formatLabel(report.Model))
	}
	line("Statuses  complete %d  needs_review %d  failed %d", ops.Statuses.Complete, ops.Statuses.NeedsReview, ops.Statuses.Failed)
	line("Invalid JSON  %s", formatRate(ops.InvalidJSONRate))
	line("Missing predictions  %d", ops.MissingPredictions)
	line("Attempts without response text  %d", ops.NoResponses)
	line("Latency in ms (%s, %d documents)  mean %s  p50 %s  p95 %s", report.LatencySource, ops.LatencyMs.ObservedDocuments,
		formatValue(ops.LatencyMs.Mean), formatValue(ops.LatencyMs.P50), formatValue(ops.LatencyMs.P95))
	for _, field := range tokenFields {
		total := ops.Tokens[field]
		line("%s  %s  (%d documents)", field, formatValue(total.Total), total.ObservedDocuments)
	}
	line("Recorded API cost in USD  %s  (%d documents)", formatValue(ops.APICostUSD.Total), ops.APICostUSD.ObservedDocuments)
	quality := report.Quality
	if quality == nil {
		line("Mock replay exercises the pipeline. Extraction quality metrics are suppressed.")
		return strings.TrimSuffix(output.String(), "\n")
	}
	line("Quality on corpus v1 fixtures using caller-recorded predictions.")
	line("Field accuracy  %s", formatRate(quality.FieldAccuracy))
	line("Canonical schema validity on complete cases  %s", formatRate(quality.CompleteSchemaValidRate))
	line("Missing fields  %d", quality.Fields.Missing)
	line("Incomplete cases passed  %s", formatRate(quality.IncompleteReviewPassRate))
	for _, group := range []struct {
		name string
		prf  PRF
	}{{"Fields", quality.Fields.PRF}, {"Rows", quality.Rows.PRF}} {
		line("%s  precision %s  recall %s  f1 %s", group.name,
			formatRate(group.prf.Precision), formatRate(group.prf.Recall), formatRate(group.prf.F1))
	}
	for _, group := range quality.NumericErrors {
		line("%s (%s)  MAE %s  max %s  %d/%d scored pairs", group.Field, group.Unit,
			formatValue(group.MeanAbsoluteError), formatValue(group.MaxAbsoluteError), group.ScoredPairs, group.ExpectedPairs)
	}
	return strings.TrimSuffix(output.String(), "\n")
}
