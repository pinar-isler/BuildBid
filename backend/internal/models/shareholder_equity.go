package models

import "gorm.io/gorm"

// ShareholderEquity, bir tapu parselindeki (TitleDeedRecord) bireysel paydaş mülkiyet oranlarını temsil eder.
type ShareholderEquity struct {
	gorm.Model
	TitleDeedRecordID uint    `gorm:"not null;index:idx_deed_shareholder"`
	ShareholderID     string  `gorm:"type:varchar(100);not null;uniqueIndex:idx_deed_shareholder"`
	FullName          string  `gorm:"type:varchar(255);not null"`
	ShareFraction     string  `gorm:"type:varchar(50);not null"`
	ComputedAreaM2    float64 `gorm:"type:decimal(15,2);not null"`

	// İlişki (Belongs To)
	TitleDeedRecord TitleDeedRecord `gorm:"foreignKey:TitleDeedRecordID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}
