// Package matchingbench scores BOQ decisions on BuildBid's labeled corpus.
package matchingbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"unicode/utf8"

	"github.com/pinar-isler/BuildBid/validation/matching"
)

const (
	Development = "development"
	HeldOut     = "held-out"
)

type corpusManifest struct {
	Version int `json:"version"`
	Splits  map[string]struct {
		BOQProjectID string `json:"boqProjectId"`
		Documents    []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Outcome string `json:"outcome"`
		} `json:"documents"`
	} `json:"splits"`
}

type reconciliationLabel struct {
	ItemIndex          *int     `json:"itemIndex"`
	Label              string   `json:"label"`
	CandidateItemCodes []string `json:"candidateItemCodes"`
}

type Row struct {
	FixtureID         string          `json:"fixtureId"`
	ItemIndex         int             `json:"itemIndex"`
	Label             string          `json:"label"`
	ExpectedItemCodes []string        `json:"expectedItemCodes"`
	Prediction        matching.Result `json:"prediction"`
}

type Rate struct {
	Count int      `json:"count"`
	Total int      `json:"total"`
	Rate  *float64 `json:"rate"`
}

type Metrics struct {
	Top1Accuracy             Rate `json:"top1Accuracy"`
	AutoMatchPrecision       Rate `json:"autoMatchPrecision"`
	AutoMatchCoverage        Rate `json:"autoMatchCoverage"`
	ManualReviewRate         Rate `json:"manualReviewRate"`
	AmbiguousRoutedForReview Rate `json:"ambiguousRoutedForReview"`
	UnmatchedLeftUnlinked    Rate `json:"unmatchedLeftUnlinked"`
	UnmatchedIdentified      Rate `json:"unmatchedIdentified"`
	CorrectDecisions         Rate `json:"correctDecisions"`
}

type Settings struct {
	CandidateThreshold float64 `json:"candidateThreshold"`
	AutoMatchThreshold float64 `json:"autoMatchThreshold"`
	AmbiguityMargin    float64 `json:"ambiguityMargin"`
}

type Report struct {
	CorpusVersion         int      `json:"corpusVersion"`
	Split                 string   `json:"split"`
	EvaluationPhase       string   `json:"evaluationPhase"`
	IndependentEvaluation bool     `json:"independentEvaluation"`
	BOQProject            string   `json:"boqProject"`
	Settings              Settings `json:"settings"`
	Metrics               Metrics  `json:"metrics"`
	Rows                  []Row    `json:"rows"`
}

// Evaluate runs the matcher on gold quotation rows for one corpus split.
// corpus must be rooted at the directory containing manifest.json.
func Evaluate(corpus fs.FS, split string) (Report, error) {
	var phase string
	switch split {
	case Development:
		phase = "development"
	case HeldOut:
		phase = "post_adjustment_regression"
	default:
		return Report{}, fmt.Errorf("unknown matching split %q", split)
	}

	manifest, err := readJSON[corpusManifest](corpus, "manifest.json")
	if err != nil {
		return Report{}, err
	}
	group, ok := manifest.Splits[split]
	if !ok {
		return Report{}, fmt.Errorf("manifest is missing split %q", split)
	}
	projects, err := readJSON[map[string][]matching.BOQItem](corpus, "master-boq.json")
	if err != nil {
		return Report{}, err
	}
	boq, ok := projects[group.BOQProjectID]
	if !ok {
		return Report{}, fmt.Errorf("master BOQ is missing project %q", group.BOQProjectID)
	}
	matcher, err := matching.NewBOQMatcher(boq)
	if err != nil {
		return Report{}, fmt.Errorf("master BOQ project %q: %w", group.BOQProjectID, err)
	}

	goldDir := path.Join(split, "ground-truth")
	predictions := make(map[string][]matching.Result)
	fixtures := []string{}
	for _, document := range group.Documents {
		if document.Kind != "quotation" || document.Outcome != "complete" {
			continue
		}
		id := document.ID
		quotation, err := readJSON[matching.Quotation](corpus, path.Join(goldDir, id+".json"))
		if err != nil {
			return Report{}, err
		}
		if quotation.ExtractedItems == nil {
			return Report{}, fmt.Errorf("quotation %q is missing extractedItems", id)
		}
		if _, exists := predictions[id]; !exists {
			fixtures = append(fixtures, id)
		}
		predictions[id] = matcher.MatchQuotation(quotation)
	}

	// Do not read the answer labels until all predictions have been made.
	labels, err := readJSON[map[string][]reconciliationLabel](corpus, path.Join(goldDir, "reconciliation.json"))
	if err != nil {
		return Report{}, err
	}
	rows, err := labeledRows(fixtures, predictions, labels)
	if err != nil {
		return Report{}, err
	}
	return Report{
		CorpusVersion: manifest.Version, Split: split, EvaluationPhase: phase,
		IndependentEvaluation: false, BOQProject: group.BOQProjectID,
		Settings: Settings{
			CandidateThreshold: matching.MinCandidateScore,
			AutoMatchThreshold: matching.MinAutoMatchScore,
			AmbiguityMargin:    matching.AmbiguityMargin,
		},
		Metrics: summarize(rows), Rows: rows,
	}, nil
}

func readJSON[T any](root fs.FS, name string) (T, error) {
	var data T
	encoded, err := fs.ReadFile(root, name)
	if err != nil {
		return data, err
	}
	if !utf8.Valid(encoded) {
		return data, fmt.Errorf("%s: invalid UTF-8", name)
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return data, fmt.Errorf("%s: %w", name, err)
	}
	return data, nil
}

func labeledRows(fixtures []string, predictions map[string][]matching.Result, labels map[string][]reconciliationLabel) ([]Row, error) {
	inventoryError := errors.New("the complete quotation inventory differs from the reconciliation labels")
	if len(labels) != len(predictions) {
		return nil, inventoryError
	}
	rows := []Row{}
	for _, id := range fixtures {
		reviewed, ok := labels[id]
		if !ok {
			return nil, inventoryError
		}
		decisions := predictions[id]
		if len(reviewed) != len(decisions) {
			return nil, fmt.Errorf("reconciliation labels do not cover every row of %s", id)
		}
		seen := make([]bool, len(decisions))
		for _, label := range reviewed {
			index := label.ItemIndex
			if index == nil || *index < 0 || *index >= len(decisions) || seen[*index] {
				return nil, fmt.Errorf("reconciliation labels do not cover every row of %s", id)
			}
			switch label.Label {
			case "correct", "ambiguous", "unmatched":
			default:
				return nil, fmt.Errorf("unknown reconciliation label %q for %s row %d", label.Label, id, *index)
			}
			seen[*index] = true
			rows = append(rows, Row{id, *index, label.Label, label.CandidateItemCodes, decisions[*index]})
		}
	}
	return rows, nil
}

func rate(count, total int) Rate {
	result := Rate{Count: count, Total: total}
	if total != 0 {
		value := float64(count) / float64(total)
		result.Rate = &value
	}
	return result
}

func summarize(rows []Row) Metrics {
	var linkable, autoMatches, correctLinks, manualReviews int
	var ambiguous, reviewed, unmatched, unlinked, identified int
	for _, row := range rows {
		prediction := row.Prediction
		if prediction.Status == matching.Matched {
			autoMatches++
		} else if prediction.Status == matching.Ambiguous {
			manualReviews++
		}
		switch row.Label {
		case "correct":
			linkable++
			if prediction.Status == matching.Matched && prediction.ItemCode != nil &&
				slices.Contains(row.ExpectedItemCodes, *prediction.ItemCode) {
				correctLinks++
			}
		case "ambiguous":
			ambiguous++
			if prediction.Status == matching.Ambiguous {
				reviewed++
			}
		case "unmatched":
			unmatched++
			if prediction.ItemCode == nil {
				unlinked++
			}
			if prediction.Status == matching.Unmatched {
				identified++
			}
		}
	}
	return Metrics{
		Top1Accuracy:             rate(correctLinks, linkable),
		AutoMatchPrecision:       rate(correctLinks, autoMatches),
		AutoMatchCoverage:        rate(autoMatches, len(rows)),
		ManualReviewRate:         rate(manualReviews, len(rows)),
		AmbiguousRoutedForReview: rate(reviewed, ambiguous),
		UnmatchedLeftUnlinked:    rate(unlinked, unmatched),
		UnmatchedIdentified:      rate(identified, unmatched),
		CorrectDecisions:         rate(correctLinks+reviewed+identified, len(rows)),
	}
}
