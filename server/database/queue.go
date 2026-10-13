package database

import (
	"time"

	"gorm.io/gorm"
)

// QueuedMessage is one prompt on its way to its Station's session (see
// station/queue.go for how and when it's sent).
type QueuedMessage struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	StationID uint      `gorm:"not null" json:"station_id"`
	Text      string    `gorm:"not null" json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func EnqueueMessage(db *gorm.DB, stationID uint, text string) (QueuedMessage, error) {
	m := QueuedMessage{StationID: stationID, Text: text}
	err := db.Create(&m).Error
	return m, err
}

// ListQueue returns stationID's queued messages, oldest first.
func ListQueue(db *gorm.DB, stationID uint) ([]QueuedMessage, error) {
	msgs := []QueuedMessage{}
	err := db.Where("station_id = ?", stationID).Order("id").Find(&msgs).Error
	return msgs, err
}

// DeleteQueued removes one queued message, reporting whether it was still
// there (it may already have been sent).
func DeleteQueued(db *gorm.DB, stationID, id uint) (bool, error) {
	res := db.Where("station_id = ? AND id = ?", stationID, id).Delete(&QueuedMessage{})
	return res.RowsAffected > 0, res.Error
}

func ClearQueue(db *gorm.DB, stationID uint) error {
	return db.Where("station_id = ?", stationID).Delete(&QueuedMessage{}).Error
}

// TakeQueue removes and returns all of stationID's queued messages in one
// transaction, so a message removed concurrently is either taken here or
// gone, never both.
func TakeQueue(db *gorm.DB, stationID uint) ([]QueuedMessage, error) {
	var msgs []QueuedMessage
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("station_id = ?", stationID).Order("id").Find(&msgs).Error; err != nil {
			return err
		}
		if len(msgs) == 0 {
			return nil
		}
		ids := make([]uint, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		return tx.Delete(&QueuedMessage{}, ids).Error
	})
	return msgs, err
}

// RestoreQueue puts messages taken by TakeQueue back under their original
// ids — ahead of anything queued since, as ids only grow.
func RestoreQueue(db *gorm.DB, msgs []QueuedMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	return db.Create(&msgs).Error
}

// StationsWithQueue lists the stations that have anything queued — used to
// resume sending after a server restart.
func StationsWithQueue(db *gorm.DB) ([]uint, error) {
	var ids []uint
	err := db.Model(&QueuedMessage{}).Distinct("station_id").Pluck("station_id", &ids).Error
	return ids, err
}
