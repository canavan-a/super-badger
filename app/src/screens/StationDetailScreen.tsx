import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {
  ActivityIndicator,
  Clipboard,
  FlatList,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';

import type {HeaderInfo} from '../App';
import {
  abortStation,
  compactStation,
  deleteStation,
  formatDataPointValue,
  formatTokens,
  getStation,
  getStationDataPoints,
  getStationUsage,
  resetStation,
  Station,
  StationDataPoint,
  TokenUsage,
} from '../api';
import {allTurns, ChatState, Part, PendingPermission, PendingQuestion, Turn} from '../chat';
import {Icon} from '../components/Icon';
import {CodeBlockSegment, parseMarkdown} from '../markdown';
import {platformConfirm} from '../platformConfirm';
import {Route} from '../routes';
import {Theme, useTheme} from '../theme';
import {QueuedMessage, useStationChat} from '../useStationChat';

type Styles = ReturnType<typeof makeStyles>;

export function StationDetailScreen({
  stationId,
  onNavigate,
  onHeaderChange,
}: {
  stationId: number;
  onNavigate: (route: Route) => void;
  onHeaderChange: (info: HeaderInfo | null) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const [station, setStation] = useState<Station | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const composerRef = useRef<TextInput>(null);

  const {
    state: chat,
    outbox,
    send: sendWs,
    reset: resetChat,
    reconnect,
    replyPermission,
    replyQuestion,
    rejectQuestion,
    dismissError,
  } = useStationChat(stationId);

  // Token/cost usage — same figures the opencode CLI's status line shows.
  // There's no event for this, so it's refetched whenever a turn finishes
  // (chat.busy true -> false) and once on mount.
  const [usage, setUsage] = useState<TokenUsage | null>(null);
  const refreshUsage = useCallback(() => {
    getStationUsage(stationId)
      .then(setUsage)
      .catch(() => {
        // Usage is a nice-to-have status line, not worth surfacing as an error.
      });
  }, [stationId]);
  useEffect(() => {
    refreshUsage();
  }, [refreshUsage]);
  useEffect(() => {
    if (!chat.busy) refreshUsage();
  }, [chat.busy, refreshUsage]);

  // Ad hoc data points (Super Badger Station Standard API) the owner chose
  // to surface on the top bar — see StationSettingsScreen for the toggle.
  // Polled independently of usage since a data point can update on its own
  // schedule regardless of chat activity.
  const [topBarPoints, setTopBarPoints] = useState<StationDataPoint[]>([]);
  useEffect(() => {
    const refreshPoints = () => {
      getStationDataPoints(stationId)
        .then(pts => setTopBarPoints(pts.filter(p => p.show_on_top_bar)))
        .catch(() => {
          // Data points are a nice-to-have status line, not worth surfacing as an error.
        });
    };
    refreshPoints();
    const interval = setInterval(refreshPoints, 15000);
    return () => clearInterval(interval);
  }, [stationId]);

  const [compacting, setCompacting] = useState(false);
  const doCompact = async () => {
    setCompacting(true);
    try {
      await compactStation(stationId);
      refreshUsage();
    } catch (err) {
      setError(String(err));
    } finally {
      setCompacting(false);
    }
  };

  // Stops a long-running or stuck in-flight turn — previously there was no
  // way to do this short of Reset, which also wipes history. Only makes
  // sense while chat.busy (there's nothing in-flight otherwise); opencode's
  // own turn-finished event flips chat.busy back off once the abort lands,
  // same as a normal reply completing.
  const [aborting, setAborting] = useState(false);
  const doAbort = async () => {
    setAborting(true);
    try {
      await abortStation(stationId);
    } catch (err) {
      setError(String(err));
    } finally {
      setAborting(false);
    }
  };

  // If the composer lost focus some other way while a reply was in flight
  // (e.g. the user manually dismissed the keyboard), jump focus back the
  // moment it finishes so they can immediately keep typing without reaching
  // for the input again. Sending itself no longer blurs it — see the
  // composer TextInput's blurOnSubmit={false}.
  const wasBusy = useRef(false);
  useEffect(() => {
    if (wasBusy.current && !chat.busy) {
      composerRef.current?.focus();
    }
    wasBusy.current = chat.busy;
  }, [chat.busy]);

  const refresh = useCallback(() => {
    setLoading(true);
    setError(null);
    getStation(stationId)
      .then(setStation)
      .catch(err => setError(String(err)))
      .finally(() => setLoading(false));
  }, [stationId]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const send = () => {
    const text = draft.trim();
    if (!text) return;
    setDraft('');
    // No local optimistic echo: the WS reliably echoes the user's own
    // message back (as a "user"-role turn) within tens of milliseconds, and
    // rendering directly from chat state (see allTurns) is what makes
    // restored history "just work" without a separate code path.
    sendWs(text);
  };

  const doReset = async () => {
    try {
      setStation(await resetStation(stationId));
      resetChat();
      reconnect();
    } catch (err) {
      setError(String(err));
    }
  };

  const doDelete = async () => {
    try {
      await deleteStation(stationId);
      onNavigate({name: 'stations'});
    } catch (err) {
      setError(String(err));
    }
  };

  const confirmDelete = () => {
    platformConfirm('Delete station', `Delete station "${station?.name}"?`, 'Delete', doDelete);
  };

  const confirmReset = () => {
    platformConfirm(
      'Reset station',
      `Reset station "${station?.name}"? This clears its chat history.`,
      'Reset',
      doReset,
    );
  };

  // The "•••" header button opens this instead of running an action
  // directly — Compact/Reset/Delete are all at least somewhat consequential
  // (Reset/Delete already confirm on top of this), so they live behind one
  // deliberate tap-to-open-menu step rather than being one accidental mis-tap
  // away on the header itself.
  const [menuOpen, setMenuOpen] = useState(false);

  // "ctx" (not "tok"/"total") since this is current context occupancy —
  // drops after a compaction — not a cumulative total-spent figure. Must
  // include cache_read: on a locally-cached model, most of a multi-turn
  // session's active context is tokens already sitting in the KV cache and
  // reused rather than reprocessed each turn — input+output alone is only
  // the small *new* slice for that one turn, which read as "a tiny
  // per-message number" instead of the real total context size (what's
  // actually resident/active for the model right now). Computed at
  // component scope (not just inside the header effect below) so the •••
  // menu's Compact row can show the same label.
  const activeContext = usage ? usage.input + usage.output + usage.cache_read + usage.cache_write : 0;
  const compactLabel = compacting
    ? 'Compacting…'
    : activeContext > 0
    ? `Compact (${formatTokens(activeContext)} ctx)`
    : 'Compact';

  // Reports title/subtitle/actions up to App's single top bar instead of
  // rendering a second header block here — one screen, one header.
  useEffect(() => {
    if (!station) {
      onHeaderChange(null);
      return;
    }
    const indicator = station.reachable
      ? station.status === 'active'
        ? {color: theme.success, label: 'active'}
        : station.status === 'error'
        ? {color: theme.danger, label: 'error'}
        : {color: theme.textMuted, label: 'idle'}
      : {color: theme.danger, label: 'unreachable'};
    // "Data" always shows (it's the only way to reach the top-bar config);
    // compact/reset/delete are opt-in like everything else on the top bar
    // (see StationSettingsScreen's Top bar section).
    const optionalActions = [
      {key: 'compact' as const, label: compactLabel, onPress: doCompact},
      {key: 'reset' as const, label: 'Reset', onPress: confirmReset},
      {key: 'delete' as const, label: 'Delete', onPress: confirmDelete, destructive: true},
    ].filter(a => station.top_bar_actions.includes(a.key));
    const badges = topBarPoints.map(p => ({label: p.label || p.key, value: formatDataPointValue(p.value, p.decimals)}));
    const showTokenCount = station.top_bar_actions.includes('tokens') && activeContext > 0;
    onHeaderChange({
      title: station.name,
      indicator,
      accentColor: station.color,
      badges,
      tokenCount: showTokenCount ? formatTokens(activeContext) : undefined,
      actions: [
        ...optionalActions,
        {label: 'Actions', icon: 'kebab', onPress: () => setMenuOpen(true)},
        {label: 'Data', icon: 'gear', onPress: () => onNavigate({name: 'stationSettings', id: stationId})},
      ],
    });
    return () => onHeaderChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [station, usage, compacting, topBarPoints, theme]);

  if (loading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator color={theme.text} />
      </View>
    );
  }

  if (error || !station) {
    return (
      <View style={styles.center}>
        <Text style={styles.error}>{error ?? 'not found'}</Text>
      </View>
    );
  }

  return (
    <View style={styles.container}>
      {!station.reachable && (
        <Text style={styles.unreachableHint}>
          The model behind this station isn't responding — is it running?
        </Text>
      )}
      {station.directory ? <Text style={styles.directory}>{station.directory}</Text> : null}

      {chat.notice && <Text style={styles.chatNotice}>{chat.notice}</Text>}
      {chat.error && (
        <View style={styles.chatErrorRow}>
          <Text style={styles.chatError}>{chat.error}</Text>
          <Pressable onPress={dismissError} accessibilityLabel="Dismiss error" hitSlop={8}>
            <Icon name="close" size={14} color={theme.danger} />
          </Pressable>
        </View>
      )}
      {chat.pendingPermission && (
        <PermissionPrompt
          styles={styles}
          permission={chat.pendingPermission}
          onReply={reply => replyPermission(chat.pendingPermission!.id, reply)}
        />
      )}
      {chat.pendingQuestion && (
        <QuestionPrompt
          styles={styles}
          theme={theme}
          question={chat.pendingQuestion}
          onReply={labels => replyQuestion(chat.pendingQuestion!.id, [labels])}
          onReject={() => rejectQuestion(chat.pendingQuestion!.id)}
        />
      )}

      <ChatList chat={chat} outbox={outbox} compacting={compacting} styles={styles} theme={theme} />

      {outbox.length > 0 && (
        <Text style={styles.queueBanner}>
          ⏳ {outbox.length} message{outbox.length > 1 ? 's' : ''} queued — will send once the current reply
          finishes
        </Text>
      )}

      <View style={styles.composer}>
        <TextInput
          ref={composerRef}
          style={styles.composerInput}
          value={draft}
          onChangeText={setDraft}
          placeholder="Prompt this station's session..."
          placeholderTextColor={theme.textMuted}
          onSubmitEditing={send}
          // A single-line TextInput defaults blurOnSubmit to true, so
          // without this, hitting send auto-dismisses the keyboard at the
          // exact instant the WS message goes out and the reply starts
          // streaming in — the resulting keyboard-close resize collided with
          // the new content landing and was a big part of the send-time
          // jank. Keeping the keyboard open (as most chat apps do) removes
          // that resize entirely.
          blurOnSubmit={false}
        />
        {
          // Always mounted (visibility/hit-testing toggled by chat.busy)
          // rather than conditionally rendered — mounting/unmounting this
          // button reflows the row (composerInput's flex width changes, plus
          // a fresh layout pass) at the same moment chat.busy flips on
          // send, compounding the same jank the keyboard fix above
          // addresses. Keeping it always present keeps that layout stable.
        }
        <Pressable
          style={[styles.stopButton, !chat.busy && styles.stopButtonHidden]}
          onPress={doAbort}
          disabled={aborting || !chat.busy}
          pointerEvents={chat.busy ? 'auto' : 'none'}>
          {aborting ? (
            <ActivityIndicator color={theme.primaryText} />
          ) : (
            <Icon name="stop" size={16} color={theme.primaryText} />
          )}
        </Pressable>
        {
          // Sending while busy no longer blocks — it queues (see
          // useStationChat's outbox/pump) instead of racing the in-flight
          // reply, matching how opencode's own CLI lets you type ahead.
        }
        <Pressable style={styles.sendButton} onPress={send}>
          {chat.busy && outbox.length === 0 ? (
            <ActivityIndicator color={theme.primaryText} />
          ) : chat.busy ? (
            <Text style={styles.sendButtonText}>Queue</Text>
          ) : (
            <Icon name="send" size={18} color={theme.primaryText} />
          )}
        </Pressable>
      </View>

      <Modal visible={menuOpen} transparent animationType="fade" onRequestClose={() => setMenuOpen(false)}>
        <Pressable style={styles.menuBackdrop} onPress={() => setMenuOpen(false)}>
          {/* Swallow taps on the card itself so they don't fall through to
              the backdrop's dismiss handler. */}
          <Pressable style={styles.menuCard} onPress={() => {}}>
            <Pressable
              style={styles.menuRow}
              disabled={compacting}
              onPress={() => {
                setMenuOpen(false);
                doCompact();
              }}>
              <Text style={[styles.menuRowText, compacting && styles.menuRowTextDisabled]}>{compactLabel}</Text>
            </Pressable>
            <View style={styles.menuDivider} />
            <Pressable
              style={styles.menuRow}
              onPress={() => {
                setMenuOpen(false);
                confirmReset();
              }}>
              <Text style={styles.menuRowText}>Reset</Text>
            </Pressable>
            <View style={styles.menuDivider} />
            <Pressable
              style={styles.menuRow}
              onPress={() => {
                setMenuOpen(false);
                confirmDelete();
              }}>
              <Text style={[styles.menuRowText, styles.menuRowTextDestructive]}>Delete</Text>
            </Pressable>
          </Pressable>
        </Pressable>
      </Modal>
    </View>
  );
}

// `inverted` FlatList is the usual chat-UI trick for "stick to bottom", but
// react-native-web's inverted implementation is flaky (janky wheel-scroll
// direction, unreliable auto-follow) — this instead renders in natural order
// and drives scrolling explicitly: `onContentSizeChange` fires whenever the
// content's height changes, which — critically — includes a reply's text
// growing in place as it streams in, not just new items being added to
// `feed`. That's paired with a near-bottom check (via onScroll) so a user
// who's scrolled up to reread history doesn't get yanked back down by
// incoming tokens; if they're already at/near the bottom, new data keeps
// them pinned there, which is the actual "stay focused on what's arriving"
// behavior wanted here.
function ChatList({
  chat,
  outbox,
  compacting,
  styles,
  theme,
}: {
  chat: ChatState;
  outbox: QueuedMessage[];
  compacting: boolean;
  styles: Styles;
  theme: Theme;
}): React.JSX.Element {
  const listRef = useRef<FlatList>(null);
  const isNearBottom = useRef(true);
  // scrollToEnd computes the target offset from FlatList's own internal
  // content-size bookkeeping, which lags behind an in-place text update just
  // enough (especially on react-native-web) that a fast-streaming reply keeps
  // out-running it — each call lands slightly short of the true bottom, so
  // the newest token is perpetually just past the edge of the viewport.
  // Tracking the actual measured height here and scrolling straight to that
  // offset (not "end") avoids depending on that internal state at all.
  const contentHeight = useRef(0);
  // Mirrors isNearBottom into render state (only on actual crossings, not
  // every onScroll tick) so the floating "Jump to bottom" button can appear.
  const [showJumpToBottom, setShowJumpToBottom] = useState(false);
  // maintainVisibleContentPosition exists for the scroll-up pagination case
  // (keeping the viewport anchored when older turns are prepended above the
  // fold) — while near the bottom during a streaming reply, it was instead
  // fighting followBottom's own scrollToOffset calls over who controls the
  // scroll position, which is what produced the flashing/jumping. Only
  // enabling it once actually scrolled away from the bottom keeps the two
  // mechanisms from ever being active at the same time.
  const [pinnedNearBottom, setPinnedNearBottom] = useState(true);

  const allChatTurns = allTurns(chat);
  // A freshly-opened long chat laying out its entire history at once is what
  // produced the "scrolls all the way down" effect — most of that history
  // isn't visible yet anyway. Instead only the most recent page is mounted
  // (so there's barely anything to lay out and it's already at the bottom),
  // with older turns paged in from `chat` — already fully in memory from the
  // one-shot history fetch, no refetching — as the user scrolls up.
  const CHAT_PAGE_SIZE = 25;
  const [visibleCount, setVisibleCount] = useState(CHAT_PAGE_SIZE);
  const turns = allChatTurns.slice(-visibleCount);
  const hasMoreAbove = allChatTurns.length > visibleCount;
  // Mirrored into a ref so renderItem below (needed for prevTurn) can stay a
  // stable useCallback instead of a fresh closure every render — a fresh
  // renderItem gives each cell a new element identity every time, which
  // would defeat TurnView's React.memo and bring back a re-render of every
  // mounted bubble on each flush tick.
  const turnsRef = useRef(turns);
  turnsRef.current = turns;

  // Guards the pagination trigger below so it fires once per "reached near
  // the top" event instead of on every one of onScroll's ~60-times-a-second
  // ticks while the user lingers up there — `maintainVisibleContentPosition`
  // re-anchors the scroll offset after each prepend, which without this
  // guard kept re-crossing the same threshold and could spiral into dozens
  // of state updates a second, each growing the render window further:
  // enough to visibly freeze the UI on a long history.
  const loadingMoreRef = useRef(false);

  // While a reply is streaming, content height grows (onContentSizeChange)
  // slightly before followBottom's scrollToOffset actually lands, so a raw
  // onScroll tick can catch a transient large distanceFromBottom for a
  // frame even though the view is (about to be) pinned to the bottom.
  // Committing pinnedNearBottom/showJumpToBottom straight from every tick
  // meant that blip flipped maintainVisibleContentPosition and the jump
  // button on and back off within milliseconds — that flicker was the
  // flashing. Debouncing the *state* updates (not isNearBottom.current,
  // which followBottom needs live) lets a one-tick blip get superseded by
  // the next tick's corrected value before it ever renders.
  const scrollSettleTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const handleScroll = (e: any) => {
    const {contentOffset, contentSize, layoutMeasurement} = e.nativeEvent;
    const distanceFromBottom = contentSize.height - (contentOffset.y + layoutMeasurement.height);
    // Keeps auto-follow (streaming replies pinning to the bottom) responsive
    // at a tight threshold — separate from the "Jump to bottom" button below,
    // which should only appear once you've actually scrolled away by a
    // decent amount, not the instant you nudge up a little.
    const nearBottom = distanceFromBottom < 150;
    isNearBottom.current = nearBottom;
    const scrolledUpAlot = distanceFromBottom > layoutMeasurement.height * 2;

    if (scrollSettleTimer.current) clearTimeout(scrollSettleTimer.current);
    scrollSettleTimer.current = setTimeout(() => {
      setPinnedNearBottom(prev => (prev === nearBottom ? prev : nearBottom));
      setShowJumpToBottom(prev => (prev === scrolledUpAlot ? prev : scrolledUpAlot));
    }, 120);

    if (contentOffset.y >= 400) {
      loadingMoreRef.current = false;
    } else if (hasMoreAbove && !loadingMoreRef.current) {
      loadingMoreRef.current = true;
      setVisibleCount(v => v + CHAT_PAGE_SIZE);
    }
  };

  // Coalesces same-frame followBottom triggers into one scrollToOffset call.
  // Right when a message is sent, onLayout (keyboard dismissing) and
  // onContentSizeChange (the new turn landing) can both fire within the same
  // frame — without this, each fired its own imperative scrollToOffset back
  // to back, which is what made sending a message visibly stutter/jump even
  // though ordinary mid-stream token arrival (only onContentSizeChange,
  // repeatedly, with a stable layout) stayed smooth.
  const followBottomScheduled = useRef(false);
  const followBottom = () => {
    if (!isNearBottom.current || followBottomScheduled.current) return;
    followBottomScheduled.current = true;
    requestAnimationFrame(() => {
      followBottomScheduled.current = false;
      if (!isNearBottom.current) return;
      // animated:false — an animation queued mid-stream just adds more lag
      // for the next update to out-run; snapping instantly is what actually
      // keeps pace with tokens arriving every ~50ms.
      listRef.current?.scrollToOffset({offset: contentHeight.current, animated: false});
    });
  };

  const jumpToBottom = () => {
    isNearBottom.current = true;
    setShowJumpToBottom(false);
    // setPinnedNearBottom(true) below disables maintainVisibleContentPosition
    // (see its own comment), but that only takes effect on the *next*
    // render — calling scrollToEnd() synchronously in this same tick raced
    // that state update, so maintainVisibleContentPosition was sometimes
    // still active and fighting the scroll, making the tap appear to do
    // nothing. Deferring to the next frame lets the render (and the prop
    // change) land first.
    setPinnedNearBottom(true);
    requestAnimationFrame(() => {
      // scrollToEnd (not the cached contentHeight offset followBottom uses)
      // for a one-off tap: it measures the list's actual current end rather
      // than relying on a height snapshot that can be a frame stale by the
      // time you tap, which is what made this land short/inconsistent. The
      // follow-up call after a beat corrects for any layout that was still
      // settling (e.g. a just-mounted paged-in row) when the first one fired.
      listRef.current?.scrollToEnd({animated: true});
      setTimeout(() => listRef.current?.scrollToEnd({animated: false}), 100);
    });
  };

  const renderItem = useCallback(
    ({item, index}: {item: Turn; index: number}) => (
      <TurnView
        turn={item}
        prevTurn={index > 0 ? turnsRef.current[index - 1] : undefined}
        compacting={compacting}
        styles={styles}
        theme={theme}
      />
    ),
    [compacting, styles, theme],
  );

  return (
    <View style={styles.chatContainer}>
      <FlatList
        ref={listRef}
        style={styles.chat}
        data={turns}
        keyExtractor={turn => turn.messageID}
        renderItem={renderItem}
        contentContainerStyle={styles.chatContent}
        onScroll={handleScroll}
        scrollEventThrottle={16}
        // Keeps the viewport's visible content stable when older turns are
        // prepended by the pagination above, instead of the scroll position
        // jumping as the list grows above the fold — only while actually
        // scrolled away from the bottom (see pinnedNearBottom's comment for
        // why this can't also be active during bottom-follow).
        maintainVisibleContentPosition={pinnedNearBottom ? undefined : {minIndexForVisible: 0}}
        onContentSizeChange={(_w, h) => {
          contentHeight.current = h;
          followBottom();
        }}
        onLayout={() => followBottom()}
        ListFooterComponent={
          outbox.length > 0 ? (
            <View style={styles.queueList}>
              {outbox.map(m => (
                <View key={m.id} style={styles.queuedItem}>
                  <View style={styles.queuedBadge}>
                    <Text style={styles.queuedBadgeText}>⏳ Queued</Text>
                  </View>
                  <View style={[styles.bubble, styles.bubbleYou, styles.bubbleQueued]}>
                    <Text style={styles.youText}>{m.text}</Text>
                  </View>
                </View>
              ))}
            </View>
          ) : null
        }
        // Only a handful of turns need to be mounted at once; keeping these
        // small is what makes virtualization actually pay off on a long
        // history instead of just being FlatList's defaults.
        initialNumToRender={12}
        maxToRenderPerBatch={8}
        windowSize={7}
        updateCellsBatchingPeriod={50}
        // removeClippedSubviews fights the frequent onContentSizeChange
        // updates during token streaming (every ~50ms) — its clip/unclip
        // recalculation races the layout update and cells flash in/out for
        // a frame, right at the bottom where the growing reply and
        // auto-scroll are both active. The mounted window is already small
        // (initialNumToRender/windowSize above), so the memory savings
        // aren't worth the flicker.
      />
      {showJumpToBottom && (
        <Pressable style={styles.jumpToBottomButton} onPress={jumpToBottom}>
          <Text style={styles.jumpToBottomText}>↓ Jump to bottom</Text>
        </Pressable>
      )}
    </View>
  );
}

// opencode pauses this session's entire agent loop until answered — there is
// no timeout and no other way to unstick it (this was the "stuck on read"
// report: a tool needed permission and nothing was ever surfacing it or
// letting the user respond).
function PermissionPrompt({
  permission,
  onReply,
  styles,
}: {
  permission: PendingPermission;
  onReply: (reply: 'once' | 'always' | 'reject') => void;
  styles: Styles;
}): React.JSX.Element {
  return (
    <View style={styles.permissionBox}>
      <Text style={styles.permissionTitle}>Permission requested: {permission.permission}</Text>
      {permission.patterns.length > 0 && (
        <Text style={styles.permissionPatterns} numberOfLines={3}>
          {permission.patterns.join(', ')}
        </Text>
      )}
      <View style={styles.permissionActions}>
        <Pressable style={styles.permissionButton} onPress={() => onReply('once')}>
          <Text style={styles.permissionButtonText}>Allow once</Text>
        </Pressable>
        <Pressable style={styles.permissionButton} onPress={() => onReply('always')}>
          <Text style={styles.permissionButtonText}>Always allow</Text>
        </Pressable>
        <Pressable
          style={[styles.permissionButton, styles.permissionDeny]}
          onPress={() => onReply('reject')}>
          <Text style={styles.permissionButtonText}>Deny</Text>
        </Pressable>
      </View>
    </View>
  );
}

// opencode's separate AskUserQuestion-style tool — structurally like a
// permission request (pauses the agent loop until answered) but a distinct
// mechanism/API (question.asked, not permission.asked — see
// server/opencode/client.go). Was previously not surfaced anywhere at all,
// so answering it was impossible from the app ("the question tool isn't
// passing back through"). Selecting an option only highlights it — nothing
// sends until Submit is pressed, single- or multi-select alike (the
// original single-select-submits-instantly behavior was too easy to
// mis-tap). When the question allows it (`custom`), an "Other…" option
// reveals a free-text field whose contents count as one more answer.
function QuestionPrompt({
  question,
  onReply,
  onReject,
  styles,
  theme,
}: {
  question: PendingQuestion;
  onReply: (labels: string[]) => void;
  onReject: () => void;
  styles: Styles;
  theme: Theme;
}): React.JSX.Element {
  const [selected, setSelected] = useState<string[]>([]);
  const [customActive, setCustomActive] = useState(false);
  const [customText, setCustomText] = useState('');

  const toggleOption = (label: string) => {
    if (question.multiple) {
      setSelected(prev => (prev.includes(label) ? prev.filter(l => l !== label) : [...prev, label]));
    } else {
      setSelected([label]);
      setCustomActive(false);
    }
  };

  const toggleCustom = () => {
    if (question.multiple) {
      setCustomActive(a => !a);
    } else {
      setCustomActive(true);
      setSelected([]);
    }
  };

  const trimmedCustom = customText.trim();
  const answers = customActive && trimmedCustom ? [...selected, trimmedCustom] : selected;
  const canSubmit = answers.length > 0;

  return (
    <View style={styles.permissionBox}>
      {question.header ? <Text style={styles.permissionTitle}>{question.header}</Text> : null}
      <Text style={styles.permissionPatterns}>{question.question}</Text>
      <View style={styles.questionOptions}>
        {question.options.map(opt => (
          <Pressable
            key={opt.label}
            style={[styles.questionOption, selected.includes(opt.label) && styles.questionOptionSelected]}
            onPress={() => toggleOption(opt.label)}>
            <Text style={styles.questionOptionLabel}>{opt.label}</Text>
            {opt.description ? <Text style={styles.questionOptionDesc}>{opt.description}</Text> : null}
          </Pressable>
        ))}
        {question.custom && (
          <Pressable
            style={[styles.questionOption, customActive && styles.questionOptionSelected]}
            onPress={toggleCustom}>
            <Text style={styles.questionOptionLabel}>Other…</Text>
          </Pressable>
        )}
      </View>
      {customActive && (
        <TextInput
          style={styles.questionCustomInput}
          value={customText}
          onChangeText={setCustomText}
          placeholder="Type your own answer"
          placeholderTextColor={theme.textMuted}
          autoFocus
        />
      )}
      <View style={styles.permissionActions}>
        <Pressable
          style={[styles.permissionButton, !canSubmit && styles.permissionButtonDisabled]}
          disabled={!canSubmit}
          onPress={() => onReply(answers)}>
          <Text style={styles.permissionButtonText}>Submit</Text>
        </Pressable>
        <Pressable style={[styles.permissionButton, styles.permissionDeny]} onPress={onReject}>
          <Text style={styles.permissionButtonText}>Dismiss</Text>
        </Pressable>
      </View>
    </View>
  );
}

// A real user prompt always has non-empty text (send() trims and bails on
// empty). Auto-compaction (opencode compacting on its own when context fills
// up, not via the Compact button) turned out to go through this same
// message.updated/message.part.updated stream as a user+assistant turn pair
// with no text content at all — that's a signature nothing else produces, so
// it doubles as "this pair is a compaction artifact" regardless of what
// triggered it, not just the manual button's `compacting` flag.
function isEmptyUserTurn(t: Turn | undefined): boolean {
  return (
    !!t &&
    t.role === 'user' &&
    !t.partOrder.some(id => {
      const p = t.parts[id];
      return p.kind === 'text' && p.text;
    })
  );
}

// Memoized: without this, every mounted bubble re-renders on every 150ms WS
// flush tick (see useStationChat's FLUSH_INTERVAL_MS comment) even though
// only the actively-streaming turn's data actually changed — chat.ts's
// updateTurn/upsertPart copy-on-write only the touched turn, so `turn`'s
// reference is a reliable "did this one actually change" signal.
const TurnView = React.memo(function TurnView({
  turn,
  prevTurn,
  compacting,
  styles,
  theme,
}: {
  turn: Turn;
  prevTurn?: Turn;
  compacting: boolean;
  styles: Styles;
  theme: Theme;
}): React.JSX.Element | null {
  if (turn.role === 'user') {
    // Hide rather than show an empty bubble ("a dot for mine") — the paired
    // assistant turn right after it (below) carries the one visible badge
    // for this pair instead.
    if (isEmptyUserTurn(turn)) return null;
    return (
      <View style={[styles.bubble, styles.bubbleYou]}>
        {turn.partOrder.map(id => {
          const part = turn.parts[id];
          return part.kind === 'text' && part.text ? (
            <Text key={id} style={styles.youText}>
              {part.text}
            </Text>
          ) : null;
        })}
      </View>
    );
  }
  const isCompactionReply = turn.partOrder.length === 0 && (compacting || isEmptyUserTurn(prevTurn));
  return (
    <View style={[styles.bubble, styles.bubbleAssistant]}>
      {turn.partOrder.map(id => (
        <PartView key={id} part={turn.parts[id]} styles={styles} theme={theme} />
      ))}
      {turn.partOrder.length === 0 &&
        (isCompactionReply ? (
          // Compact (button, or opencode auto-compacting on its own) creates
          // its own empty turn pair on the wire with no parts ever streamed
          // into it — so without this it rendered as an indefinite bare
          // spinner with nothing to explain it ("a blank dot chat"). A
          // normal in-flight reply's spinner (real content on the way) is
          // unaffected by this check.
          <Text style={styles.chip}>⚙ Compaction triggered</Text>
        ) : (
          !turn.done && <ActivityIndicator size="small" color={theme.textMuted} />
        ))}
    </View>
  );
});

const PartView = React.memo(function PartView({
  part,
  styles,
  theme,
}: {
  part: Part;
  styles: Styles;
  theme: Theme;
}): React.JSX.Element | null {
  switch (part.kind) {
    case 'reasoning':
      if (!part.text) return null;
      return (
        <View style={styles.reasoningBox}>
          <Text style={styles.reasoningLabel}>Thinking</Text>
          <Text style={styles.reasoningText}>{part.text}</Text>
        </View>
      );
    case 'text':
      if (!part.text) return null;
      return <MarkdownText text={part.text} styles={styles} theme={theme} />;
    case 'tool':
      return <ToolPartView part={part} styles={styles} />;
    case 'file':
      return <Text style={styles.chip}>📄 {part.label}</Text>;
    case 'subtask':
      return <Text style={styles.chip}>↳ {part.label}</Text>;
    case 'notice':
      return <Text style={styles.notice}>{part.text}</Text>;
    case 'step-finish':
      return null;
    default:
      return null;
  }
});

function runStyle(run: {code: boolean; bold: boolean; italic: boolean}, styles: Styles) {
  return [
    run.code && styles.inlineCode,
    run.bold && styles.boldText,
    run.italic && styles.italicText,
  ];
}

const InlineRuns = React.memo(function InlineRuns({
  runs,
  styles,
}: {
  runs: {code: boolean; bold: boolean; italic: boolean; value: string}[];
  styles: Styles;
}): React.JSX.Element {
  return (
    <>
      {runs.map((run, j) => (
        <Text key={j} style={runStyle(run, styles)}>
          {run.value}
        </Text>
      ))}
    </>
  );
});

// Renders a full (small-subset) markdown document: headings, list items,
// paragraphs with bold/italic/inline-code, and fenced code blocks with basic
// token-colored syntax highlighting (monospace, horizontally scrollable so
// long lines don't force-wrap or blow out the bubble width) — replaces what
// used to be a literal wall of backticks and markdown source characters.
function MarkdownText({text, styles, theme}: {text: string; styles: Styles; theme: Theme}): React.JSX.Element {
  // Memoized on `text`: during active streaming this still reparses on
  // every flush tick (the text itself changes), but it skips redoing the
  // full heading/list/code-block/table scan for every *other*, already-
  // finished message in the chat that re-renders for unrelated reasons
  // (parent state changes, theme, etc.) — those don't need reparsing at all
  // since their text hasn't changed.
  const segments = useMemo(() => parseMarkdown(text), [text]);
  const codeTokenStyles = useMemo(() => codeTokenStyleMap(theme), [theme]);
  return (
    <>
      {segments.map((seg, i) => {
        switch (seg.kind) {
          case 'code-block':
            return <CodeBlockView key={i} seg={seg} styles={styles} theme={theme} codeTokenStyles={codeTokenStyles} />;
          case 'heading':
            return (
              <Text key={i} style={[styles.assistantText, styles.heading, HEADING_STYLES[seg.level] ?? null]}>
                <InlineRuns runs={seg.runs} styles={styles} />
              </Text>
            );
          case 'list-item':
            return (
              <View key={i} style={styles.listItemRow}>
                <Text style={styles.listBullet}>{seg.ordered ? '•' : '–'}</Text>
                <Text style={styles.assistantText}>
                  <InlineRuns runs={seg.runs} styles={styles} />
                </Text>
              </View>
            );
          case 'paragraph':
          default:
            return (
              <Text key={i} style={styles.assistantText}>
                <InlineRuns runs={seg.runs} styles={styles} />
              </Text>
            );
        }
      })}
    </>
  );
}

// Collapsed line cap for a code block before "Show more" appears — without
// this a single long ```bash output rendered with no height limit could grow
// to dominate the whole chat FlatList row (see ToolPartView's numberOfLines
// truncation above, which this mirrors).
const CODE_BLOCK_COLLAPSED_LINES = 12;

const CodeBlockView = React.memo(function CodeBlockView({
  seg,
  styles,
  theme,
  codeTokenStyles,
}: {
  seg: CodeBlockSegment;
  styles: Styles;
  theme: Theme;
  codeTokenStyles: Record<string, object>;
}): React.JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const lineCount = seg.code.split('\n').length;
  const showToggle = lineCount > CODE_BLOCK_COLLAPSED_LINES;

  const copy = () => {
    Clipboard.setString(seg.code);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <View style={styles.codeBlock}>
      <View style={styles.codeBlockHeader}>
        {seg.lang ? <Text style={styles.codeLang}>{seg.lang}</Text> : <View style={styles.codeLangSpacer} />}
        <Pressable style={styles.codeCopyButton} onPress={copy} accessibilityLabel="Copy code">
          {copied ? (
            <Text style={styles.codeCopiedText}>Copied</Text>
          ) : (
            <Icon name="copy" size={14} color={theme.textMuted} />
          )}
        </Pressable>
      </View>
      <View style={expanded ? styles.codeScrollExpanded : styles.codeScrollClamped}>
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          // Nested inside the chat's vertical FlatList - without this,
          // Android's gesture arbitration can give the outer list the touch
          // stream entirely, so a horizontal swipe on a code block never
          // actually scrolls it. A *vertical* ScrollView here too (to make
          // the expanded height interactively scrollable) was tried and
          // reverted — nesting a vertical scroller inside a FlatList row
          // fought the outer list for scroll/gesture control, breaking
          // auto-follow-to-bottom during streaming and causing visible
          // jumping. maxHeight + overflow:hidden below still caps the
          // height either way; content past that cap just isn't reachable
          // by scrolling — use the copy button for the rest.
          nestedScrollEnabled
          directionalLockEnabled>
          <Text style={styles.codeText}>
            {seg.tokens.map((tok, j) => (
              <Text key={j} style={codeTokenStyles[tok.type]}>
                {tok.text}
              </Text>
            ))}
          </Text>
        </ScrollView>
      </View>
      {showToggle && (
        <Text style={styles.toolToggle} onPress={() => setExpanded(e => !e)}>
          {expanded ? 'Show less' : 'Show more'}
        </Text>
      )}
    </View>
  );
});


function codeTokenStyleMap(theme: Theme): Record<string, object> {
  return {
    plain: {},
    keyword: {color: theme.danger, fontWeight: '700'},
    string: {color: theme.success},
    comment: {color: theme.textMuted, fontStyle: 'italic'},
    number: {color: theme.primary},
  };
}

const HEADING_STYLES: Record<number, object> = {
  1: {fontSize: 18, fontWeight: '800'},
  2: {fontSize: 16, fontWeight: '800'},
  3: {fontSize: 14, fontWeight: '700'},
};

// Long unbroken lines (a path, a URL, single-line output with no spaces)
// used to overflow the bubble horizontally instead of wrapping — same root
// cause as the header-overflow bug fixed earlier (a flex child needs
// minWidth:0 to actually shrink/wrap instead of forcing its container
// wider), fixed on toolBox/toolOutput/toolError below. The numberOfLines
// clipping is also now tap-to-expand instead of a silent permanent cutoff.
// The command/args a tool call was invoked with — arrives in `state.input`
// from the very start (it's needed to invoke the tool at all), unlike
// `title`/`output` which some tools only fill in once running/completed.
// Was already captured into Part.input by chat.ts's partFromRaw, but never
// rendered anywhere, so a pending/running call showed nothing but the bare
// tool name ("pending bash") with no indication of what it was actually
// about to run.
function formatToolInput(input: unknown): string | null {
  if (input == null) return null;
  if (typeof input === 'object' && 'command' in (input as Record<string, unknown>)) {
    const command = (input as Record<string, unknown>).command;
    if (typeof command === 'string') return command;
  }
  try {
    return JSON.stringify(input);
  } catch {
    return null;
  }
}

function ToolPartView({part, styles}: {part: Extract<Part, {kind: 'tool'}>; styles: Styles}): React.JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const badgeStyle =
    part.status === 'completed'
      ? styles.toolBadgeOk
      : part.status === 'error'
      ? styles.toolBadgeError
      : styles.toolBadgePending;

  const inputText = part.status === 'pending' || part.status === 'running' ? formatToolInput(part.input) : null;

  // numberOfLines can't tell us whether it actually truncated anything, so
  // this is a rough proxy for "is there plausibly more than the clip shows"
  // — good enough to decide whether the toggle is worth showing at all.
  const showToggle =
    (part.error?.length ?? 0) > 200 ||
    (part.output?.length ?? 0) > 200 ||
    (part.title?.length ?? 0) > 40 ||
    (inputText?.length ?? 0) > 200;

  return (
    <View style={styles.toolBox}>
      <Pressable style={styles.toolHeader} onPress={() => setExpanded(e => !e)}>
        <Text style={[styles.toolBadge, badgeStyle]}>{part.status}</Text>
        <Text style={styles.toolName} numberOfLines={expanded ? undefined : 1}>
          {part.title || part.tool}
        </Text>
      </Pressable>
      {inputText ? (
        <Pressable onPress={() => setExpanded(e => !e)}>
          <Text style={styles.toolInputText} numberOfLines={expanded ? undefined : 4}>
            {inputText}
          </Text>
        </Pressable>
      ) : null}
      {part.status === 'error' && part.error ? (
        <Pressable onPress={() => setExpanded(e => !e)}>
          <Text style={styles.toolError} numberOfLines={expanded ? undefined : 4}>
            {part.error}
          </Text>
        </Pressable>
      ) : null}
      {part.status === 'completed' && part.output ? (
        <Pressable onPress={() => setExpanded(e => !e)}>
          <Text style={styles.toolOutput} numberOfLines={expanded ? undefined : 6}>
            {part.output}
          </Text>
        </Pressable>
      ) : null}
      {showToggle && (
        <Text style={styles.toolToggle} onPress={() => setExpanded(e => !e)}>
          {expanded ? 'Show less' : 'Show more'}
        </Text>
      )}
    </View>
  );
}



function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    center: {
      flex: 1,
      alignItems: 'center',
      justifyContent: 'center',
      backgroundColor: theme.bg,
    },
    error: {
      color: theme.danger,
    },
    menuBackdrop: {
      flex: 1,
      backgroundColor: 'rgba(0,0,0,0.3)',
      alignItems: 'flex-end',
      padding: 12,
    },
    menuCard: {
      minWidth: 180,
      borderRadius: 10,
      backgroundColor: theme.surface,
      borderWidth: 1,
      borderColor: theme.border,
      overflow: 'hidden',
    },
    menuRow: {
      paddingHorizontal: 16,
      paddingVertical: 14,
    },
    menuRowText: {
      fontSize: 15,
      fontWeight: '600',
      color: theme.text,
    },
    menuRowTextDisabled: {
      opacity: 0.5,
    },
    menuRowTextDestructive: {
      color: theme.danger,
    },
    menuDivider: {
      height: 1,
      backgroundColor: theme.border,
    },
    directory: {
      fontSize: 11,
      color: theme.textMuted,
      marginTop: 2,
      paddingHorizontal: 16,
      fontFamily: 'monospace',
    },
    unreachableHint: {
      fontSize: 12,
      color: theme.danger,
      paddingHorizontal: 16,
      paddingTop: 8,
    },
    chatErrorRow: {
      flexDirection: 'row',
      alignItems: 'flex-start',
      justifyContent: 'space-between',
      gap: 8,
      paddingHorizontal: 16,
      paddingTop: 8,
    },
    chatError: {
      flex: 1,
      minWidth: 0,
      color: theme.danger,
      fontSize: 13,
    },
    chatNotice: {
      color: theme.textMuted,
      paddingHorizontal: 16,
      paddingTop: 8,
      fontSize: 13,
      fontStyle: 'italic',
    },
    permissionBox: {
      margin: 16,
      marginBottom: 0,
      padding: 12,
      borderRadius: 8,
      borderWidth: 1,
      borderColor: theme.primary,
      backgroundColor: theme.surfaceAlt,
    },
    permissionTitle: {
      fontSize: 13,
      fontWeight: '700',
      fontFamily: 'monospace',
      color: theme.text,
    },
    permissionPatterns: {
      fontSize: 12,
      color: theme.textMuted,
      fontFamily: 'monospace',
      marginTop: 4,
    },
    permissionActions: {
      flexDirection: 'row',
      gap: 8,
      marginTop: 10,
    },
    permissionButton: {
      paddingHorizontal: 12,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.primary,
    },
    permissionDeny: {
      backgroundColor: theme.danger,
    },
    permissionButtonText: {
      color: theme.primaryText,
      fontWeight: '600',
      fontSize: 13,
    },
    questionOptions: {
      gap: 6,
      marginTop: 10,
    },
    questionOption: {
      padding: 10,
      borderRadius: 6,
      borderWidth: 1,
      borderColor: theme.border,
      backgroundColor: theme.surface,
    },
    questionOptionSelected: {
      borderColor: theme.primary,
      backgroundColor: theme.surfaceAlt,
    },
    questionOptionLabel: {
      fontSize: 13,
      fontWeight: '600',
      color: theme.text,
    },
    questionOptionDesc: {
      fontSize: 11,
      color: theme.textMuted,
      marginTop: 2,
    },
    questionCustomInput: {
      marginTop: 8,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 8,
      fontSize: 13,
      color: theme.text,
      backgroundColor: theme.surface,
    },
    permissionButtonDisabled: {
      opacity: 0.5,
    },
    chatContainer: {
      flex: 1,
    },
    chat: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    jumpToBottomButton: {
      position: 'absolute',
      bottom: 12,
      alignSelf: 'center',
      flexDirection: 'row',
      alignItems: 'center',
      paddingHorizontal: 14,
      paddingVertical: 8,
      borderRadius: 999,
      backgroundColor: theme.primary,
      shadowColor: '#000',
      shadowOpacity: 0.2,
      shadowRadius: 4,
      shadowOffset: {width: 0, height: 2},
      elevation: 4,
    },
    jumpToBottomText: {
      color: theme.primaryText,
      fontSize: 13,
      fontWeight: '700',
    },
    chatContent: {
      padding: 16,
      // Generous breathing room below the last bubble specifically — a reply
      // still streaming in otherwise sits flush against (or under) the
      // composer, so newly-arriving text at the very bottom reads as cut off
      // right as it appears. This needs to be large (not just a few px):
      // auto-scroll only fires on content-size change, so there's always a
      // brief window where the freshest token is right at the viewport edge —
      // a big fixed buffer keeps that edge comfortably below the fold instead
      // of exactly at it.
      paddingBottom: 120,
      gap: 10,
    },
    bubble: {
      maxWidth: '85%',
      borderRadius: 10,
      padding: 10,
    },
    bubbleYou: {
      alignSelf: 'flex-end',
      backgroundColor: theme.bubbleUser,
    },
    // Dashed border (not just opacity) so this reads as "hasn't been sent
    // yet" rather than "older/faded text" — dimming alone looked too close
    // to a normal message that had simply scrolled out of focus.
    bubbleQueued: {
      opacity: 0.7,
      borderWidth: 1,
      borderStyle: 'dashed',
      borderColor: theme.textMuted,
    },
    queueList: {
      gap: 4,
      marginTop: 10,
    },
    queuedItem: {
      alignItems: 'flex-end',
      gap: 2,
    },
    queuedBadge: {
      paddingHorizontal: 8,
      paddingVertical: 2,
      borderRadius: 10,
      backgroundColor: theme.surfaceAlt,
    },
    queuedBadgeText: {
      fontSize: 10,
      fontWeight: '700',
      textTransform: 'uppercase',
      color: theme.textMuted,
    },
    queueBanner: {
      fontSize: 12,
      color: theme.textMuted,
      paddingHorizontal: 16,
      paddingTop: 8,
      fontStyle: 'italic',
    },
    bubbleAssistant: {
      alignSelf: 'flex-start',
      // Wider than the shared 85% cap (still shy of 100% so it doesn't
      // touch the screen edge) — the assistant's replies are the content
      // being read, so they benefit from the extra width more than the
      // user's own short prompts do.
      maxWidth: '95%',
      backgroundColor: theme.bubbleAssistant,
      gap: 6,
    },
    youText: {
      fontSize: 13,
      color: theme.bubbleUserText,
    },
    assistantText: {
      fontSize: 13,
      color: theme.bubbleAssistantText,
    },
    boldText: {
      fontWeight: '700',
    },
    italicText: {
      fontStyle: 'italic',
    },
    heading: {
      marginTop: 2,
      marginBottom: 2,
    },
    listItemRow: {
      flexDirection: 'row',
      gap: 6,
      paddingLeft: 2,
    },
    listBullet: {
      fontSize: 13,
      color: theme.bubbleAssistantText,
    },
    inlineCode: {
      fontFamily: 'monospace',
      backgroundColor: theme.inlineCodeBg,
      fontSize: 12,
    },
    codeBlock: {
      backgroundColor: theme.codeBg,
      borderWidth: 1,
      borderColor: theme.codeBorder,
      borderRadius: 6,
      padding: 8,
    },
    codeBlockHeader: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'space-between',
      marginBottom: 4,
    },
    codeLang: {
      fontSize: 10,
      fontWeight: '700',
      color: theme.textMuted,
      textTransform: 'uppercase',
    },
    codeLangSpacer: {
      flex: 1,
    },
    codeCopyButton: {
      padding: 4,
    },
    codeCopiedText: {
      fontSize: 10,
      fontWeight: '600',
      color: theme.textMuted,
    },
    codeText: {
      fontFamily: 'monospace',
      fontSize: 12,
      lineHeight: 16,
      color: theme.codeText,
    },
    // A hard height cap via maxHeight on the wrapping View (not
    // numberOfLines on the Text) — numberOfLines doesn't reliably clip a
    // Text inside a horizontal ScrollView (the ScrollView gives it
    // unconstrained width, and Android in particular can then ignore the
    // line clamp). "Show more" switches to codeScrollExpanded, a *larger*
    // fixed cap with its own vertical scroll — never fully unbounded, so an
    // enormous block (a huge table, a long log dump) can't grow to dominate
    // the whole chat even when expanded; you scroll within the block itself.
    codeScrollClamped: {
      maxHeight: 16 * CODE_BLOCK_COLLAPSED_LINES,
      overflow: 'hidden',
    },
    codeScrollExpanded: {
      maxHeight: 16 * 40,
      overflow: 'hidden',
    },
    reasoningBox: {
      borderLeftWidth: 2,
      borderLeftColor: theme.border,
      paddingLeft: 8,
    },
    reasoningLabel: {
      fontSize: 10,
      fontWeight: '700',
      color: theme.textMuted,
      textTransform: 'uppercase',
      marginBottom: 2,
    },
    reasoningText: {
      fontSize: 12,
      color: theme.textMuted,
      fontStyle: 'italic',
    },
    chip: {
      fontSize: 12,
      color: theme.textMuted,
      backgroundColor: theme.surfaceAlt,
      alignSelf: 'flex-start',
      paddingHorizontal: 8,
      paddingVertical: 2,
      borderRadius: 10,
    },
    notice: {
      fontSize: 11,
      color: theme.textMuted,
      fontStyle: 'italic',
    },
    toolBox: {
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      padding: 8,
      backgroundColor: theme.surface,
      minWidth: 0,
      alignSelf: 'stretch',
    },
    toolHeader: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 6,
      minWidth: 0,
    },
    toolBadge: {
      fontSize: 10,
      fontWeight: '700',
      textTransform: 'uppercase',
      paddingHorizontal: 6,
      paddingVertical: 2,
      borderRadius: 4,
      overflow: 'hidden',
    },
    toolBadgePending: {
      backgroundColor: theme.surfaceAlt,
      color: theme.textMuted,
    },
    toolBadgeOk: {
      backgroundColor: theme.surfaceAlt,
      color: theme.success,
    },
    toolBadgeError: {
      backgroundColor: theme.dangerBg,
      color: theme.danger,
    },
    toolName: {
      flex: 1,
      minWidth: 0,
      fontSize: 12,
      fontWeight: '600',
      fontFamily: 'monospace',
      color: theme.text,
    },
    toolOutput: {
      fontSize: 11,
      fontFamily: 'monospace',
      color: theme.textMuted,
      marginTop: 4,
      flexShrink: 1,
      minWidth: 0,
      // wordBreak is a react-native-web-only CSS property; RN itself wraps
      // on whitespace only, which does nothing for an unbroken long token
      // (a path/URL) — this is what actually forces those to wrap.
      // @ts-expect-error not in RN's ViewStyle type
      wordBreak: 'break-all',
    },
    toolError: {
      fontSize: 11,
      fontFamily: 'monospace',
      color: theme.danger,
      marginTop: 4,
      flexShrink: 1,
      minWidth: 0,
      wordBreak: 'break-all',
    },
    // Distinct from toolOutput (the result) — this is the request, shown
    // while pending/running so there's something to look at besides the bare
    // tool name (e.g. the actual bash command about to run).
    toolInputText: {
      fontSize: 11,
      fontFamily: 'monospace',
      color: theme.text,
      marginTop: 4,
      flexShrink: 1,
      minWidth: 0,
      wordBreak: 'break-all',
    },
    toolToggle: {
      fontSize: 11,
      fontWeight: '600',
      color: theme.primary,
      marginTop: 6,
    },
    composer: {
      flexDirection: 'row',
      padding: 12,
      gap: 8,
      borderTopWidth: 1,
      borderTopColor: theme.border,
      backgroundColor: theme.bg,
    },
    composerInput: {
      flex: 1,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 8,
      color: theme.text,
      backgroundColor: theme.surface,
    },
    sendButton: {
      backgroundColor: theme.primary,
      borderRadius: 6,
      paddingHorizontal: 16,
      justifyContent: 'center',
    },
    sendButtonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
    stopButton: {
      backgroundColor: theme.danger,
      borderRadius: 6,
      paddingHorizontal: 16,
      alignItems: 'center',
      justifyContent: 'center',
    },
    // Kept mounted (not conditionally rendered) so toggling chat.busy never
    // reflows the composer row — see its usage site's comment.
    stopButtonHidden: {
      opacity: 0,
    },
  });
}
