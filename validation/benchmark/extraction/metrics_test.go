package extractionbench

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"golang.org/x/text/unicode/norm"
)

const corpusDir = "../../corpus/v1"
const schemaDir = "../../../Schemas"

func testGold(t *testing.T, fixture, split string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(corpusDir, split, "ground-truth", fixture+".json"))
	if err != nil {
		t.Fatal("could not read gold fixture")
	}
	return object(t, data)
}

func object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	value, err := decodeJSON(raw)
	payload, ok := value.(map[string]any)
	if err != nil || !ok {
		t.Fatal("expected a valid JSON object")
	}
	return payload
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal("could not encode test input")
	}
	return data
}

func clone(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	return object(t, encode(t, payload))
}

func testValidators(t *testing.T) *extraction.Validators {
	t.Helper()
	validators, err := extraction.LoadValidators(os.DirFS(schemaDir))
	if err != nil {
		t.Fatal("could not load canonical validators")
	}
	return validators
}

func compare(t *testing.T, expected, actual map[string]any, kind extraction.Kind) *CompleteScore {
	t.Helper()
	file := "Vendor_Quotation_Schema"
	if kind == extraction.TitleDeed {
		file = "Title_deed_Schema"
	}
	schema, _, err := readScoringSchema(os.DirFS(schemaDir), kind, file)
	if err != nil {
		t.Fatal("could not load canonical scoring fields")
	}
	validators := testValidators(t)
	result := validators.ValidateResponse(kind, extraction.ProviderResponse{PayloadJSON: string(encode(t, actual))})
	if result.Payload == nil {
		t.Fatalf("scoring fixture was rejected: %v", result.Errors)
	}
	score, err := scoreComplete(expected, result, schema)
	if err != nil {
		t.Fatal(err)
	}
	return score
}

func qualityFrom(t *testing.T, scores ...*CompleteScore) *Quality {
	t.Helper()
	docs := make([]Document, len(scores))
	valid := true
	for i, score := range scores {
		docs[i] = Document{Quality: score, CanonicalSchemaValid: &valid}
	}
	quality, err := summarizeQuality(docs)
	if err != nil {
		t.Fatal(err)
	}
	return quality
}

func numeric(t *testing.T, groups []NumericGroup, field, unit string) NumericGroup {
	t.Helper()
	for _, group := range groups {
		if group.Field == field && group.Unit == unit {
			return group
		}
	}
	t.Fatalf("missing numeric group %s/%s", field, unit)
	return NumericGroup{}
}

func numericSummary(t *testing.T, groups []NumericSummary, field, unit string) NumericSummary {
	t.Helper()
	for _, group := range groups {
		if group.Field == field && group.Unit == unit {
			return group
		}
	}
	t.Fatalf("missing numeric summary %s/%s", field, unit)
	return NumericSummary{}
}

func checkRate(t *testing.T, got Rate, count, total int) {
	t.Helper()
	if !reflect.DeepEqual(got, rate(count, total)) {
		t.Fatalf("expected rate %d/%d, got %+v", count, total, got)
	}
}

func checkDecimal(t *testing.T, got *Decimal, expected string) {
	t.Helper()
	want, err := parseDecimal(json.Number(expected))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.cmp(want) != 0 {
		t.Fatalf("expected decimal %s, got %v", expected, got)
	}
}

func TestAlignedFieldsCountMissingExtraAndWrongValues(t *testing.T) {
	expected := testGold(t, "Q-DEV-01", Development)
	actual := clone(t, expected)
	actual["vendorName"] = "Synthetic different vendor"
	rows := rowArray(actual, "extractedItems")
	first, third := rows[0].(map[string]any), rows[2].(map[string]any)
	first["quantity"], first["quotedUnitPrice"], first["unitOfMeasure"] = json.Number("1251.00"), json.Number("2451.00"), "m³"
	third["rawDescription"] = "  " + norm.NFD.String(third["rawDescription"].(string)) + "  "
	third["quotedUnitPrice"] = nil
	extra := maps.Clone(rows[1].(map[string]any))
	extra["rawDescription"] = "Extra row"
	actual["extractedItems"] = []any{rows[3], first, third, extra}
	score := compare(t, expected, actual, extraction.Quotation)
	if score.Fields != (FieldCounts{23, 22, 14, 3, 6}) || score.Rows != (RowCounts{4, 4, 3}) ||
		score.Fields.Extra() != 5 ||
		!slices.Contains(score.MissingFieldPointers, "/extractedItems/2/quotedUnitPrice") ||
		!slices.Contains(score.MissingFieldPointers, "/extractedItems/1/rawDescription") {
		t.Fatalf("unexpected field or row score: %+v", score)
	}
	summary := qualityFrom(t, score)
	checkRate(t, summary.FieldAccuracy, 14, 23)
	checkRate(t, summary.Fields.Precision, 14, 22)
	checkRate(t, summary.Fields.Recall, 14, 23)
	checkRate(t, summary.Fields.F1, 28, 45)
	checkRate(t, summary.Rows.Precision, 3, 4)
	checkRate(t, summary.Rows.Recall, 3, 4)
	checkRate(t, summary.Rows.F1, 6, 8)
	if summary.Rows.Extra != 1 || summary.Rows.Missing != 1 {
		t.Fatal("summary lost missing or extra rows")
	}
	checkDecimal(t, numericSummary(t, summary.NumericErrors, "quantity", "m3").MeanAbsoluteError, "0.50")
	price := numericSummary(t, summary.NumericErrors, "quotedUnitPrice", "TRY")
	checkDecimal(t, price.MeanAbsoluteError, "0.125")
	if price.ScoredPairs != 2 {
		t.Fatal("wrong number of comparable prices")
	}
}

func TestDuplicateQuoteDescriptionsAlignInSourceOrder(t *testing.T) {
	expected := testGold(t, "Q-DEV-01", Development)
	first := rowArray(expected, "extractedItems")[0].(map[string]any)
	second := maps.Clone(first)
	second["quantity"] = json.Number("1252.50")
	expected["extractedItems"] = []any{first, second}
	actual := maps.Clone(expected)
	actual["extractedItems"] = []any{second, first}
	score := compare(t, expected, actual, extraction.Quotation)
	if score.Rows.Aligned != 2 || score.Fields.DifferentValues != 2 {
		t.Fatal("duplicate rows did not align in source order")
	}
	group := numeric(t, score.Numeric, "quantity", "m3")
	checkDecimal(t, &group.AbsoluteErrorSum, "4")
}

func TestUnalignedRowsAddValuesAndLeaveExpectedFieldsMissing(t *testing.T) {
	expected := testGold(t, "Q-DEV-01", Development)
	first := rowArray(expected, "extractedItems")[0].(map[string]any)
	expected["extractedItems"] = []any{first}
	actual := maps.Clone(expected)
	extra, anonymous := maps.Clone(first), maps.Clone(first)
	extra["rawDescription"], anonymous["rawDescription"] = first["rawDescription"].(string)+"!", nil
	actual["extractedItems"] = []any{extra, anonymous}
	score := compare(t, expected, actual, extraction.Quotation)
	if score.Rows != (RowCounts{1, 2, 0}) || score.Fields != (FieldCounts{8, 12, 3, 0, 5}) || score.Fields.Extra() != 9 {
		t.Fatalf("wrong missing/extra scoring: %+v", score)
	}
}

func TestZeroAndFalseArePresentAndSymbolOnlyUnitIsWrong(t *testing.T) {
	expected := testGold(t, "Q-DEV-01", Development)
	first := rowArray(expected, "extractedItems")[0].(map[string]any)
	first["quantity"], first["quotedUnitPrice"], first["isTaxIncluded"] = json.Number("0"), json.Number("0"), false
	expected["extractedItems"] = []any{first}
	actual := clone(t, expected)
	rowArray(actual, "extractedItems")[0].(map[string]any)["unitOfMeasure"] = "%"
	score := compare(t, expected, actual, extraction.Quotation)
	if score.Fields != (FieldCounts{8, 8, 7, 1, 0}) || len(score.MissingFieldPointers) != 0 {
		t.Fatal("zero or false treated as missing")
	}
	for _, group := range score.Numeric {
		if group.ScoredPairs != 0 {
			t.Fatal("incomparable numeric pair was scored")
		}
	}
}

func TestDeedAlignmentUsesIDAndPreservesLeadingZeros(t *testing.T) {
	expected := testGold(t, "D-HOLD-01", HeldOut)
	actual := clone(t, expected)
	slices.Reverse(rowArray(actual, "shareholders"))
	actual["parcelParsel"] = strings.TrimLeft(expected["parcelParsel"].(string), "0")
	base, err := parseDecimal(expected["totalAreaM2"].(json.Number))
	if err != nil {
		t.Fatal(err)
	}
	delta, _ := parseDecimal("0.125")
	area, err := base.add(delta)
	if err != nil {
		t.Fatal(err)
	}
	actual["totalAreaM2"] = json.Number(area.String())
	score := compare(t, expected, actual, extraction.TitleDeed)
	if score.Rows.Aligned != len(rowArray(expected, "shareholders")) || score.Fields.DifferentValues != 2 {
		t.Fatal("deed alignment changed when row order changed")
	}
	checkDecimal(t, score.Numeric[0].MaxAbsoluteError, "0.125")

	first := rowArray(expected, "shareholders")[0].(map[string]any)
	second := maps.Clone(first)
	second["shareFraction"] = "2/3"
	expected["shareholders"] = []any{first, second}
	anonymous := maps.Clone(first)
	delete(anonymous, "nationalId")
	actual = maps.Clone(expected)
	actual["shareholders"] = []any{second, anonymous, first}
	score = compare(t, expected, actual, extraction.TitleDeed)
	if score.Rows != (RowCounts{2, 3, 2}) || score.Fields.DifferentValues != 2 || score.Fields.Extra() != 2 {
		t.Fatal("duplicate or absent national IDs did not follow alignment rules")
	}
}

func TestNumericErrorsRequireComparableUnitsAndCurrency(t *testing.T) {
	expected := testGold(t, "Q-DEV-01", Development)
	actual := clone(t, expected)
	actual["currency"] = "USD"
	rowArray(actual, "extractedItems")[0].(map[string]any)["unitOfMeasure"] = "m2"
	score := compare(t, expected, actual, extraction.Quotation)
	if numeric(t, score.Numeric, "quantity", "m3").ScoredPairs != 0 ||
		numeric(t, score.Numeric, "quotedUnitPrice", "TRY").ScoredPairs != 0 ||
		numeric(t, score.Numeric, "quantity", "kg").ScoredPairs != 1 {
		t.Fatal("units or currency changed the wrong comparable pairs")
	}
	actual["currency"] = "TRY"
	score = compare(t, expected, actual, extraction.Quotation)
	if numeric(t, score.Numeric, "quotedUnitPrice", "TRY").ScoredPairs != 3 {
		t.Fatal("three prices should be comparable")
	}
}

func TestNumericSummariesWeightScoredPairs(t *testing.T) {
	expected := testGold(t, "Q-DEV-01", Development)
	first := rowArray(expected, "extractedItems")[0].(map[string]any)
	second := maps.Clone(first)
	second["rawDescription"] = "Second placement"
	expected["extractedItems"] = []any{first, second}
	a, b := clone(t, expected), clone(t, expected)
	rowArray(a, "extractedItems")[1].(map[string]any)["quantity"] = json.Number("1252.50")
	b["extractedItems"] = rowArray(b, "extractedItems")[:1]
	rowArray(b, "extractedItems")[0].(map[string]any)["quantity"] = json.Number("1240.50")
	summary := qualityFrom(t, compare(t, expected, a, extraction.Quotation), compare(t, expected, b, extraction.Quotation))
	quantity := numericSummary(t, summary.NumericErrors, "quantity", "m3")
	if quantity.ExpectedPairs != 4 || quantity.ScoredPairs != 3 {
		t.Fatal("wrong pair coverage")
	}
	checkDecimal(t, quantity.MeanAbsoluteError, "4")
	checkDecimal(t, quantity.MaxAbsoluteError, "10")
}

func TestIncompleteReviewRequiresExactFlagsRetainedRowsAndUnsetValues(t *testing.T) {
	const price = "/extractedItems/0/quotedUnitPrice"
	entry := manifestDocument{
		Kind: extraction.Quotation, SourceRowCount: 1,
		UnavailableFields: []extraction.UnavailableField{{Pointer: price}},
	}
	validators := testValidators(t)
	for _, test := range []struct {
		name   string
		change func(map[string]any, map[string]any)
		pass   bool
	}{
		{"exact", func(map[string]any, map[string]any) {}, true},
		{"unexpected flag", func(p, _ map[string]any) { p["vendorName"] = nil }, false},
		{"no rows", func(p, _ map[string]any) { p["extractedItems"] = []any{} }, false},
		{"zero is present", func(_, r map[string]any) { r["quotedUnitPrice"] = json.Number("0") }, false},
		{"omission", func(_, r map[string]any) { delete(r, "quotedUnitPrice") }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := testGold(t, "Q-DEV-01", Development)
			row := rowArray(payload, "extractedItems")[0].(map[string]any)
			payload["extractedItems"], row["quotedUnitPrice"] = []any{row}, nil
			test.change(payload, row)
			result := validators.ValidateResponse(entry.Kind, extraction.ProviderResponse{PayloadJSON: string(encode(t, payload))})
			if got := scoreIncomplete(entry, result); got.Passed != test.pass {
				t.Fatalf("unexpected incomplete score %+v", got)
			}
		})
	}
}

func TestTopLevelMissingFieldRequiresPayloadAndRetainedRows(t *testing.T) {
	entry := manifestDocument{
		Kind: extraction.TitleDeed, SourceRowCount: 1,
		UnavailableFields: []extraction.UnavailableField{{Pointer: "/city"}},
	}
	payload := testGold(t, "D-DEV-02", Development)
	delete(payload, "city")
	result := testValidators(t).ValidateResponse(entry.Kind, extraction.ProviderResponse{PayloadJSON: string(encode(t, payload))})
	if !scoreIncomplete(entry, result).Passed {
		t.Fatal("omitted top-level field should pass")
	}
	failure := scoreIncomplete(entry, recordFailure(entry.Kind, "failed", "failed"))
	if failure.Passed || len(failure.UnsetOnRetainedRows) != 0 {
		t.Fatal("failed extraction credited an absent payload")
	}
}

func TestNoCompleteCasesHasUndefinedQualityRates(t *testing.T) {
	quality := qualityFrom(t)
	checkRate(t, quality.FieldAccuracy, 0, 0)
	checkRate(t, quality.IncompleteReviewPassRate, 0, 0)
	for _, prf := range []PRF{quality.Fields.PRF, quality.Rows.PRF} {
		checkRate(t, prf.Precision, 0, 0)
		checkRate(t, prf.Recall, 0, 0)
		checkRate(t, prf.F1, 0, 0)
	}
}
