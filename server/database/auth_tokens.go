package database

import (
	"time"

	"gorm.io/gorm"
)

// AuthToken is one API credential minted by `badger token generate` (see
// cmd/badger). Only TokenHash (sha256, hex) is stored — see the migration's
// comment for why. Once at least one row exists here, api.RequireAuth starts
// enforcing Bearer auth on every request; with none, the API stays open
// (matches the zero-config local-dev behavior superbadger had before auth
// existed at all).
type AuthToken struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Label      string     `gorm:"not null;default:''" json:"label"`
	TokenHash  string     `gorm:"uniqueIndex;not null" json:"-"`
	Last4      string     `gorm:"not null" json:"last4"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func CreateAuthToken(db *gorm.DB, t *AuthToken) error {
	return db.Create(t).Error
}

func ListAuthTokens(db *gorm.DB) ([]AuthToken, error) {
	var tokens []AuthToken
	err := db.Order("created_at").Find(&tokens).Error
	return tokens, err
}

func CountAuthTokens(db *gorm.DB) (int64, error) {
	var n int64
	err := db.Model(&AuthToken{}).Count(&n).Error
	return n, err
}

// GetAuthTokenByHash looks up a token by its hash for request auth (see
// api.RequireAuth) — nil, nil when no row matches (not a real error, just
// "invalid token").
func GetAuthTokenByHash(db *gorm.DB, hash string) (*AuthToken, error) {
	var t AuthToken
	err := db.Where("token_hash = ?", hash).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TouchAuthToken records that t was just used to authenticate a request —
// best-effort, so a failure here never fails the request it's authenticating.
func TouchAuthToken(db *gorm.DB, id uint) error {
	return db.Model(&AuthToken{}).Where("id = ?", id).Update("last_used_at", time.Now()).Error
}

func DeleteAuthToken(db *gorm.DB, id uint) error {
	return db.Delete(&AuthToken{}, id).Error
}
