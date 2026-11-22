package extractionbench

import (
	"encoding/json"
	"math/big"
	"slices"

	"github.com/pinar-isler/BuildBid/validation/extraction"
)

type StatusCounts struct {
	Complete    int `json:"complete"`
	NeedsReview int `json:"needs_review"`
	Failed      int `json:"failed"`
}

type Latency struct {
	ObservedDocuments int      `json:"observedDocuments"`
	Mean              *Decimal `json:"mean"`
	P50               *Decimal `json:"p50"`
	P95               *Decimal `json:"p95"`
}

type TelemetryTotal struct {
	ObservedDocuments int          `json:"observedDocuments"`
	Total             *json.Number `json:"total"`
}

type Operations struct {
	Documents          int                       `json:"documents"`
	Statuses           StatusCounts              `json:"statuses"`
	InvalidJSONRate    Rate                      `json:"invalidJsonRate"`
	MissingPredictions int                       `json:"missingPredictions"`
	NoResponses        int                       `json:"noResponses"`
	LatencyMs          Latency                   `json:"latencyMs"`
	Tokens             map[string]TelemetryTotal `json:"tokens"`
	APICostUSD         TelemetryTotal            `json:"apiCostUsd"`
}

func summarizeOperations(documents []Document) (Operations, error) {
	ops := Operations{Documents: len(documents), Tokens: make(map[string]TelemetryTotal)}
	var invalidJSON int
	var latencies []Decimal
	var cost Decimal
	tokenTotals := make(map[string]*big.Int)
	for _, doc := range documents {
		switch doc.Status {
		case extraction.Complete:
			ops.Statuses.Complete++
		case extraction.NeedsReview:
			ops.Statuses.NeedsReview++
		case extraction.Failed:
			ops.Statuses.Failed++
		}
		if slices.Contains(doc.ErrorCodes, "invalid_json") {
			invalidJSON++
		}
		if slices.Contains(doc.ErrorCodes, "missing_prediction") {
			ops.MissingPredictions++
		}
		if slices.Contains(doc.ErrorCodes, "no_response") {
			ops.NoResponses++
		}
		if elapsed, ok := doc.Telemetry["latencyMs"]; ok {
			value, err := parseDecimal(elapsed)
			if err != nil {
				return Operations{}, err
			}
			latencies = append(latencies, value)
		}
		if observed, ok := doc.Telemetry["costUsd"]; ok {
			value, err := parseDecimal(observed)
			if err != nil {
				return Operations{}, err
			}
			cost, err = cost.add(value)
			if err != nil {
				return Operations{}, err
			}
			ops.APICostUSD.ObservedDocuments++
		}
		for _, field := range tokenFields {
			if number, ok := doc.Telemetry[field]; ok {
				value, _ := new(big.Int).SetString(number.String(), 10)
				if tokenTotals[field] == nil {
					tokenTotals[field] = new(big.Int)
				}
				tokenTotals[field].Add(tokenTotals[field], value)
				total := ops.Tokens[field]
				total.ObservedDocuments++
				ops.Tokens[field] = total
			}
		}
	}
	ops.InvalidJSONRate = rate(invalidJSON, len(documents))
	if ops.APICostUSD.ObservedDocuments != 0 {
		total := json.Number(cost.String())
		ops.APICostUSD.Total = &total
	}
	for _, field := range tokenFields {
		total := ops.Tokens[field]
		if value := tokenTotals[field]; value != nil {
			number := json.Number(value.String())
			total.Total = &number
		}
		ops.Tokens[field] = total
	}
	latency, err := summarizeLatency(latencies)
	ops.LatencyMs = latency
	return ops, err
}

func summarizeLatency(values []Decimal) (Latency, error) {
	count := len(values)
	latency := Latency{ObservedDocuments: count}
	if count == 0 {
		return latency, nil
	}
	// The reference sorts before summing, which matters after rounding.
	slices.SortStableFunc(values, Decimal.cmp)
	var total Decimal
	for _, elapsed := range values {
		var err error
		total, err = total.add(elapsed)
		if err != nil {
			return Latency{}, err
		}
	}
	mean, err := total.quo(decimalInt(int64(count)))
	if err != nil {
		return Latency{}, err
	}
	latency.Mean, latency.P50, latency.P95 = &mean, &values[(count+1)/2-1], &values[(95*count+99)/100-1]
	return latency, nil
}
