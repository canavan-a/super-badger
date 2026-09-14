package database

import (
	"time"

	"gorm.io/gorm"
)

// StationStatus tracks whether a Station has a live opencode session behind it.
type StationStatus string

const (
	StatusIdle   StationStatus = "idle"
	StatusActive StationStatus = "active"
	StatusError  StationStatus = "error"
)

// Station groups one opencode agent + one provider/model already declared in
// opencode's own config (confirmed live that opencode does not reliably
// support dynamically-registered providers at prompt-time — see git history
// / migration 00003) + at most one live opencode session. Directory is the
// cwd opencode runs the session's agent in.
type Station struct {
	ID                uint          `gorm:"primaryKey" json:"id"`
	Name              string        `gorm:"uniqueIndex;not null" json:"name"`
	ProviderID        string        `gorm:"not null" json:"provider_id"`
	ModelID           string        `gorm:"not null" json:"model_id"`
	Directory         string        `json:"directory"`
	Agent             string        `gorm:"not null" json:"agent"`
	OpencodeSessionID string        `json:"opencode_session_id"`
	Status            StationStatus `gorm:"not null;default:idle" json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// MetricSource is one external "Super Badger Station Standard API" endpoint
// the server polls on its own schedule (see server/metrics for the spec and
// the poller). Multiple sources can each cover a different subset of
// Stations (e.g. one per physical box).
type MetricSource struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	Name                string    `gorm:"not null" json:"name"`
	URL                 string    `gorm:"not null" json:"url"`
	APIKey              *string   `json:"api_key,omitempty"`
	PollIntervalSeconds int       `gorm:"not null;default:30" json:"poll_interval_seconds"`
	Enabled             bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// StationDataPoint is the latest known value of one arbitrarily-named metric
// for a Station (e.g. "gpu_temp_c", "tokens_per_sec"), as reported by a
// MetricSource. One row per (station, key) — each poll upserts in place, so
// UpdatedAt is the point's recency.
type StationDataPoint struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	StationID uint      `gorm:"uniqueIndex:idx_station_data_points_station_key;not null" json:"station_id"`
	SourceID  *uint     `json:"source_id,omitempty"`
	Key       string    `gorm:"uniqueIndex:idx_station_data_points_station_key;not null" json:"key"`
	Value     float64   `gorm:"not null" json:"value"`
	RawJSON   string    `json:"raw_json,omitempty"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

type ThresholdDirection string

const (
	ThresholdAbove ThresholdDirection = "above"
	ThresholdBelow ThresholdDirection = "below"
)

// DataPointSetting is a Station owner's preferences for one data point key:
// whether to surface it on the top bar, and an optional threshold that
// triggers a push notification when the value crosses it (LastNotifiedValue
// avoids re-notifying on every poll while a threshold stays tripped — see
// server/metrics.RunPoller).
type DataPointSetting struct {
	ID                 uint               `gorm:"primaryKey" json:"id"`
	StationID          uint               `gorm:"uniqueIndex:idx_data_point_settings_station_key;not null" json:"station_id"`
	Key                string             `gorm:"uniqueIndex:idx_data_point_settings_station_key;not null" json:"key"`
	ShowOnTopBar       bool               `gorm:"not null;default:false" json:"show_on_top_bar"`
	ThresholdEnabled   bool               `gorm:"not null;default:false" json:"threshold_enabled"`
	ThresholdValue     float64            `gorm:"not null;default:0" json:"threshold_value"`
	ThresholdDirection ThresholdDirection `gorm:"not null;default:above" json:"threshold_direction"`
	LastNotifiedValue  *float64           `json:"-"`
}

func CreateStation(db *gorm.DB, s *Station) error {
	return db.Create(s).Error
}

func ListStations(db *gorm.DB) ([]Station, error) {
	var stations []Station
	err := db.Order("name").Find(&stations).Error
	return stations, err
}

func GetStation(db *gorm.DB, id uint) (Station, error) {
	var s Station
	err := db.First(&s, id).Error
	return s, err
}

// FindActiveStationForBackend finds any Station (other than excludeID) already
// holding a live session against the same provider/model pair, so callers can
// tear it down before creating a new one — the local model backing a
// provider/model has no session concurrency of its own.
func FindActiveStationForBackend(db *gorm.DB, providerID, modelID string, excludeID uint) (*Station, error) {
	var s Station
	err := db.Where("provider_id = ? AND model_id = ? AND id != ? AND opencode_session_id != ''", providerID, modelID, excludeID).
		First(&s).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func UpdateStationSession(db *gorm.DB, id uint, sessionID string, status StationStatus) error {
	return db.Model(&Station{}).Where("id = ?", id).Updates(map[string]any{
		"opencode_session_id": sessionID,
		"status":              status,
	}).Error
}

func DeleteStation(db *gorm.DB, id uint) error {
	return db.Delete(&Station{}, id).Error
}

func ListMetricSources(db *gorm.DB) ([]MetricSource, error) {
	var sources []MetricSource
	err := db.Order("name").Find(&sources).Error
	return sources, err
}

func CreateMetricSource(db *gorm.DB, s *MetricSource) error {
	return db.Create(s).Error
}

func UpdateMetricSource(db *gorm.DB, id uint, updates map[string]any) error {
	return db.Model(&MetricSource{}).Where("id = ?", id).Updates(updates).Error
}

func DeleteMetricSource(db *gorm.DB, id uint) error {
	return db.Delete(&MetricSource{}, id).Error
}

// UpsertStationDataPoint writes the latest value for one (station, key) pair,
// replacing whatever was there — see StationDataPoint's uniqueIndex.
func UpsertStationDataPoint(db *gorm.DB, p *StationDataPoint) error {
	var existing StationDataPoint
	err := db.Where("station_id = ? AND key = ?", p.StationID, p.Key).First(&existing).Error
	if err == nil {
		p.ID = existing.ID
		return db.Save(p).Error
	}
	if err == gorm.ErrRecordNotFound {
		return db.Create(p).Error
	}
	return err
}

func ListStationDataPoints(db *gorm.DB, stationID uint) ([]StationDataPoint, error) {
	var points []StationDataPoint
	err := db.Where("station_id = ?", stationID).Order("key").Find(&points).Error
	return points, err
}

func GetDataPointSetting(db *gorm.DB, stationID uint, key string) (*DataPointSetting, error) {
	var s DataPointSetting
	err := db.Where("station_id = ? AND key = ?", stationID, key).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func ListDataPointSettings(db *gorm.DB, stationID uint) ([]DataPointSetting, error) {
	var settings []DataPointSetting
	err := db.Where("station_id = ?", stationID).Find(&settings).Error
	return settings, err
}

// UpsertDataPointSetting creates a setting row on first write (e.g. the first
// time a client toggles "show on top bar" for a key that has no row yet).
func UpsertDataPointSetting(db *gorm.DB, s *DataPointSetting) error {
	var existing DataPointSetting
	err := db.Where("station_id = ? AND key = ?", s.StationID, s.Key).First(&existing).Error
	if err == nil {
		s.ID = existing.ID
		return db.Save(s).Error
	}
	if err == gorm.ErrRecordNotFound {
		return db.Create(s).Error
	}
	return err
}

// UpdateLastNotifiedValue records the value that most recently tripped (or
// cleared) a threshold, so RunPoller only notifies once per crossing.
func UpdateLastNotifiedValue(db *gorm.DB, settingID uint, value *float64) error {
	return db.Model(&DataPointSetting{}).Where("id = ?", settingID).Update("last_notified_value", value).Error
}
