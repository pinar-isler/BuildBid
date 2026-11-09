package models

import "gorm.io/gorm"

// QuotationItem, Multimodal AI tarafından yüklenen tedarikçi teklifinden (VendorQuotation) çıkarılan ham satır kalemlerini temsil eder[cite: 3].
type QuotationItem struct {
	gorm.Model
	VendorQuotationID uint    // Foreign key (VendorQuotation tablosu ile 1-to-N ilişki)
	QuoteItemID       string  `gorm:"type:varchar(100);not null;uniqueIndex"`
	RawDescription    string  `gorm:"type:text;not null"`
	Quantity          float64 `gorm:"type:decimal(15,2);not null"` // Deterministik matematiksel işlemler için Double/float64[cite: 3]
	UnitOfMeasure     string  `gorm:"type:varchar(20);not null"`
	QuotedUnitPrice   float64 `gorm:"type:decimal(15,2);not null"` // Deterministik matematiksel işlemler için Double/float64[cite: 3]
	IsTaxIncluded     bool    `gorm:"default:false"`

	// 1-to-1 İlişki: Bu kalemin Master BOQ referansıyla olan uzlaştırma (reconciliation) bağlantısı[cite: 3]
	ReconciliationRecord *ReconciliationRecord `gorm:"foreignKey:QuotationItemID"`
}
