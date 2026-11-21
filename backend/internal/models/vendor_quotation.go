package models

import (
	"time"

	"gorm.io/gorm"
)

// VendorQuotation, tedarikçiden gelen PDF/Excel teklif meta verilerini tutar.
type VendorQuotation struct {
	gorm.Model
	ProcurementProjectID uint      `gorm:"not null;index"`
	QuotationID          string    `gorm:"type:varchar(100);uniqueIndex;not null"`
	VendorName           string    `gorm:"type:varchar(255);not null"`
	DocumentHashSHA256   string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	SubmissionDate       time.Time `gorm:"type:timestamptz"`
	TotalQuotedAmount    float64   `gorm:"type:decimal(15,2)"`
	ExtractionStatus     string    `gorm:"type:varchar(50);default:'Pending'"`

	// İlişkiler
	ProcurementProject ProcurementProject `gorm:"foreignKey:ProcurementProjectID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	QuotationItems     []QuotationItem    `gorm:"foreignKey:VendorQuotationID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}
