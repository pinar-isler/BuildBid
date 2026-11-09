package models

import (
	"gorm.io/gorm"
)

// ProcurementProject, ihale çalışma alanını (tender workspace) temsil eder ve
// projeye ait temel meta verileri barındırır[cite: 3].
type ProcurementProject struct {
	gorm.Model
	ProjectID  string `gorm:"type:varchar(100);uniqueIndex;not null"`
	Title      string `gorm:"type:varchar(255);not null"`
	ClientName string `gorm:"type:varchar(255);not null"`
	Location   string `gorm:"type:varchar(255)"`
	Currency   string `gorm:"type:varchar(10);not null"`
	Status     string `gorm:"type:varchar(50);default:'Draft'"`

	// Bire-Çok (1-to-N) ve Bire-Bir (1-to-1) İlişkiler[cite: 3]
	MasterBOQItems   []MasterBOQItem   `gorm:"foreignKey:ProcurementProjectID"`
	VendorQuotations []VendorQuotation `gorm:"foreignKey:ProcurementProjectID"`
	TitleDeedRecord  *TitleDeedRecord  `gorm:"foreignKey:ProcurementProjectID"`
}
