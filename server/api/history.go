// handleDataPointHistory serves downsampled graph data for one station data
// point, mirroring ../stealth-operation's handleGetFieldHistory: a fixed
// range enum (never an arbitrary duration, so a request can't force an
// unbounded scan or a badly-sized bucket) and a bucket width computed from
// the actual data span present, capped to a fixed point budget — the same
// approach, just against SQLite's `recorded_at / bucketSeconds` integer
// bucketing (see database.QueryDataPointHistoryBuckets) instead of
// TimescaleDB's time_bucket().
package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
)

// targetBuckets caps how many points a single history response returns,
// regardless of range — the client graphs a fixed-size series either way, so
// there's no benefit to more resolution than the chart can show.
const targetBuckets = 400

var historyRanges = map[string]time.Duration{
	"1h":  time.Hour,
	"3h":  3 * time.Hour,
	"12h": 12 * time.Hour,
	"1d":  24 * time.Hour,
	"2d":  48 * time.Hour,
	"1w":  7 * 24 * time.Hour,
	"1m":  30 * 24 * time.Hour,
	"3m":  90 * 24 * time.Hour,
	"1y":  365 * 24 * time.Hour,
	"max": 0,
}

func stationDataPointHistory(db *gorm.DB) gin.HandlerFunc {
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

		rangeParam := c.DefaultQuery("range", "1d")
		dur, ok := historyRanges[rangeParam]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "range must be one of 1h,3h,12h,1d,2d,1w,1m,3m,1y,max"})
			return
		}

		var since *int64
		if dur > 0 {
			s := time.Now().Add(-dur).Unix()
			since = &s
		}

		oldest, found, err := database.OldestDataPointHistory(db, id, key, since)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !found {
			c.JSON(http.StatusOK, []database.HistoryBucket{})
			return
		}

		// Bucket width sized to the data actually present in the window
		// (now - oldest), not the nominal range length — picking "1y" with
		// only a day of real data buckets at ~4-minute granularity instead
		// of forcing everything into a couple of year-wide buckets.
		span := time.Since(time.Unix(oldest, 0))
		bucketSeconds := int64(span/targetBuckets/time.Second) + 1

		buckets, err := database.QueryDataPointHistoryBuckets(db, id, key, since, bucketSeconds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, buckets)
	}
}
