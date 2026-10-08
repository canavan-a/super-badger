// Package metrics implements the Super Badger Station Standard API poller:
// an arbitrary set of external HTTP sources, each returning a JSON object of
// the shape {"<station-id-or-name>": {"<key>": <number>, ...}, ...}. Values
// are stored per (station, key) as the latest known reading (see
// database.StationDataPoint), and an optional threshold per (station, key)
// can trigger a notification when the value crosses it (see
// database.DataPointSetting and Notifier).
//
// Each source may also serve a sibling commands endpoint (see
// docs/command-spec.md and PollCommands) listing actions that belong to the
// source as a whole rather than to any station.
package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	NotifyThreshold(station database.Station, key, label string, decimals int, value float64, direction database.ThresholdDirection)
}

// NotifierFunc adapts a plain function to Notifier.
type NotifierFunc func(station database.Station, key, label string, decimals int, value float64, direction database.ThresholdDirection)

func (f NotifierFunc) NotifyThreshold(station database.Station, key, label string, decimals int, value float64, direction database.ThresholdDirection) {
	f(station, key, label, decimals, value, direction)
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
			pollCommands(ctx, db, source.ID, src)
		}
	}
}

// errNoCommands means the source has no commands endpoint (HTTP 404) — a
// normal state for a source that only reports metrics, unlike a transport
// failure.
var errNoCommands = errors.New("source has no commands endpoint")

// CommandsURL is the sibling "commands" path next to the metrics URL:
// http://box:9000/metrics → http://box:9000/commands, http://box:9000/ →
// http://box:9000/commands, http://box:9000/api/ → http://box:9000/api/commands.
func (h *HTTPSource) CommandsURL() (*url.URL, error) {
	base, err := url.Parse(h.URL)
	if err != nil {
		return nil, err
	}
	if base.Path == "" {
		base.Path = "/"
	}
	return base.ResolveReference(&url.URL{Path: "commands"}), nil
}

// PollCommands fetches the source's command list. Each command's url may be
// relative; it's resolved against the commands URL so the stored URL is
// always absolute. Entries missing a path or url, or repeating a path, are
// dropped rather than failing the whole list.
func (h *HTTPSource) PollCommands(ctx context.Context) ([]database.Command, error) {
	cu, err := h.CommandsURL()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", cu.String(), nil)
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoCommands
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("commands for %q: HTTP %d", h.Name, resp.StatusCode)
	}

	var raw []struct {
		Path    string                   `json:"path"`
		Label   string                   `json:"label"`
		URL     string                   `json:"url"`
		Group   string                   `json:"group"`
		Active  bool                     `json:"active"`
		Confirm string                   `json:"confirm"`
		Options []database.CommandOption `json:"options"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	out := make([]database.Command, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, r := range raw {
		if r.Path == "" || r.URL == "" || seen[r.Path] {
			continue
		}
		ref, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		seen[r.Path] = true
		out = append(out, database.Command{
			Path:     r.Path,
			Label:    r.Label,
			URL:      cu.ResolveReference(ref).String(),
			Position: len(out),
			Group:    r.Group,
			Active:   r.Active,
			Confirm:  r.Confirm,
			Options:  encodeOptions(r.Options),
		})
	}
	return out, nil
}

// encodeOptions keeps a picker's options that have a value, first occurrence
// winning, and stores them as JSON. nil (a plain command) when none survive
// or none were sent.
func encodeOptions(opts []database.CommandOption) *string {
	kept := make([]database.CommandOption, 0, len(opts))
	seen := make(map[string]bool, len(opts))
	for _, o := range opts {
		if o.Value == "" || seen[o.Value] {
			continue
		}
		seen[o.Value] = true
		kept = append(kept, o)
	}
	if len(kept) == 0 {
		return nil
	}
	b, _ := json.Marshal(kept)
	s := string(b)
	return &s
}

// pollCommands refreshes a source's stored command list. A 404 clears it (the
// source stopped serving commands); any other failure leaves the last known
// list in place, the same way a failed metrics poll leaves stale values.
func pollCommands(ctx context.Context, db *gorm.DB, sourceID uint, src *HTTPSource) {
	cmds, err := src.PollCommands(ctx)
	if errors.Is(err, errNoCommands) {
		cmds, err = nil, nil
	}
	if err != nil {
		return
	}
	now := time.Now()
	for i := range cmds {
		cmds[i].SourceID = sourceID
		cmds[i].UpdatedAt = now
	}
	_ = database.ReplaceSourceCommands(db, sourceID, cmds)
}

// RefreshCommands re-polls one source's command list right away, outside its
// ticker — called after a command runs, since running one usually changes
// what the source advertises (e.g. which mode is active).
func RefreshCommands(ctx context.Context, db *gorm.DB, source database.MetricSource) {
	apiKey := ""
	if source.APIKey != nil {
		apiKey = *source.APIKey
	}
	pollCommands(ctx, db, source.ID, NewHTTPSource(source.Name, source.URL, apiKey))
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
	byAlias := make(map[string]database.Station, len(stations))
	byName := make(map[string]database.Station, len(stations))
	for _, st := range stations {
		byID[strconv.FormatUint(uint64(st.ID), 10)] = st
		if st.Alias != nil && *st.Alias != "" {
			byAlias[*st.Alias] = st
		}
		byName[st.Name] = st
	}

	id := sourceID
	for key, snap := range snapshots {
		st, ok := byID[key]
		if !ok {
			st, ok = byAlias[key]
		}
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
		// A fresh value arriving is exactly what "un-hide a temp-deleted data
		// point" means (see database.HideDataPoint) — done here, at ingestion,
		// as a single no-op-if-not-hidden UPDATE, rather than on every read in
		// listStationDataPoints (which used to read-then-write per hidden
		// point on every single GET request).
		_ = database.UnhideDataPointIfHidden(db, st.ID, key)
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
			notifier.NotifyThreshold(st, key, setting.Label, setting.Decimals, value, setting.ThresholdDirection)
		}
	} else {
		_ = database.UpdateLastNotifiedValue(db, setting.ID, nil)
	}
}
