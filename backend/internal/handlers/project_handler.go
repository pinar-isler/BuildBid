package handlers

import (
	"strconv"

	"buildbid/backend/internal/config"
	"buildbid/backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

type CreateProjectRequest struct {
	ProjectID  string `json:"project_id"`
	Title      string `json:"title"`
	ClientName string `json:"client_name"`
	Location   string `json:"location"`
	Currency   string `json:"currency"`
	Status     string `json:"status"`
}

// CreateProject yeni bir ihale çalışma alanı oluşturur.
func CreateProject(c *fiber.Ctx) error {
	var req CreateProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz JSON gövdesi",
		})
	}

	if req.ProjectID == "" || req.Title == "" || req.ClientName == "" || req.Currency == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "project_id, title, client_name ve currency alanları zorunludur",
		})
	}

	project := models.ProcurementProject{
		ProjectID:  req.ProjectID,
		Title:      req.Title,
		ClientName: req.ClientName,
		Location:   req.Location,
		Currency:   req.Currency,
	}
	if req.Status != "" {
		project.Status = req.Status
	}

	if err := config.DB.Create(&project).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Proje kaydedilemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(project)
}

// GetProjects tüm ihale projelerini listeler.
func GetProjects(c *fiber.Ctx) error {
	var projects []models.ProcurementProject
	if err := config.DB.Find(&projects).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Projeler listelenemedi",
			"details": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(projects)
}

// GetProjectByID tek bir projenin detayını ve bağlı kayıtlarını getirir.
func GetProjectByID(c *fiber.Ctx) error {
	idParam := c.Params("id")
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz proje ID",
		})
	}

	var project models.ProcurementProject
	err = config.DB.
		Preload("MasterBOQItems").
		Preload("VendorQuotations").
		Preload("TitleDeedRecord").
		First(&project, uint(id)).Error

	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Proje bulunamadı",
		})
	}

	return c.Status(fiber.StatusOK).JSON(project)
}
