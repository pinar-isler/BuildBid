package handlers

import (
	"strconv"

	"buildbid/backend/internal/config"
	"buildbid/backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

type CreateBOQItemRequest struct {
	ItemCode           string  `json:"item_code"`
	Description        string  `json:"description"`
	UnitOfMeasure      string  `json:"unit_of_measure"`
	EstimatedQuantity  float64 `json:"estimated_quantity"`
	EstimatedUnitPrice float64 `json:"estimated_unit_price"`
}

// CreateBOQItem belirli bir projeye yeni bir MasterBOQItem ekler.
func CreateBOQItem(c *fiber.Ctx) error {
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

	var req CreateBOQItemRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz JSON gövdesi",
		})
	}

	if req.ItemCode == "" || req.Description == "" || req.UnitOfMeasure == "" || req.EstimatedQuantity <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "item_code, description, unit_of_measure ve pozitif estimated_quantity alanları zorunludur",
		})
	}

	item := models.MasterBOQItem{
		ProcurementProjectID: uint(projectID),
		ItemCode:             req.ItemCode,
		Description:          req.Description,
		UnitOfMeasure:        req.UnitOfMeasure,
		EstimatedQuantity:    req.EstimatedQuantity,
		EstimatedUnitPrice:   req.EstimatedUnitPrice,
	}

	if err := config.DB.Create(&item).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Metraj kalemi kaydedilemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(item)
}

// GetBOQItemsByProject belirli bir projenin tüm metraj kalemlerini listeler.
func GetBOQItemsByProject(c *fiber.Ctx) error {
	projectIDParam := c.Params("projectId")
	projectID, err := strconv.ParseUint(projectIDParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz proje ID",
		})
	}

	var items []models.MasterBOQItem
	if err := config.DB.Where("procurement_project_id = ?", uint(projectID)).Find(&items).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Metraj kalemleri listelenemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(items)
}
