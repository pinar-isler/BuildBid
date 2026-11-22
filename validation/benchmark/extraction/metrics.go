package extractionbench

import (
	"cmp"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/matching"
	"golang.org/x/text/unicode/norm"
)

type Rate struct {
	Count int      `json:"count"`
	Total int      `json:"total"`
	Rate  *float64 `json:"rate"`
}

func rate(count, total int) Rate {
	result := Rate{Count: count, Total: total}
	if total != 0 {
		value := float64(count) / float64(total)
		result.Rate = &value
	}
	return result
}

type PRF struct {
	Precision Rate `json:"precision"`
	Recall    Rate `json:"recall"`
	F1        Rate `json:"f1"`
}

func prf(correct, predicted, expected int) PRF {
	return PRF{rate(correct, predicted), rate(correct, expected), rate(2*correct, predicted+expected)}
}

type FieldCounts struct {
	Expected        int `json:"expected"`
	Predicted       int `json:"predicted"`
	Correct         int `json:"correct"`
	DifferentValues int `json:"differentValues"`
	Missing         int `json:"missing"`
}

// Extra counts present fields on unaligned prediction rows.
func (c FieldCounts) Extra() int { return c.Predicted - (c.Expected - c.Missing) }

type RowCounts struct {
	Expected  int `json:"expected"`
	Predicted int `json:"predicted"`
	Aligned   int `json:"aligned"`
}

type NumericGroup struct {
	Field            string   `json:"field"`
	Unit             string   `json:"unit"`
	ExpectedPairs    int      `json:"expectedPairs"`
	ScoredPairs      int      `json:"scoredPairs"`
	AbsoluteErrorSum Decimal  `json:"absoluteErrorSum"`
	MaxAbsoluteError *Decimal `json:"maxAbsoluteError"`
}

type CompleteScore struct {
	Fields               FieldCounts    `json:"fields"`
	Rows                 RowCounts      `json:"rows"`
	MissingFieldPointers []string       `json:"missingFieldPointers"`
	Numeric              []NumericGroup `json:"numeric"`
}

type IncompleteScore struct {
	Passed                      bool     `json:"passed"`
	ExpectedRows                int      `json:"expectedRows"`
	PredictedRows               int      `json:"predictedRows"`
	ExpectedUnavailablePointers []string `json:"expectedUnavailablePointers"`
	UnflaggedPointers           []string `json:"unflaggedPointers"`
	UnexpectedPointers          []string `json:"unexpectedPointers"`
	UnsetOnRetainedRows         []string `json:"unsetOnRetainedRows"`
}

type FieldSummary struct {
	FieldCounts
	PRF
}

type RowSummary struct {
	RowCounts
	Missing int `json:"missing"`
	Extra   int `json:"extra"`
	PRF
}

type NumericSummary struct {
	NumericGroup
	MeanAbsoluteError *Decimal `json:"meanAbsoluteError"`
}

type Quality struct {
	CompleteSchemaValidRate  Rate             `json:"completeSchemaValidRate"`
	FieldAccuracy            Rate             `json:"fieldAccuracy"`
	Fields                   FieldSummary     `json:"fields"`
	Rows                     RowSummary       `json:"rows"`
	NumericErrors            []NumericSummary `json:"numericErrors"`
	IncompleteReviewPassRate Rate             `json:"incompleteReviewPassRate"`
}

type scoringSchema struct {
	rowKey, identity string
	headerFields     []string
	rowFields        []string
}

func rowIdentity(kind extraction.Kind) (string, string) {
	if kind == extraction.TitleDeed {
		return "shareholders", "nationalId"
	}
	return "extractedItems", "rawDescription"
}

func whitespace(r rune) bool {
	return unicode.IsSpace(r) || (r >= '\x1c' && r <= '\x1f')
}

func normalized(field string, value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	text = strings.Join(strings.FieldsFunc(norm.NFC.String(text), whitespace), " ")
	if field == "unitOfMeasure" {
		if normalizedUnit := matching.NormalizeUnit(text); normalizedUnit != "" {
			text = normalizedUnit
		}
	}
	if text == "" {
		return nil
	}
	return text
}

func rowArray(payload map[string]any, key string) []any {
	rows, _ := payload[key].([]any)
	return rows
}

func align(expected, predicted []any, identity string) ([]map[string]any, []map[string]any) {
	available := make(map[string][]int)
	for i, raw := range predicted {
		row := raw.(map[string]any)
		if id, ok := normalized(identity, row[identity]).(string); ok {
			available[id] = append(available[id], i)
		}
	}
	aligned := make([]map[string]any, len(expected))
	used := make([]bool, len(predicted))
	for i, raw := range expected {
		id, _ := normalized(identity, raw.(map[string]any)[identity]).(string)
		if queue := available[id]; len(queue) != 0 {
			index := queue[0]
			available[id], used[index] = queue[1:], true
			aligned[i] = predicted[index].(map[string]any)
		}
	}
	extra := make([]map[string]any, 0)
	for i, raw := range predicted {
		if !used[i] {
			extra = append(extra, raw.(map[string]any))
		}
	}
	return aligned, extra
}

type numericKey struct{ field, unit string }

func orderedNumericKeys(groups map[numericKey]*NumericGroup) []numericKey {
	return slices.SortedFunc(maps.Keys(groups), func(left, right numericKey) int {
		if order := cmp.Compare(left.field, right.field); order != 0 {
			return order
		}
		return cmp.Compare(left.unit, right.unit)
	})
}

func scoreComplete(gold map[string]any, result extraction.Result, schema scoringSchema) (*CompleteScore, error) {
	prediction := result.Payload
	goldRows, predictedRows := rowArray(gold, schema.rowKey), rowArray(prediction, schema.rowKey)
	aligned, extra := align(goldRows, predictedRows, schema.identity)
	score := &CompleteScore{
		Rows:                 RowCounts{len(goldRows), len(predictedRows), len(predictedRows) - len(extra)},
		MissingFieldPointers: []string{},
		Numeric:              []NumericGroup{},
	}
	groups := make(map[numericKey]*NumericGroup)
	compare := func(fields []string, expected, actual map[string]any, prefix string) error {
		for _, field := range fields {
			score.Fields.Expected++
			goldValue, value := normalized(field, expected[field]), normalized(field, actual[field])
			// The canonical schemas guarantee scalar values of the same
			// type in gold and present, validated predictions.
			if number, numeric := goldValue.(json.Number); numeric {
				if err := scoreNumber(field, number, value, expected, actual, gold, prediction, groups, &score.Fields); err != nil {
					return err
				}
			} else if value != nil {
				if value == goldValue {
					score.Fields.Correct++
				} else {
					score.Fields.DifferentValues++
				}
			}
			if value == nil {
				score.MissingFieldPointers = append(score.MissingFieldPointers, prefix+"/"+field)
			} else {
				score.Fields.Predicted++
			}
		}
		return nil
	}
	if err := compare(schema.headerFields, gold, prediction, ""); err != nil {
		return nil, err
	}
	for i, row := range goldRows {
		if err := compare(schema.rowFields, row.(map[string]any), aligned[i], "/"+schema.rowKey+"/"+strconv.Itoa(i)); err != nil {
			return nil, err
		}
	}
	score.Fields.Missing = len(score.MissingFieldPointers)
	for _, row := range extra {
		for _, field := range schema.rowFields {
			if normalized(field, row[field]) != nil {
				score.Fields.Predicted++
			}
		}
	}
	for _, key := range orderedNumericKeys(groups) {
		score.Numeric = append(score.Numeric, *groups[key])
	}
	return score, nil
}

func scoreNumber(field string, goldNumber json.Number, value any, expected, actual, gold, prediction map[string]any, groups map[numericKey]*NumericGroup, counts *FieldCounts) error {
	var unit string
	comparable := true
	switch field {
	case "totalAreaM2":
		unit = "m2"
	case "quantity", "quotedUnitPrice":
		quotedUnit := normalized("unitOfMeasure", expected["unitOfMeasure"])
		comparable = quotedUnit == normalized("unitOfMeasure", actual["unitOfMeasure"])
		unit, _ = quotedUnit.(string)
		if field == "quotedUnitPrice" {
			unit = gold["currency"].(string)
			comparable = comparable && unit == normalized("currency", prediction["currency"])
		}
	}
	key := numericKey{field, unit}
	group := groups[key]
	if group == nil {
		group = &NumericGroup{Field: field, Unit: unit}
		groups[key] = group
	}
	group.ExpectedPairs++
	if value == nil {
		return nil
	}
	base, err := parseDecimal(goldNumber)
	if err != nil {
		return err
	}
	got, err := parseDecimal(value.(json.Number))
	if err != nil {
		return err
	}
	if got.cmp(base) == 0 {
		counts.Correct++
	} else {
		counts.DifferentValues++
	}
	if !comparable {
		return nil
	}
	delta, err := got.sub(base)
	if err != nil {
		return err
	}
	delta = delta.abs()
	group.AbsoluteErrorSum, err = group.AbsoluteErrorSum.add(delta)
	if err != nil {
		return err
	}
	group.ScoredPairs++
	if group.MaxAbsoluteError == nil || delta.cmp(*group.MaxAbsoluteError) > 0 {
		group.MaxAbsoluteError = &delta
	}
	return nil
}

func scoreIncomplete(entry manifestDocument, result extraction.Result) *IncompleteScore {
	expected, flagged := make(map[string]bool), make(map[string]bool)
	for _, field := range entry.UnavailableFields {
		expected[field.Pointer] = true
	}
	for _, field := range result.UnavailableFields {
		flagged[field.Pointer] = true
	}
	rowKey, _ := rowIdentity(entry.Kind)
	score := &IncompleteScore{
		ExpectedRows:                entry.SourceRowCount,
		PredictedRows:               len(rowArray(result.Payload, rowKey)),
		ExpectedUnavailablePointers: slices.Sorted(maps.Keys(expected)),
		UnflaggedPointers:           []string{},
		UnexpectedPointers:          []string{},
		UnsetOnRetainedRows:         []string{},
	}
	if score.ExpectedUnavailablePointers == nil {
		score.ExpectedUnavailablePointers = []string{}
	}
	for _, pointer := range score.ExpectedUnavailablePointers {
		if !flagged[pointer] {
			score.UnflaggedPointers = append(score.UnflaggedPointers, pointer)
		}
		if unsetAtPointer(result.Payload, pointer) {
			score.UnsetOnRetainedRows = append(score.UnsetOnRetainedRows, pointer)
		}
	}
	for _, pointer := range slices.Sorted(maps.Keys(flagged)) {
		if !expected[pointer] {
			score.UnexpectedPointers = append(score.UnexpectedPointers, pointer)
		}
	}
	score.Passed = result.Status == extraction.NeedsReview && score.PredictedRows == score.ExpectedRows &&
		len(score.UnflaggedPointers) == 0 && len(score.UnexpectedPointers) == 0 &&
		len(score.UnsetOnRetainedRows) == len(expected)
	return score
}

var pointerUnescape = strings.NewReplacer("~1", "/", "~0", "~")

func unsetAtPointer(payload map[string]any, pointer string) bool {
	path, ok := strings.CutPrefix(pointer, "/")
	if !ok {
		return false
	}
	parts := strings.Split(path, "/")
	var value any = payload
	for _, encoded := range parts[:len(parts)-1] {
		part := pointerUnescape.Replace(encoded)
		switch node := value.(type) {
		case map[string]any:
			value = node[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return false
			}
			value = node[index]
		default:
			return false
		}
	}
	object, ok := value.(map[string]any)
	return ok && object != nil && object[pointerUnescape.Replace(parts[len(parts)-1])] == nil
}

func summarizeQuality(documents []Document) (*Quality, error) {
	var fields FieldCounts
	var rows RowCounts
	var complete, schemaValid, incomplete, reviewPassed int
	groups := make(map[numericKey]*NumericGroup)
	for _, doc := range documents {
		switch score := doc.Quality.(type) {
		case *CompleteScore:
			complete++
			if doc.CanonicalSchemaValid != nil && *doc.CanonicalSchemaValid {
				schemaValid++
			}
			fields.Expected += score.Fields.Expected
			fields.Predicted += score.Fields.Predicted
			fields.Correct += score.Fields.Correct
			fields.DifferentValues += score.Fields.DifferentValues
			fields.Missing += score.Fields.Missing
			rows.Expected += score.Rows.Expected
			rows.Predicted += score.Rows.Predicted
			rows.Aligned += score.Rows.Aligned
			for _, item := range score.Numeric {
				if err := accumulateNumeric(groups, item); err != nil {
					return nil, err
				}
			}
		case *IncompleteScore:
			incomplete++
			if score.Passed {
				reviewPassed++
			}
		}
	}
	quality := &Quality{
		CompleteSchemaValidRate: rate(schemaValid, complete),
		FieldAccuracy:           rate(fields.Correct, fields.Expected),
		Fields:                  FieldSummary{fields, prf(fields.Correct, fields.Predicted, fields.Expected)},
		Rows: RowSummary{rows, rows.Expected - rows.Aligned, rows.Predicted - rows.Aligned,
			prf(rows.Aligned, rows.Predicted, rows.Expected)},
		NumericErrors:            []NumericSummary{},
		IncompleteReviewPassRate: rate(reviewPassed, incomplete),
	}
	for _, key := range orderedNumericKeys(groups) {
		group := groups[key]
		summary := NumericSummary{NumericGroup: *group}
		if group.ScoredPairs != 0 {
			mean, err := group.AbsoluteErrorSum.quo(decimalInt(int64(group.ScoredPairs)))
			if err != nil {
				return nil, err
			}
			summary.MeanAbsoluteError = &mean
		}
		quality.NumericErrors = append(quality.NumericErrors, summary)
	}
	return quality, nil
}

func accumulateNumeric(groups map[numericKey]*NumericGroup, item NumericGroup) error {
	key := numericKey{item.Field, item.Unit}
	group := groups[key]
	if group == nil {
		group = &NumericGroup{Field: item.Field, Unit: item.Unit}
		groups[key] = group
	}
	group.ExpectedPairs += item.ExpectedPairs
	group.ScoredPairs += item.ScoredPairs
	var err error
	group.AbsoluteErrorSum, err = group.AbsoluteErrorSum.add(item.AbsoluteErrorSum)
	if err != nil {
		return err
	}
	if item.MaxAbsoluteError != nil {
		current := Decimal{}
		if group.MaxAbsoluteError != nil && !group.MaxAbsoluteError.zero() {
			current = *group.MaxAbsoluteError
		}
		if item.MaxAbsoluteError.cmp(current) > 0 {
			current = *item.MaxAbsoluteError
		}
		group.MaxAbsoluteError = &current
	}
	return nil
}
