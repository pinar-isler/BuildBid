package models

import "gorm.io/gorm"

// ReconciliationRecord, bir satıcı teklif kalemi (QuotationItem) ile proje ana metraj kalemi (MasterBOQItem) arasındaki eşleşmeyi temsil eder.
type ReconciliationRecord struct {
	gorm.Model
	QuotationItemID uint `gorm:"not null;uniqueIndex"`
	MasterBOQItemID uint `gorm:"not null;index"`

	ReconciliationID string  `gorm:"type:varchar(100);uniqueIndex;not null"`
	ConfidenceScore  float64 `gorm:"type:decimal(5,2);not null"`
	MatchStatus      string  `gorm:"type:varchar(50);not null"`
	IsHumanVerified  bool    `gorm:"default:false"`

	// İlişkiler
	QuotationItem QuotationItem `gorm:"foreignKey:QuotationItemID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	MasterBOQItem MasterBOQItem `gorm:"foreignKey:MasterBOQItemID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT" json:"-"`
}
