package matchingbench

import (
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/pinar-isler/BuildBid/validation/matching"
)

func TestScoresUseAutomaticLinksForPrecisionAndCountAbstentions(t *testing.T) {
	a, c := "A", "C"
	report := summarize([]Row{
		{Label: "correct", ExpectedItemCodes: []string{"A"}, Prediction: matching.Result{Status: matching.Matched, ItemCode: &a}},
		{Label: "correct", ExpectedItemCodes: []string{"B"}, Prediction: matching.Result{Status: matching.Ambiguous}},
		{Label: "ambiguous", ExpectedItemCodes: []string{"C", "D"}, Prediction: matching.Result{Status: matching.Matched, ItemCode: &c}},
		{Label: "unmatched", Prediction: matching.Result{Status: matching.Ambiguous}},
	})
	half, zero, one, quarter := 0.5, 0.0, 1.0, 0.25
	want := Metrics{
		Top1Accuracy:             Rate{1, 2, &half},
		AutoMatchPrecision:       Rate{1, 2, &half},
		AutoMatchCoverage:        Rate{2, 4, &half},
		ManualReviewRate:         Rate{2, 4, &half},
		AmbiguousRoutedForReview: Rate{0, 1, &zero},
		UnmatchedLeftUnlinked:    Rate{1, 1, &one},
		UnmatchedIdentified:      Rate{0, 1, &zero},
		CorrectDecisions:         Rate{1, 4, &quarter},
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("metrics = %+v, want %+v", report, want)
	}
}

func TestDevelopmentBenchmarkCoversAllReviewedRows(t *testing.T) {
	report, err := Evaluate(os.DirFS("../../corpus/v1"), Development)
	if err != nil {
		t.Fatal(err)
	}
	if report.BOQProject != "SYN-DEV" || report.EvaluationPhase != "development" || report.IndependentEvaluation {
		t.Fatalf("unexpected benchmark provenance: %+v", report)
	}
	checkCorrectDecisions(t, report)
	reviewed := 0
	for _, row := range report.Rows {
		if row.Label != "ambiguous" {
			continue
		}
		reviewed++
		candidates := make([]string, 0, len(row.Prediction.Candidates))
		for _, candidate := range row.Prediction.Candidates {
			candidates = append(candidates, candidate.ItemCode)
		}
		slices.Sort(candidates)
		expected := slices.Clone(row.ExpectedItemCodes)
		slices.Sort(expected)
		if !slices.Equal(candidates, expected) {
			t.Fatalf("review candidates = %v, want %v", candidates, expected)
		}
	}
	if reviewed != 1 {
		t.Fatalf("got %d labeled ambiguous rows, want 1", reviewed)
	}
}

func TestHeldOutDirectoryIsReportedAsPostAdjustmentRegression(t *testing.T) {
	report, err := Evaluate(os.DirFS("../../corpus/v1"), HeldOut)
	if err != nil {
		t.Fatal(err)
	}
	if report.Split != "held-out" || report.BOQProject != "SYN-HOLD" ||
		report.EvaluationPhase != "post_adjustment_regression" || report.IndependentEvaluation {
		t.Fatalf("unexpected benchmark provenance: %+v", report)
	}
}

func checkCorrectDecisions(t *testing.T, report Report) {
	t.Helper()
	got := report.Metrics.CorrectDecisions
	if got.Count != 8 || got.Total != 8 || got.Rate == nil || *got.Rate != 1 {
		t.Fatalf("correct decisions = %+v, want 8/8", got)
	}
}

func TestEmptyDenominatorsAreUnavailable(t *testing.T) {
	if got := summarize(nil); got != (Metrics{}) {
		t.Fatalf("empty benchmark metrics = %+v, want zero counts and nil rates", got)
	}
}

func TestLabelsCoverEveryPredictedIndex(t *testing.T) {
	first, second, outOfRange := 0, 1, 2
	predictions := map[string][]matching.Result{
		"Q": {{Status: matching.Matched}, {Status: matching.Unmatched}},
	}
	for _, test := range []struct {
		name   string
		labels []reconciliationLabel
	}{
		{"missing row", []reconciliationLabel{{ItemIndex: &first, Label: "correct"}}},
		{"missing index", []reconciliationLabel{{Label: "correct"}, {ItemIndex: &second, Label: "unmatched"}}},
		{"duplicate", []reconciliationLabel{{ItemIndex: &first, Label: "correct"}, {ItemIndex: &first, Label: "correct"}}},
		{"out of range", []reconciliationLabel{{ItemIndex: &first, Label: "correct"}, {ItemIndex: &outOfRange, Label: "unmatched"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := labeledRows([]string{"Q"}, predictions, map[string][]reconciliationLabel{"Q": test.labels}); err == nil {
				t.Fatal("accepted invalid reconciliation labels")
			}
		})
	}
	rows, err := labeledRows([]string{"Q"}, predictions, map[string][]reconciliationLabel{
		"Q": {{ItemIndex: &second, Label: "unmatched"}, {ItemIndex: &first, Label: "correct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ItemIndex != second || rows[0].Prediction.Status != matching.Unmatched ||
		rows[1].ItemIndex != first || rows[1].Prediction.Status != matching.Matched {
		t.Fatalf("benchmark did not preserve label order and row alignment: %+v", rows)
	}
}
