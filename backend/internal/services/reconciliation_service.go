package services

import (
	"math"
	"strings"

	"buildbid/backend/internal/models"
)

// LevenshteinDistance iki metin arasındaki karakter düzenleme mesafesini hesaplar (Türkçe karakter uyumlu).
func LevenshteinDistance(a, b string) int {
	ra := []rune(strings.ToLower(strings.TrimSpace(a)))
	rb := []rune(strings.ToLower(strings.TrimSpace(b)))

	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost

			minVal := del
			if ins < minVal {
				minVal = ins
			}
			if sub < minVal {
				minVal = sub
			}
			curr[j] = minVal
		}
		copy(prev, curr)
	}

	return prev[lb]
}

// CalculateTextSimilarity Levenshtein mesafesini 0-100 arası benzerlik yüzdesine çevirir.
func CalculateTextSimilarity(a, b string) float64 {
	ra := []rune(strings.ToLower(strings.TrimSpace(a)))
	rb := []rune(strings.ToLower(strings.TrimSpace(b)))
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	if maxLen == 0 {
		return 100.0
	}
	dist := LevenshteinDistance(a, b)
	return (1.0 - float64(dist)/float64(maxLen)) * 100.0
}

// CalculateUnitScore birim eşleşmesini kontrol eder (eşleşirse 100, değilse 0).
func CalculateUnitScore(u1, u2 string) float64 {
	if strings.EqualFold(strings.TrimSpace(u1), strings.TrimSpace(u2)) {
		return 100.0
	}
	return 0.0
}

// CalculatePriceScore teklif fiyatı ile tahmini birim fiyat arasındaki sapmaya göre 0-100 arası puan verir.
func CalculatePriceScore(quoted, estimated float64) float64 {
	if estimated <= 0 {
		return 0.0
	}
	diffRatio := math.Abs(quoted-estimated) / estimated
	if diffRatio >= 1.0 {
		return 0.0
	}
	return (1.0 - diffRatio) * 100.0
}

// CalculateHybridScore RAD ağırlıklarına göre (Metin %60, Birim %25, Fiyat %15) hibrit güven skoru hesaplar.
func CalculateHybridScore(qItem models.QuotationItem, boqItem models.MasterBOQItem) float64 {
	textScore := CalculateTextSimilarity(qItem.RawDescription, boqItem.Description)
	unitScore := CalculateUnitScore(qItem.UnitOfMeasure, boqItem.UnitOfMeasure)
	priceScore := CalculatePriceScore(qItem.QuotedUnitPrice, boqItem.EstimatedUnitPrice)

	hybrid := (textScore * 0.60) + (unitScore * 0.25) + (priceScore * 0.15)
	return math.Round(hybrid*100) / 100
}

// DetermineMatchStatus hibrit skora göre eşleşme durumunu belirler.
func DetermineMatchStatus(score float64) string {
	switch {
	case score >= 85.0:
		return "AutoMatched"
	case score >= 60.0:
		return "PendingReview"
	default:
		return "DiscardedOrUncataloged"
	}
}
