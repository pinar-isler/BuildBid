package config

import (
	"log"
	"os"
	"time"

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

	// GORM, tüm write işlemlerini varsayılan olarak transaction içinde yürütür, bu da ACID prensiplerini destekler.
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Fatalf("Veritabanı bağlantısı kurulamadı: %v", err)
	}

	// Connection Pooling yapılandırması
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Veritabanı instance'ı alınamadı: %v", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	DB = db
	log.Println("PostgreSQL veritabanı bağlantısı başarıyla kuruldu.")
}
