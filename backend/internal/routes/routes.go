package routes

import (
	"buildbid/backend/internal/handlers"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App) {
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status":  "ok",
			"service": "buildbid-backend",
		})
	})

	api := app.Group("/api/v1")
	api.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"message": "BuildBid API v1 ready",
		})
	})

	projects := api.Group("/projects")
	projects.Post("/", handlers.CreateProject)
	projects.Get("/", handlers.GetProjects)
	projects.Get("/:id", handlers.GetProjectByID)
	projects.Post("/:projectId/boq-items", handlers.CreateBOQItem)
	projects.Get("/:projectId/boq-items", handlers.GetBOQItemsByProject)
	projects.Post("/:projectId/quotations", handlers.CreateQuotation)
	projects.Get("/:projectId/quotations", handlers.GetQuotationsByProject)
	projects.Get("/:projectId/comparison-matrix", handlers.GetComparisonMatrix)

	api.Get("/quotations/:id", handlers.GetQuotationByID)
	api.Post("/quotations/:id/reconcile", handlers.RunReconciliation)
	api.Patch("/reconciliations/:id", handlers.VerifyReconciliation)
}
