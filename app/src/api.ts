import {EventMsg} from './chat';
import {settingsStore} from './settings';

export type StationStatus = 'idle' | 'active' | 'error';

// The fixed header buttons/badges that can be opted into the top bar (see
// StationSettingsScreen) — "data" isn't included since that's always shown,
// it's the only way to reach this config. "tokens" is a badge (a
// label:value pill, like a data point) rather than a button.
export type TopBarActionKey = 'compact' | 'reset' | 'delete' | 'tokens';

export interface Station {
  id: number;
  name: string;
  // Owner-settable tag metric sources can target instead of id/name (see
  // server/metrics's poller) — null until set.
  alias: string | null;
  // Hex string from STATION_COLORS (see stationColors.ts), carried into push
  // notifications as the accent color.
  color: string;
  // Owner opt-in list of which fixed header action buttons show on the top
  // bar — empty by default, matching every other top-bar element's "nothing
  // unless you opt in" rule.
  top_bar_actions: TopBarActionKey[];
  provider_id: string;
  model_id: string;
  directory: string;
  agent: string;
  opencode_session_id: string;
  status: StationStatus;
  // Live-checked on every fetch (see server's station.Service.Reachable) —
  // unlike status, this reflects whether the model is answering *right now*,
  // not whether a session was created at some point in the past.
  reachable: boolean;
  created_at: string;
  updated_at: string;
}

export interface OpencodeProviderModel {
  id?: string;
  name?: string;
}

export interface OpencodeProvider {
  id: string;
  name?: string;
  options?: {baseURL?: string; apiKey?: string};
  models: Record<string, OpencodeProviderModel>;
}

export interface OpencodeProvidersResponse {
  providers: OpencodeProvider[];
  default: Record<string, string>;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const {serverUrl, authToken} = settingsStore.get();
  const headers: Record<string, string> = {'Content-Type': 'application/json'};
  // The server has no auth yet — sent ahead of time so the header is already
  // wired up once it does; harmless no-op until then.
  if (authToken) {
    headers.Authorization = `Bearer ${authToken}`;
  }

  const res = await fetch(`${serverUrl}${path}`, {
    headers,
    ...init,
  });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(`${init?.method ?? 'GET'} ${path}: ${res.status} ${body}`);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

export function listStations(): Promise<Station[]> {
  return request<Station[]>('/stations');
}

// Checks superbadger itself, not opencode or any station's model — a plain
// fetch failure here (network error/refused connection) means "the server
// address in Settings is wrong or superbadger isn't running", distinct from
// a station being unreachable (that's a live model, not the server).
export async function checkServerHealth(): Promise<boolean> {
  try {
    await request('/health');
    return true;
  } catch {
    return false;
  }
}

export function getStation(id: number): Promise<Station> {
  return request<Station>(`/stations/${id}`);
}

export function createStation(params: {
  name: string;
  provider_id: string;
  model_id: string;
  directory?: string;
}): Promise<Station> {
  return request<Station>('/stations', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export function deleteStation(id: number): Promise<void> {
  return request<void>(`/stations/${id}`, {method: 'DELETE'});
}

export function updateStation(
  id: number,
  updates: {name?: string; alias?: string | null; color?: string; top_bar_actions?: TopBarActionKey[]},
): Promise<Station> {
  return request<Station>(`/stations/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
}

export function resetStation(id: number): Promise<Station> {
  return request<Station>(`/stations/${id}/reset`, {method: 'POST'});
}

// Triggers opencode's conversation-summarization for a station's session —
// the same thing the CLI's /compact command does. Unlike reset, this keeps
// the session alive; it just folds older turns into a summary so context
// stays under the model's window.
export function compactStation(id: number): Promise<void> {
  return request<void>(`/stations/${id}/compact`, {method: 'POST'});
}

// Cancels a station's session's in-flight turn — the same thing the CLI's
// Escape/Ctrl-C does mid-response, for stopping a long-running or stuck
// reply without losing history the way Reset does.
export function abortStation(id: number): Promise<void> {
  return request<void>(`/stations/${id}/abort`, {method: 'POST'});
}

export interface TokenUsage {
  input: number;
  output: number;
  reasoning: number;
  cache_read: number;
  cache_write: number;
  cost: number;
}

// Same token/cost figures the CLI's status line shows — updated by opencode
// after every completed turn (no dedicated event for it, so this is polled).
export function getStationUsage(id: number): Promise<TokenUsage> {
  return request<TokenUsage>(`/stations/${id}/usage`);
}

export function promptStation(id: number, text: string): Promise<unknown> {
  return request<unknown>(`/stations/${id}/prompt`, {
    method: 'POST',
    body: JSON.stringify({text}),
  });
}

// The history endpoint returns raw opencode events verbatim (see
// server/opencode/events.go's Event) — same shape as what streams over the
// station WebSocket, so both feed chat.ts's applyEvent/seedFromHistory.
export function getStationHistory(id: number): Promise<EventMsg[]> {
  return request<EventMsg[]>(`/stations/${id}/history`);
}

export function listProviders(): Promise<OpencodeProvidersResponse> {
  return request<OpencodeProvidersResponse>('/providers');
}

export function listModels(): Promise<unknown> {
  return request<unknown>('/models');
}

export function testProviderConnection(params: {
  base_url: string;
  api_key?: string;
}): Promise<{ok: boolean; error?: string}> {
  return request<{ok: boolean; error?: string}>('/providers/test', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export interface MullvadOutput {
  output: string;
}

export function mullvadStatus(): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/status');
}

export function mullvadConnect(): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/connect', {method: 'POST'});
}

export function mullvadDisconnect(): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/disconnect', {method: 'POST'});
}

export function mullvadConfigure(accountToken: string): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/configure', {
    method: 'POST',
    body: JSON.stringify({account_token: accountToken}),
  });
}

export function mullvadSetLocation(params: {
  country: string;
  city?: string;
  hostname?: string;
}): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/location', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export function mullvadListRelays(): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/relays');
}

export function mullvadSetLan(allow: boolean): Promise<MullvadOutput> {
  return request<MullvadOutput>('/mullvad/lan', {
    method: 'POST',
    body: JSON.stringify({allow}),
  });
}

// A Station's ad hoc data points (Super Badger Station Standard API), one
// per external metric key (e.g. "gpu_temp_c", "tokens_per_sec"), populated
// by whatever MetricSource(s) the server is configured to poll.
export interface StationDataPoint {
  key: string;
  // Owner-supplied display name (e.g. "GPU Temp" for "gpu_temp_c") — empty
  // until set, in which case callers should fall back to `key`.
  label: string;
  value: number;
  // Decimal places to show when rendering `value` — a display preference
  // only, doesn't affect the value's stored precision.
  decimals: number;
  updated_at: string;
  show_on_top_bar: boolean;
  threshold_enabled: boolean;
  threshold_value: number;
  threshold_direction: 'above' | 'below';
  // Owner-controlled top-bar/list position (lower first) — see the "..."
  // config drawer's drag-to-reorder.
  order: number;
}

export function getStationDataPoints(id: number): Promise<StationDataPoint[]> {
  return request<StationDataPoint[]>(`/stations/${id}/datapoints`);
}

// "Temp deletes" a data point: hidden from the top bar and settings list
// until a fresh value arrives for it.
export function hideStationDataPoint(id: number, key: string): Promise<void> {
  return request<void>(`/stations/${id}/datapoints/${encodeURIComponent(key)}/hide`, {
    method: 'POST',
  });
}

// Persists the owner's drag-to-reorder result from the config drawer.
export function reorderStationDataPoints(id: number, order: string[]): Promise<void> {
  return request<void>(`/stations/${id}/datapoints/reorder`, {
    method: 'PUT',
    body: JSON.stringify({order}),
  });
}

// Renders a data point value at its owner-configured decimal precision (see
// StationDataPoint.decimals) — the one place this formatting happens, so the
// top-bar badges, the settings list, and the history chart all agree.
export function formatDataPointValue(value: number, decimals: number): string {
  return value.toFixed(Math.max(0, Math.min(6, decimals)));
}

export type HistoryRange = '1h' | '3h' | '12h' | '1d' | '2d' | '1w' | '1m' | '3m' | '1y' | 'max';

export interface HistoryBucket {
  ts: number; // unix seconds, bucket start
  value: number;
}

// Downsampled graph data for one data point — see server/api/history.go.
// Bucket width is sized server-side to the data actually present, capped at
// ~400 points regardless of range, so this is safe to render directly
// without any further client-side thinning.
export function getStationDataPointHistory(id: number, key: string, range: HistoryRange): Promise<HistoryBucket[]> {
  return request<HistoryBucket[]>(
    `/stations/${id}/datapoints/${encodeURIComponent(key)}/history?range=${range}`,
  );
}

export function updateStationDataPointSettings(
  id: number,
  key: string,
  settings: {
    label: string;
    decimals: number;
    show_on_top_bar: boolean;
    threshold_enabled: boolean;
    threshold_value: number;
    threshold_direction: 'above' | 'below';
  },
): Promise<void> {
  return request<void>(`/stations/${id}/datapoints/${encodeURIComponent(key)}/settings`, {
    method: 'PUT',
    body: JSON.stringify(settings),
  });
}

// MetricSource: one external Super Badger Station Standard API endpoint the
// server polls on its own schedule — server-wide config, not per-station.
export interface MetricSource {
  id: number;
  name: string;
  url: string;
  api_key?: string | null;
  poll_interval_seconds: number;
  enabled: boolean;
}

export function listMetricSources(): Promise<MetricSource[]> {
  return request<MetricSource[]>('/metric-sources');
}

export function createMetricSource(params: {
  name: string;
  url: string;
  api_key?: string;
  poll_interval_seconds?: number;
  enabled?: boolean;
}): Promise<MetricSource> {
  return request<MetricSource>('/metric-sources', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export function updateMetricSource(
  id: number,
  params: {
    name: string;
    url: string;
    api_key?: string;
    poll_interval_seconds?: number;
    enabled?: boolean;
  },
): Promise<void> {
  return request<void>(`/metric-sources/${id}`, {
    method: 'PUT',
    body: JSON.stringify(params),
  });
}

export function deleteMetricSource(id: number): Promise<void> {
  return request<void>(`/metric-sources/${id}`, {method: 'DELETE'});
}
