package models

import (
	"time"

	"gorm.io/gorm"
)

// VendorQuotation, tedarikçiden gelen PDF/Excel teklif meta verilerini tutar[cite: 3].
type VendorQuotation struct {
	gorm.Model
	ProcurementProjectID uint   // Foreign key
	QuotationID          string `gorm:"type:varchar(100);uniqueIndex;not null"`
	VendorName           string `gorm:"type:varchar(255);not null"`
	DocumentHashSHA256   string `gorm:"type:varchar(64);uniqueIndex;not null"`
	SubmissionDate       time.Time
	TotalQuotedAmount    float64 `gorm:"type:decimal(15,2)"`
	ExtractionStatus     string  `gorm:"type:varchar(50)"`

	// 1-to-N İlişki: Bir teklif dosyasının içindeki satır kalemleri[cite: 3]
	QuotationItems []QuotationItem `gorm:"foreignKey:VendorQuotationID"`
}
