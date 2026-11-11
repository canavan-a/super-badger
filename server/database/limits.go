package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelLimit overrides the context window / output cap opencode uses for one
// provider/model. Nil fields keep the value the opencode plugin detects from
// the engine.
type ModelLimit struct {
	ProviderID string    `gorm:"primaryKey" json:"provider_id"`
	ModelID    string    `gorm:"primaryKey" json:"model_id"`
	Context    *int      `json:"context"`
	Output     *int      `json:"output"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func ListModelLimits(db *gorm.DB) ([]ModelLimit, error) {
	var out []ModelLimit
	err := db.Order("provider_id, model_id").Find(&out).Error
	return out, err
}

// SetModelLimit stores an override; with both fields nil it removes it.
func SetModelLimit(db *gorm.DB, l ModelLimit) error {
	if l.Context == nil && l.Output == nil {
		return db.Where("provider_id = ? AND model_id = ?", l.ProviderID, l.ModelID).Delete(&ModelLimit{}).Error
	}
	l.UpdatedAt = time.Now()
	return db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&l).Error
}
