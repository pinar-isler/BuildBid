package models

import "gorm.io/gorm"

// QuotationItem, tedarikçi teklifinden (VendorQuotation) çıkarılan ham satır kalemlerini temsil eder.
type QuotationItem struct {
	gorm.Model
	VendorQuotationID uint    `gorm:"not null;index:idx_quote_item"`
	QuoteItemID       string  `gorm:"type:varchar(100);not null;uniqueIndex:idx_quote_item"`
	RawDescription    string  `gorm:"type:text;not null"`
	Quantity          float64 `gorm:"type:decimal(15,2);not null"`
	UnitOfMeasure     string  `gorm:"type:varchar(20);not null"`
	QuotedUnitPrice   float64 `gorm:"type:decimal(15,2);not null"`
	IsTaxIncluded     bool    `gorm:"default:false"`

	// İlişkiler
	VendorQuotation      VendorQuotation       `gorm:"foreignKey:VendorQuotationID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	ReconciliationRecord *ReconciliationRecord `gorm:"foreignKey:QuotationItemID"`
}
