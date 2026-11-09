package models

import "gorm.io/gorm"

// TitleDeedRecord, parsel niteliklerini ve alan verilerini tutar[cite: 3].
type TitleDeedRecord struct {
	gorm.Model
	ProcurementProjectID uint    // Foreign key
	DeedID               string  `gorm:"type:varchar(100);uniqueIndex;not null"`
	City                 string  `gorm:"type:varchar(100);not null"`
	District             string  `gorm:"type:varchar(100);not null"`
	BlockAda             string  `gorm:"type:varchar(50);not null"`
	ParcelParsel         string  `gorm:"type:varchar(50);not null"`
	TotalAreaM2          float64 `gorm:"type:decimal(15,2);not null"`

	// 1-to-N İlişki: Bu parsele ait paydaşlar[cite: 3]
	ShareholderEquities []ShareholderEquity `gorm:"foreignKey:TitleDeedRecordID"`
}
