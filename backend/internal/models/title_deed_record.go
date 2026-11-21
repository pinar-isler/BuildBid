package models

import "gorm.io/gorm"

// TitleDeedRecord, bir projeye veya taşınmaza ait tapu sicil bilgilerini temsil eder.
type TitleDeedRecord struct {
	gorm.Model
	ProcurementProjectID uint `gorm:"not null;uniqueIndex"`

	TitleDeedID  string  `gorm:"type:varchar(100);uniqueIndex;not null"`
	Province     string  `gorm:"type:varchar(100);not null"`
	District     string  `gorm:"type:varchar(100);not null"`
	Neighborhood string  `gorm:"type:varchar(150)"`
	BlockNo      string  `gorm:"type:varchar(50);not null"` // Ada
	ParcelNo     string  `gorm:"type:varchar(50);not null"` // Parsel
	SheetNo      string  `gorm:"type:varchar(50)"`          // Pafta
	TotalAreaM2  float64 `gorm:"type:decimal(15,2);not null"`
	LandUseType  string  `gorm:"type:varchar(100)"`

	// İlişkiler
	ProcurementProject  ProcurementProject  `gorm:"foreignKey:ProcurementProjectID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
	ShareholderEquities []ShareholderEquity `gorm:"foreignKey:TitleDeedRecordID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}
