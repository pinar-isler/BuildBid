package handlers

import (
	"fmt"
	"strconv"

	"buildbid/backend/internal/config"
	"buildbid/backend/internal/models"
	"buildbid/backend/internal/services"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type VerifyReconciliationRequest struct {
	Action          string `json:"action"` // "approve", "override", "discard"
	MasterBOQItemID *uint  `json:"master_boq_item_id"`
}

// RunReconciliation bir teklif için algoritmik eşleştirmeyi çalıştırır ve ReconciliationRecord kayıtlarını oluşturur.
func RunReconciliation(c *fiber.Ctx) error {
	idParam := c.Params("id")
	quotationID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz teklif ID",
		})
	}

	var quotation models.VendorQuotation
	if err := config.DB.Preload("QuotationItems").First(&quotation, uint(quotationID)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Teklif bulunamadı",
		})
	}

	if len(quotation.QuotationItems) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Eşleştirilecek teklif satır kalemi bulunamadı",
		})
	}

	var boqItems []models.MasterBOQItem
	if err := config.DB.Where("procurement_project_id = ?", quotation.ProcurementProjectID).Find(&boqItems).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Ana metraj kalemleri alınamadı",
		})
	}
	if len(boqItems) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Bu projeye ait MasterBOQItem bulunamadı",
		})
	}

	records := make([]models.ReconciliationRecord, 0, len(quotation.QuotationItems))

	err = config.DB.Transaction(func(tx *gorm.DB) error {
		for _, qItem := range quotation.QuotationItems {
			bestBOQ := boqItems[0]
			bestScore := -1.0

			for _, boq := range boqItems {
				score := services.CalculateHybridScore(qItem, boq)
				if score > bestScore {
					bestScore = score
					bestBOQ = boq
				}
			}

			rec := models.ReconciliationRecord{
				QuotationItemID:  qItem.ID,
				MasterBOQItemID:  bestBOQ.ID,
				ReconciliationID: fmt.Sprintf("REC-Q%d-B%d", qItem.ID, bestBOQ.ID),
				ConfidenceScore:  bestScore,
				MatchStatus:      services.DetermineMatchStatus(bestScore),
				IsHumanVerified:  false,
			}

			// Aynı QuotationItemID için daha önce kayıt varsa güncelle (Upsert)
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "quotation_item_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"master_boq_item_id", "confidence_score", "match_status", "is_human_verified"}),
			}).Create(&rec).Error; err != nil {
				return err
			}

			records = append(records, rec)
		}
		return nil
	})

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Eşleştirme kayıtları oluşturulamadı",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"quotation_id": quotation.ID,
		"reconciled":   len(records),
		"records":      records,
	})
}

// VerifyReconciliation mühendisin eşleşmeyi onaylamasını, değiştirmesini veya reddetmesini sağlar (HITL).
func VerifyReconciliation(c *fiber.Ctx) error {
	idParam := c.Params("id")
	recID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz eşleştirme ID",
		})
	}

	var req VerifyReconciliationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz JSON gövdesi",
		})
	}

	var record models.ReconciliationRecord
	if err := config.DB.First(&record, uint(recID)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Eşleştirme kaydı bulunamadı",
		})
	}

	switch req.Action {
	case "approve":
		record.MatchStatus = "HumanApproved"
		record.IsHumanVerified = true
	case "override":
		if req.MasterBOQItemID == nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "override işlemi için master_boq_item_id zorunludur",
			})
		}
		record.MasterBOQItemID = *req.MasterBOQItemID
		record.ConfidenceScore = 100.0
		record.MatchStatus = "ManuallyOverridden"
		record.IsHumanVerified = true
	case "discard":
		record.MatchStatus = "DiscardedOrUncataloged"
		record.IsHumanVerified = true
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz action. Beklenen değerler: approve, override, discard",
		})
	}

	if err := config.DB.Save(&record).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Eşleştirme güncellenemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(record)
}
