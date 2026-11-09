package models

import "gorm.io/gorm"

// ShareholderEquity, bir tapu parselindeki (TitleDeedRecord) bireysel paydaş mülkiyet oranlarını temsil eder.
type ShareholderEquity struct {
	gorm.Model
	TitleDeedRecordID uint    // Foreign key (TitleDeedRecord tablosu ile 1-to-N ilişki)
	ShareholderID     string  `gorm:"type:varchar(100);not null"`
	FullName          string  `gorm:"type:varchar(255);not null"`
	ShareFraction     string  `gorm:"type:varchar(50);not null"`   // Kesirli pay oranı, örn: "3/8" veya "1/4"
	ComputedAreaM2    float64 `gorm:"type:decimal(15,2);not null"` // Determinizm ilkesi gereği matematiksel tipe (Double) çevrilir[cite: 3]
}
