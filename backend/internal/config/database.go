package config

import (
	"log"
	"os"
	"time"

	"buildbid/backend/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func ConnectDatabase() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL ortam değişkeni bulunamadı.")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		log.Fatalf("Veritabanı bağlantısı kurulamadı: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Veritabanı instance'ı alınamadı: %v", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := db.AutoMigrate(
		&models.ProcurementProject{},
		&models.TitleDeedRecord{},
		&models.MasterBOQItem{},
		&models.VendorQuotation{},
		&models.ShareholderEquity{},
		&models.QuotationItem{},
		&models.ReconciliationRecord{},
	); err != nil {
		log.Fatalf("Veritabanı tabloları migrate edilemedi: %v", err)
	}

	DB = db
	log.Println("PostgreSQL veritabanı bağlantısı ve migration işlemleri başarıyla tamamlandı.")
}
