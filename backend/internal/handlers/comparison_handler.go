package handlers

import (
	"math"
	"strconv"

	"buildbid/backend/internal/config"
	"buildbid/backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

type VendorCell struct {
	QuotationID     uint    `json:"quotation_id"`
	VendorName      string  `json:"vendor_name"`
	QuotedUnitPrice float64 `json:"quoted_unit_price"`
	Quantity        float64 `json:"quantity"`
	LineTotal       float64 `json:"line_total"`
	VariancePercent float64 `json:"variance_percent"`
	IsLowest        bool    `json:"is_lowest"`
	IsOmitted       bool    `json:"is_omitted"`
}

type MatrixRow struct {
	MasterBOQItemID    uint         `json:"master_boq_item_id"`
	ItemCode           string       `json:"item_code"`
	Description        string       `json:"description"`
	UnitOfMeasure      string       `json:"unit_of_measure"`
	EstimatedQuantity  float64      `json:"estimated_quantity"`
	EstimatedUnitPrice float64      `json:"estimated_unit_price"`
	BaselineLineTotal  float64      `json:"baseline_line_total"`
	VendorBids         []VendorCell `json:"vendor_bids"`
}

type VendorSummary struct {
	QuotationID          uint    `json:"quotation_id"`
	VendorName           string  `json:"vendor_name"`
	TotalEvaluatedAmount float64 `json:"total_evaluated_amount"`
	VariancePercent      float64 `json:"variance_percent"`
	OmittedItemCount     int     `json:"omitted_item_count"`
	IsIncomplete         bool    `json:"is_incomplete"`
}

// GetComparisonMatrix proje bazında çoklu tedarikçi karşılaştırma ve sapma matrisini deterministik olarak üretir.
func GetComparisonMatrix(c *fiber.Ctx) error {
	projectIDParam := c.Params("projectId")
	projectID, err := strconv.ParseUint(projectIDParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz proje ID",
		})
	}

	var project models.ProcurementProject
	if err := config.DB.First(&project, uint(projectID)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Proje bulunamadı",
		})
	}

	var boqItems []models.MasterBOQItem
	if err := config.DB.Where("procurement_project_id = ?", uint(projectID)).Find(&boqItems).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Ana metraj kalemleri alınamadı",
		})
	}

	var quotations []models.VendorQuotation
	if err := config.DB.
		Preload("QuotationItems.ReconciliationRecord").
		Where("procurement_project_id = ?", uint(projectID)).
		Find(&quotations).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Teklifler alınamadı",
		})
	}

	// Hızlı erişim haritası: [quotationID][masterBOQItemID] -> QuotationItem
	reconciledMap := make(map[uint]map[uint]models.QuotationItem)
	for _, q := range quotations {
		reconciledMap[q.ID] = make(map[uint]models.QuotationItem)
		for _, item := range q.QuotationItems {
			rec := item.ReconciliationRecord
			if rec == nil {
				continue
			}
			// Yalnızca geçerli eşleşmeleri matrise dahil et
			if rec.MatchStatus == "AutoMatched" || rec.MatchStatus == "HumanApproved" || rec.MatchStatus == "ManuallyOverridden" {
				reconciledMap[q.ID][rec.MasterBOQItemID] = item
			}
		}
	}

	rows := make([]MatrixRow, 0, len(boqItems))
	vendorTotals := make(map[uint]float64)
	vendorOmitted := make(map[uint]int)
	baselineTotal := 0.0

	for _, boq := range boqItems {
		baselineLineTotal := boq.EstimatedQuantity * boq.EstimatedUnitPrice
		baselineTotal += baselineLineTotal

		row := MatrixRow{
			MasterBOQItemID:    boq.ID,
			ItemCode:           boq.ItemCode,
			Description:        boq.Description,
			UnitOfMeasure:      boq.UnitOfMeasure,
			EstimatedQuantity:  boq.EstimatedQuantity,
			EstimatedUnitPrice: boq.EstimatedUnitPrice,
			BaselineLineTotal:  math.Round(baselineLineTotal*100) / 100,
			VendorBids:         make([]VendorCell, 0, len(quotations)),
		}

		minPrice := math.MaxFloat64
		hasValidBid := false

		for _, q := range quotations {
			qItem, exists := reconciledMap[q.ID][boq.ID]
			if !exists {
				vendorOmitted[q.ID]++
				row.VendorBids = append(row.VendorBids, VendorCell{
					QuotationID: q.ID,
					VendorName:  q.VendorName,
					IsOmitted:   true,
				})
				continue
			}

			lineTotal := qItem.Quantity * qItem.QuotedUnitPrice
			vendorTotals[q.ID] += lineTotal

			variancePct := 0.0
			if boq.EstimatedUnitPrice > 0 {
				variancePct = ((qItem.QuotedUnitPrice - boq.EstimatedUnitPrice) / boq.EstimatedUnitPrice) * 100.0
			}

			if qItem.QuotedUnitPrice < minPrice {
				minPrice = qItem.QuotedUnitPrice
				hasValidBid = true
			}

			row.VendorBids = append(row.VendorBids, VendorCell{
				QuotationID:     q.ID,
				VendorName:      q.VendorName,
				QuotedUnitPrice: qItem.QuotedUnitPrice,
				Quantity:        qItem.Quantity,
				LineTotal:       math.Round(lineTotal*100) / 100,
				VariancePercent: math.Round(variancePct*100) / 100,
				IsOmitted:       false,
			})
		}

		// Satırdaki en düşük birim fiyatı işaretle
		if hasValidBid {
			for i := range row.VendorBids {
				if !row.VendorBids[i].IsOmitted && row.VendorBids[i].QuotedUnitPrice == minPrice {
					row.VendorBids[i].IsLowest = true
				}
			}
		}

		rows = append(rows, row)
	}

	summaries := make([]VendorSummary, 0, len(quotations))
	for _, q := range quotations {
		total := vendorTotals[q.ID]
		omittedCount := vendorOmitted[q.ID]
		variancePct := 0.0
		if baselineTotal > 0 {
			variancePct = ((total - baselineTotal) / baselineTotal) * 100.0
		}

		summaries = append(summaries, VendorSummary{
			QuotationID:          q.ID,
			VendorName:           q.VendorName,
			TotalEvaluatedAmount: math.Round(total*100) / 100,
			VariancePercent:      math.Round(variancePct*100) / 100,
			OmittedItemCount:     omittedCount,
			IsIncomplete:         omittedCount > 0,
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"project_id":       project.ID,
		"project_title":    project.Title,
		"currency":         project.Currency,
		"baseline_total":   math.Round(baselineTotal*100) / 100,
		"rows":             rows,
		"vendor_summaries": summaries,
	})
}
