// Package matching reconciles extracted quotation rows against a project's BOQ.
package matching

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	MinCandidateScore = 0.60
	MinAutoMatchScore = 0.68
	AmbiguityMargin   = 0.08
)

type Status string

const (
	Matched   Status = "matched"
	Ambiguous Status = "ambiguous"
	Unmatched Status = "unmatched"
)

type BOQItem struct {
	ItemCode      string `json:"itemCode"`
	Description   string `json:"description"`
	UnitOfMeasure string `json:"unitOfMeasure"`
}

type QuotationItem struct {
	RawDescription string `json:"rawDescription"`
	UnitOfMeasure  string `json:"unitOfMeasure"`
}

// UnmarshalJSON retains the reference matcher's behavior for absent, null,
// or non-string row fields. Such fields cannot supply matching evidence.
func (row *QuotationItem) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("quotation row must be a JSON object")
	}
	*row = QuotationItem{
		RawDescription: jsonString(fields["rawDescription"]),
		UnitOfMeasure:  jsonString(fields["unitOfMeasure"]),
	}
	return nil
}

func jsonString(data json.RawMessage) string {
	var value string
	if len(data) != 0 && data[0] == '"' {
		// The raw value was already syntax-checked when reading the row.
		_ = json.Unmarshal(data, &value)
	}
	return value
}

type Quotation struct {
	ExtractedItems []QuotationItem `json:"extractedItems"`
}

type Candidate struct {
	ItemCode         string   `json:"item_code"`
	Description      string   `json:"description"`
	Score            float64  `json:"score"`
	UnconfirmedSpecs []string `json:"unconfirmed_specs"`
}

type Result struct {
	Status     Status      `json:"status"`
	ItemCode   *string     `json:"item_code"`
	Candidates []Candidate `json:"candidates"`
	Reason     string      `json:"reason"`
}

type boqEntry struct {
	item       BOQItem
	normalized description
}

// BOQMatcher indexes the BOQ by normalized unit. A constructed matcher can be
// reused across quotations from the same project.
type BOQMatcher struct {
	byUnit map[string][]boqEntry
}

func NewBOQMatcher(rows []BOQItem) (*BOQMatcher, error) {
	matcher := &BOQMatcher{byUnit: make(map[string][]boqEntry)}
	codes := make(map[string]struct{}, len(rows))
	invalidRow := errors.New("each BOQ row needs a unique code, a unit, and a description")
	for _, row := range rows {
		unit := NormalizeUnit(row.UnitOfMeasure)
		_, duplicate := codes[row.ItemCode]
		if strings.TrimFunc(row.ItemCode, isSpace) == "" || duplicate || unit == "" {
			return nil, invalidRow
		}
		normalized := normalizeDescription(row.Description)
		if normalized.text == "" {
			return nil, invalidRow
		}
		codes[row.ItemCode] = struct{}{}
		matcher.byUnit[unit] = append(matcher.byUnit[unit], boqEntry{row, normalized})
	}
	return matcher, nil
}

func (m *BOQMatcher) MatchItem(row QuotationItem) Result {
	result := Result{Status: Unmatched, Candidates: []Candidate{}}
	unit := NormalizeUnit(row.UnitOfMeasure)
	if unit == "" || strings.TrimFunc(row.RawDescription, isSpace) == "" {
		result.Reason = "The quotation row needs a description and unit."
		return result
	}
	quoted := normalizeDescription(row.RawDescription)
	if quoted.text == "" {
		result.Reason = "The quotation row has no usable description."
		return result
	}
	for _, entry := range m.byUnit[unit] {
		unconfirmed, compatible := compareSpecs(quoted, entry.normalized)
		if !compatible {
			continue
		}
		score := max(similarity(quoted.text, entry.normalized.text),
			similarity(quoted.sortedText, entry.normalized.sortedText))
		if score >= MinCandidateScore {
			result.Candidates = append(result.Candidates, Candidate{
				ItemCode: entry.item.ItemCode, Description: entry.item.Description,
				Score: score, UnconfirmedSpecs: unconfirmed,
			})
		}
	}
	sort.Slice(result.Candidates, func(i, j int) bool {
		left, right := result.Candidates[i], result.Candidates[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		return left.ItemCode < right.ItemCode
	})
	if len(result.Candidates) == 0 {
		result.Reason = "No sufficiently similar BOQ row has compatible units and specifications."
		return result
	}
	best := result.Candidates[0]
	result.Status = Ambiguous
	switch {
	case len(best.UnconfirmedSpecs) != 0:
		result.Reason = "The best candidate needs confirmation of " + strings.Join(best.UnconfirmedSpecs, ", ") + "."
	case len(result.Candidates) > 1 && best.Score-result.Candidates[1].Score <= AmbiguityMargin:
		result.Reason = "Multiple BOQ candidates have similar scores."
	case best.Score < MinAutoMatchScore:
		result.Reason = "The best candidate falls below the automatic match threshold."
	default:
		result.Status = Matched
		result.ItemCode = &best.ItemCode
		result.Reason = "The best candidate meets the automatic match rules."
	}
	return result
}

func (m *BOQMatcher) MatchQuotation(quotation Quotation) []Result {
	results := make([]Result, len(quotation.ExtractedItems))
	for i, row := range quotation.ExtractedItems {
		results[i] = m.MatchItem(row)
	}
	return results
}

// LevenshteinDistance counts edits in Unicode code points.
func LevenshteinDistance(left, right string) int {
	return distance([]rune(left), []rune(right))
}

func distance(left, right []rune) int {
	if len(left) < len(right) {
		left, right = right, left
	}
	previous, current := make([]int, len(right)+1), make([]int, len(right)+1)
	for i := range previous {
		previous[i] = i
	}
	for i, leftRune := range left {
		current[0] = i + 1
		for j, rightRune := range right {
			cost := previous[j]
			if leftRune != rightRune {
				cost++
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func similarity(left, right string) float64 {
	if left == right {
		return 1
	}
	leftRunes, rightRunes := []rune(left), []rune(right)
	return 1 - float64(distance(leftRunes, rightRunes))/float64(max(len(leftRunes), len(rightRunes)))
}
