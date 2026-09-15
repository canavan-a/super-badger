// Handlers for the Super Badger Station Standard API: per-station data
// points (read-only, populated by server/metrics's poller) and their display
// / threshold-notification settings, plus CRUD for the MetricSource configs
// that tell the poller what to poll and how often (see server/metrics.Manager).
package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
	"main/metrics"
)

// dataPointOut merges a StationDataPoint's current value/recency with its
// DataPointSetting (defaulted if the owner never touched it), so the app can
// render the whole per-station settings tab from one list.
type dataPointOut struct {
	Key                string                      `json:"key"`
	Label              string                      `json:"label"`
	Value              float64                     `json:"value"`
	Decimals           int                         `json:"decimals"`
	UpdatedAt          string                      `json:"updated_at"`
	ShowOnTopBar       bool                        `json:"show_on_top_bar"`
	ThresholdEnabled   bool                        `json:"threshold_enabled"`
	ThresholdValue     float64                     `json:"threshold_value"`
	ThresholdDirection database.ThresholdDirection `json:"threshold_direction"`
}

func listStationDataPoints(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		points, err := database.ListStationDataPoints(db, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		settings, err := database.ListDataPointSettings(db, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		byKey := make(map[string]database.DataPointSetting, len(settings))
		for _, s := range settings {
			byKey[s.Key] = s
		}

		out := make([]dataPointOut, len(points))
		for i, p := range points {
			s, hasSetting := byKey[p.Key]
			if s.ThresholdDirection == "" {
				s.ThresholdDirection = database.ThresholdAbove
			}
			if !hasSetting {
				s.Decimals = 1
			}
			out[i] = dataPointOut{
				Key:                p.Key,
				Label:              s.Label,
				Value:              p.Value,
				Decimals:           s.Decimals,
				UpdatedAt:          p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				ShowOnTopBar:       s.ShowOnTopBar,
				ThresholdEnabled:   s.ThresholdEnabled,
				ThresholdValue:     s.ThresholdValue,
				ThresholdDirection: s.ThresholdDirection,
			}
		}
		c.JSON(http.StatusOK, out)
	}
}

func updateDataPointSettings(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		key := c.Param("key")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing data point key"})
			return
		}

		var body struct {
			Label              string                      `json:"label"`
			Decimals           int                         `json:"decimals"`
			ShowOnTopBar       bool                        `json:"show_on_top_bar"`
			ThresholdEnabled   bool                        `json:"threshold_enabled"`
			ThresholdValue     float64                     `json:"threshold_value"`
			ThresholdDirection database.ThresholdDirection `json:"threshold_direction"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if body.ThresholdDirection == "" {
			body.ThresholdDirection = database.ThresholdAbove
		}
		if body.ThresholdDirection != database.ThresholdAbove && body.ThresholdDirection != database.ThresholdBelow {
			c.JSON(http.StatusBadRequest, gin.H{"error": "threshold_direction must be 'above' or 'below'"})
			return
		}
		if body.Decimals < 0 || body.Decimals > 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "decimals must be between 0 and 6"})
			return
		}

		err := database.UpsertDataPointSetting(db, &database.DataPointSetting{
			StationID:          id,
			Key:                key,
			Label:              body.Label,
			Decimals:           body.Decimals,
			ShowOnTopBar:       body.ShowOnTopBar,
			ThresholdEnabled:   body.ThresholdEnabled,
			ThresholdValue:     body.ThresholdValue,
			ThresholdDirection: body.ThresholdDirection,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func listMetricSources(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sources, err := database.ListMetricSources(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, sources)
	}
}

type metricSourceBody struct {
	Name                string  `json:"name" binding:"required"`
	URL                 string  `json:"url" binding:"required"`
	APIKey              *string `json:"api_key"`
	PollIntervalSeconds int     `json:"poll_interval_seconds"`
	Enabled             *bool   `json:"enabled"`
}

func createMetricSource(db *gorm.DB, mm *metrics.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body metricSourceBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		interval := body.PollIntervalSeconds
		if interval <= 0 {
			interval = 30
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		src := &database.MetricSource{
			Name:                body.Name,
			URL:                 body.URL,
			APIKey:              body.APIKey,
			PollIntervalSeconds: interval,
			Enabled:             enabled,
		}
		if err := database.CreateMetricSource(db, src); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		mm.Reload(context.Background())
		c.JSON(http.StatusCreated, src)
	}
}

func updateMetricSource(db *gorm.DB, mm *metrics.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var body metricSourceBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		interval := body.PollIntervalSeconds
		if interval <= 0 {
			interval = 30
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		updates := map[string]any{
			"name":                  body.Name,
			"url":                   body.URL,
			"api_key":               body.APIKey,
			"poll_interval_seconds": interval,
			"enabled":               enabled,
		}
		if err := database.UpdateMetricSource(db, id, updates); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		mm.Reload(context.Background())
		c.Status(http.StatusNoContent)
	}
}

func deleteMetricSource(db *gorm.DB, mm *metrics.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		if err := database.DeleteMetricSource(db, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		mm.Reload(context.Background())
		c.Status(http.StatusNoContent)
	}
}
