package models

import "gorm.io/gorm"

// MasterBOQItem, temel metraj cetvelindeki (baseline BOQ) referans kalemleri temsil eder[cite: 3].
type MasterBOQItem struct {
	gorm.Model
	ProcurementProjectID uint   // ProcurementProject tablosu ile 1-N ilişki için Foreign Key
	ItemCode             string `gorm:"type:varchar(50);not null"`
	Description          string `gorm:"type:text;not null"`
	UnitOfMeasure        string `gorm:"type:varchar(20);not null"`
	// Deterministik matematiksel işlemlerin hatasız yürütülebilmesi için sayısal tipler kullanılır[cite: 2, 3].
	EstimatedQuantity  float64 `gorm:"type:decimal(15,2);not null"`
	EstimatedUnitPrice float64 `gorm:"type:decimal(15,2);not null"`
}
