package main

import (
	"log"

	"buildbid/backend/internal/config"
	"buildbid/backend/internal/routes"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
)

func main() {

	if err := godotenv.Load(); err != nil {
		log.Printf("Error loading .env file: %v", err)
	}

	// 1. Veritabanı bağlantısı (ACID uyumluluğu için GORM Start-up)
	config.ConnectDatabase()

	// 2. Fiber uygulamasını başlat (SDD Boundary Condition: 25 MB Limit)
	app := fiber.New(fiber.Config{
		BodyLimit: 25 * 1024 * 1024,
	})

	// 3. API Rotalarını entegre et (Ball-and-socket arayüzleri)
	routes.SetupRoutes(app)

	// 4. Sunucuyu dinlemeye başla
	log.Println("BuildBid Backend Sunucusu başlatılıyor...")
	log.Fatal(app.Listen(":8080"))
}
