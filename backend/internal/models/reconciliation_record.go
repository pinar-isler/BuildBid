package models

import "gorm.io/gorm"

// ReconciliationRecord, bir satıcı teklif kalemi (QuotationItem) ile proje ana metraj kalemi (MasterBOQItem) arasındaki eşleşmeyi temsil eder[cite: 3].
type ReconciliationRecord struct {
	gorm.Model
	QuotationItemID uint // Foreign key (QuotationItem tablosu ile 1-to-1 ilişki)
	MasterBOQItemID uint // Foreign key (MasterBOQItem tablosuna referans)

	ReconciliationID string  `gorm:"type:varchar(100);uniqueIndex;not null"`
	ConfidenceScore  float64 `gorm:"type:decimal(5,2);not null"` // Hibrit güven skoru (örn: 78.50)[cite: 3]
	MatchStatus      string  `gorm:"type:varchar(50);not null"`  // Durumlar: AutoMatched, PendingReview, HumanApproved, Discarded[cite: 3]
	IsHumanVerified  bool    `gorm:"default:false"`              // İnsan döngüsü (HITL) manuel onay durumu[cite: 3]
}
