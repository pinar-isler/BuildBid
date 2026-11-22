package handlers

import (
	"errors"
	"strconv"
	"time"

	"buildbid/backend/internal/config"
	"buildbid/backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type CreateQuotationItemInput struct {
	QuoteItemID     string  `json:"quote_item_id"`
	RawDescription  string  `json:"raw_description"`
	Quantity        float64 `json:"quantity"`
	UnitOfMeasure   string  `json:"unit_of_measure"`
	QuotedUnitPrice float64 `json:"quoted_unit_price"`
	IsTaxIncluded   bool    `json:"is_tax_included"`
}

type CreateQuotationRequest struct {
	QuotationID        string                     `json:"quotation_id"`
	VendorName         string                     `json:"vendor_name"`
	DocumentHashSHA256 string                     `json:"document_hash_sha256"`
	SubmissionDate     *time.Time                 `json:"submission_date"`
	TotalQuotedAmount  float64                    `json:"total_quoted_amount"`
	ExtractionStatus   string                     `json:"extraction_status"`
	Items              []CreateQuotationItemInput `json:"items"`
}

// CreateQuotation bir projeye tedarikçi teklifi ve satır kalemlerini ekler (SHA-256 cache destekli).
func CreateQuotation(c *fiber.Ctx) error {
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
			"error": "Bağlı proje bulunamadı",
		})
	}

	var req CreateQuotationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz JSON gövdesi",
		})
	}

	if req.QuotationID == "" || req.VendorName == "" || req.DocumentHashSHA256 == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "quotation_id, vendor_name ve document_hash_sha256 alanları zorunludur",
		})
	}

	// SHA-256 Cache Kontrolü
	var existing models.VendorQuotation
	err = config.DB.Preload("QuotationItems").
		Where("document_hash_sha256 = ?", req.DocumentHashSHA256).
		First(&existing).Error
	if err == nil {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"cached":    true,
			"quotation": existing,
		})
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Önbellek kontrolü başarısız",
			"details": err.Error(),
		})
	}

	submissionDate := time.Now()
	if req.SubmissionDate != nil {
		submissionDate = *req.SubmissionDate
	}

	extractionStatus := "Parsed"
	if req.ExtractionStatus != "" {
		extractionStatus = req.ExtractionStatus
	}

	// Toplam tutar gönderilmediyse satır kalemlerinden deterministik hesapla
	totalAmount := req.TotalQuotedAmount
	if totalAmount == 0 && len(req.Items) > 0 {
		for _, it := range req.Items {
			totalAmount += it.Quantity * it.QuotedUnitPrice
		}
	}

	quotation := models.VendorQuotation{
		ProcurementProjectID: uint(projectID),
		QuotationID:          req.QuotationID,
		VendorName:           req.VendorName,
		DocumentHashSHA256:   req.DocumentHashSHA256,
		SubmissionDate:       submissionDate,
		TotalQuotedAmount:    totalAmount,
		ExtractionStatus:     extractionStatus,
	}

	// Atomik işlem (ACID): Teklif ve satır kalemlerini tek transaction içinde kaydet
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&quotation).Error; err != nil {
			return err
		}

		if len(req.Items) > 0 {
			items := make([]models.QuotationItem, 0, len(req.Items))
			for _, it := range req.Items {
				items = append(items, models.QuotationItem{
					VendorQuotationID: quotation.ID,
					QuoteItemID:       it.QuoteItemID,
					RawDescription:    it.RawDescription,
					Quantity:          it.Quantity,
					UnitOfMeasure:     it.UnitOfMeasure,
					QuotedUnitPrice:   it.QuotedUnitPrice,
					IsTaxIncluded:     it.IsTaxIncluded,
				})
			}
			if err := tx.Create(&items).Error; err != nil {
				return err
			}
			quotation.QuotationItems = items
		}

		return nil
	})

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Teklif kaydedilemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"cached":    false,
		"quotation": quotation,
	})
}

// GetQuotationsByProject belirli bir projeye ait tüm teklifleri ve satır kalemlerini listeler.
func GetQuotationsByProject(c *fiber.Ctx) error {
	projectIDParam := c.Params("projectId")
	projectID, err := strconv.ParseUint(projectIDParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz proje ID",
		})
	}

	var quotations []models.VendorQuotation
	if err := config.DB.
		Preload("QuotationItems").
		Where("procurement_project_id = ?", uint(projectID)).
		Find(&quotations).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Teklifler listelenemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(quotations)
}

// GetQuotationByID tek bir teklifin detayını, kalemlerini ve varsa eşleştirme kayıtlarını getirir.
func GetQuotationByID(c *fiber.Ctx) error {
	idParam := c.Params("id")
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz teklif ID",
		})
	}

	var quotation models.VendorQuotation
	if err := config.DB.
		Preload("QuotationItems.ReconciliationRecord").
		First(&quotation, uint(id)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Teklif bulunamadı",
		})
	}

	return c.Status(fiber.StatusOK).JSON(quotation)
}
