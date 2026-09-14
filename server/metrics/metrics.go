// Package metrics implements the Super Badger Station Standard API poller:
// an arbitrary set of external HTTP sources, each returning a JSON object of
// the shape {"<station-id-or-name>": {"<key>": <number>, ...}, ...}. Values
// are stored per (station, key) as the latest known reading (see
// database.StationDataPoint), and an optional threshold per (station, key)
// can trigger a notification when the value crosses it (see
// database.DataPointSetting and Notifier).
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"

	"main/database"
)

// Snapshot is one source's poll result for one station: arbitrary numeric
// data points keyed by name, plus the raw per-station JSON for fallback
// display of anything non-numeric.
type Snapshot struct {
	Values  map[string]float64
	RawJSON string
}

// Notifier is told about a data point crossing a configured threshold, so
// callers (notify.Hub, wired in from main) can push it to clients without
// this package depending on the API/WS layer.
type Notifier interface {
	NotifyThreshold(station database.Station, key string, value float64, direction database.ThresholdDirection)
}

// NotifierFunc adapts a plain function to Notifier.
type NotifierFunc func(station database.Station, key string, value float64, direction database.ThresholdDirection)

func (f NotifierFunc) NotifyThreshold(station database.Station, key string, value float64, direction database.ThresholdDirection) {
	f(station, key, value, direction)
}

// HTTPSource polls one Super Badger Station Standard API endpoint.
type HTTPSource struct {
	Name   string
	URL    string
	APIKey string
	c      *http.Client
}

func NewHTTPSource(name, url, apiKey string) *HTTPSource {
	return &HTTPSource{Name: name, URL: url, APIKey: apiKey, c: &http.Client{Timeout: 10 * time.Second}}
}

func (h *HTTPSource) Poll(ctx context.Context) (map[string]Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", h.URL, nil)
	if err != nil {
		return nil, err
	}
	if h.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.APIKey)
	}
	resp, err := h.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metric source %q: HTTP %d", h.Name, resp.StatusCode)
	}

	var raw map[string]map[string]json.Number
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	out := make(map[string]Snapshot, len(raw))
	for station, fields := range raw {
		values := make(map[string]float64, len(fields))
		for k, n := range fields {
			if f, err := strconv.ParseFloat(n.String(), 64); err == nil {
				values[k] = f
			}
		}
		b, _ := json.Marshal(fields)
		out[station] = Snapshot{Values: values, RawJSON: string(b)}
	}
	return out, nil
}

// Manager supervises one goroutine per enabled MetricSource, so adding,
// editing or removing a source via the API takes effect immediately without
// a server restart. Reload re-reads metric_sources from the DB and
// starts/stops goroutines to match.
type Manager struct {
	db       *gorm.DB
	notifier Notifier

	mu      sync.Mutex
	cancels map[uint]context.CancelFunc
}

func NewManager(db *gorm.DB, notifier Notifier) *Manager {
	return &Manager{db: db, notifier: notifier, cancels: map[uint]context.CancelFunc{}}
}

// Start loads the current set of enabled sources and begins polling them,
// plus the shared history-pruning loop (independent of any one source).
func (m *Manager) Start(ctx context.Context) {
	m.Reload(ctx)
	go pruneLoop(ctx, m.db)
}

// Reload re-reads metric_sources and reconciles running pollers against it —
// call after any create/update/delete on MetricSource.
func (m *Manager) Reload(ctx context.Context) {
	sources, err := database.ListMetricSources(m.db)
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wanted := make(map[uint]database.MetricSource, len(sources))
	for _, s := range sources {
		if s.Enabled {
			wanted[s.ID] = s
		}
	}

	for id, cancel := range m.cancels {
		if _, ok := wanted[id]; !ok {
			cancel()
			delete(m.cancels, id)
		}
	}

	for id, s := range wanted {
		if _, running := m.cancels[id]; running {
			// Restart on every reload rather than diffing field-by-field — a
			// config change (URL/key/interval) should take effect right
			// away, and re-subscribing a ticker is cheap.
			m.cancels[id]()
		}
		sctx, cancel := context.WithCancel(ctx)
		m.cancels[id] = cancel
		go RunSource(sctx, m.db, s, m.notifier)
	}
}

// RunSource polls one MetricSource on its own ticker until ctx is done,
// upserting every reported data point and evaluating thresholds.
func RunSource(ctx context.Context, db *gorm.DB, source database.MetricSource, notifier Notifier) {
	apiKey := ""
	if source.APIKey != nil {
		apiKey = *source.APIKey
	}
	src := NewHTTPSource(source.Name, source.URL, apiKey)

	interval := time.Duration(source.PollIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll(ctx, db, source.ID, src, notifier)
		}
	}
}

func poll(ctx context.Context, db *gorm.DB, sourceID uint, src *HTTPSource, notifier Notifier) {
	snapshots, err := src.Poll(ctx)
	if err != nil || len(snapshots) == 0 {
		return
	}
	stations, err := database.ListStations(db)
	if err != nil {
		return
	}
	byID := make(map[string]database.Station, len(stations))
	byName := make(map[string]database.Station, len(stations))
	for _, st := range stations {
		byID[strconv.FormatUint(uint64(st.ID), 10)] = st
		byName[st.Name] = st
	}

	id := sourceID
	for key, snap := range snapshots {
		st, ok := byID[key]
		if !ok {
			st, ok = byName[key]
		}
		if !ok {
			continue
		}
		applySnapshot(db, st, id, snap, notifier)
	}
}

func applySnapshot(db *gorm.DB, st database.Station, sourceID uint, snap Snapshot, notifier Notifier) {
	now := time.Now()
	for key, value := range snap.Values {
		_ = database.UpsertStationDataPoint(db, &database.StationDataPoint{
			StationID: st.ID,
			SourceID:  &sourceID,
			Key:       key,
			Value:     value,
			RawJSON:   snap.RawJSON,
			UpdatedAt: now,
		})
		// Append-only, alongside the latest-value upsert above — this is what
		// backs the app's graphs (see server/api/history.go). Never fails the
		// poll on an insert error; a dropped sample just leaves a gap.
		_ = database.InsertDataPointHistory(db, &database.StationDataPointHistory{
			StationID:  st.ID,
			Key:        key,
			Value:      value,
			RecordedAt: now.Unix(),
		})
		checkThreshold(db, st, key, value, notifier)
	}
}

// historyRetention bounds station_data_point_history's growth — SQLite has
// no TimescaleDB-style drop_chunks() retention policy, so a fixed cutoff is
// applied on a timer instead (see pruneLoop). 90 days keeps a "3 months"
// graph range fully populated while capping worst-case table size at a
// modest number of rows even for a data point polled every few seconds.
const historyRetention = 90 * 24 * time.Hour

// pruneLoop deletes history older than historyRetention once on start and
// then every 6h — infrequent because the operation is a single indexed range
// delete, not something that needs to track individual writes.
func pruneLoop(ctx context.Context, db *gorm.DB) {
	prune := func() {
		_ = database.PruneDataPointHistory(db, time.Now().Add(-historyRetention).Unix())
	}
	prune()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

// checkThreshold notifies once per crossing: it compares against
// LastNotifiedValue's tripped/untripped state rather than firing on every
// poll while the value stays past the threshold.
func checkThreshold(db *gorm.DB, st database.Station, key string, value float64, notifier Notifier) {
	setting, err := database.GetDataPointSetting(db, st.ID, key)
	if err != nil || setting == nil || !setting.ThresholdEnabled {
		return
	}

	tripped := false
	switch setting.ThresholdDirection {
	case database.ThresholdAbove:
		tripped = value > setting.ThresholdValue
	case database.ThresholdBelow:
		tripped = value < setting.ThresholdValue
	}

	wasTripped := setting.LastNotifiedValue != nil
	if tripped == wasTripped {
		return
	}

	if tripped {
		v := value
		_ = database.UpdateLastNotifiedValue(db, setting.ID, &v)
		if notifier != nil {
			notifier.NotifyThreshold(st, key, value, setting.ThresholdDirection)
		}
	} else {
		_ = database.UpdateLastNotifiedValue(db, setting.ID, nil)
	}
}
