package models

import "gorm.io/gorm"

// MasterBOQItem, temel metraj cetvelindeki (baseline BOQ) referans kalemleri temsil eder.
type MasterBOQItem struct {
	gorm.Model
	ProcurementProjectID uint    `gorm:"not null;index;uniqueIndex:idx_project_item_code"`
	ItemCode             string  `gorm:"type:varchar(50);not null;uniqueIndex:idx_project_item_code"`
	Description          string  `gorm:"type:text;not null"`
	UnitOfMeasure        string  `gorm:"type:varchar(20);not null"`
	EstimatedQuantity    float64 `gorm:"type:decimal(15,2);not null"`
	EstimatedUnitPrice   float64 `gorm:"type:decimal(15,2);not null"`

	// İlişki (Belongs To)
	ProcurementProject ProcurementProject `gorm:"foreignKey:ProcurementProjectID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}
