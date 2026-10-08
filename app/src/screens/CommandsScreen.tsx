import React, {useCallback, useEffect, useState} from 'react';
import {ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View} from 'react-native';

import {Command, CommandOption, CommandResult, listCommands, runCommand} from '../api';
import {platformConfirm} from '../platformConfirm';
import {Theme, useTheme} from '../theme';

// CommandsScreen renders the commands the metric sources advertise on their
// commands endpoints (see docs/command-spec.md), sectioned by source (when
// there's more than one) and by each command's `group`. A command with
// `options` is a picker: tapping it expands its choices inline. Each run is
// a POST the server streams back — starting a model can take minutes — so
// the row shows the latest progress line while it runs and the outcome
// afterwards, and the list is refetched (a finished command usually changes
// what's active).
export function CommandsScreen(): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [commands, setCommands] = useState<Command[]>([]);
  // Keyed by source + path: two sources can each advertise the same path.
  const [runningKey, setRunningKey] = useState<string | null>(null);
  const [progress, setProgress] = useState('');
  const [results, setResults] = useState<Record<string, CommandResult>>({});
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  const refresh = useCallback(() => {
    listCommands().then(setCommands).catch(() => setCommands([]));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const start = (cmd: Command, option?: string) => {
    if (runningKey !== null) return; // one command at a time
    const key = commandKey(cmd);
    setRunningKey(key);
    setExpandedKey(null);
    setProgress('');
    setResults(r => Object.fromEntries(Object.entries(r).filter(([k]) => k !== key)));
    runCommand(cmd.source_id, cmd.path, option, setProgress)
      .catch(err => ({ok: false, message: String(err?.message ?? err)}))
      .then(result => setResults(r => ({...r, [key]: result})))
      .finally(() => {
        setRunningKey(null);
        refresh();
      });
  };

  const run = (cmd: Command, option?: CommandOption) => {
    if (!cmd.confirm) {
      start(cmd, option?.value);
      return;
    }
    const title = option ? `${cmd.label || cmd.path}: ${option.label || option.value}` : cmd.label || cmd.path;
    platformConfirm(title, cmd.confirm, 'Run', () => start(cmd, option?.value), false);
  };

  const onPressRow = (cmd: Command) => {
    const key = commandKey(cmd);
    if (cmd.options?.length) {
      setExpandedKey(k => (k === key ? null : key));
      return;
    }
    run(cmd);
  };

  const multiSource = new Set(commands.map(c => c.source_id)).size > 1;

  const rows: React.ReactNode[] = [];
  commands.forEach((cmd, i) => {
    const prev = commands[i - 1];
    const newSource = !prev || prev.source_id !== cmd.source_id;
    if (multiSource && newSource) {
      rows.push(
        <Text key={`src:${cmd.source_id}`} style={styles.sourceHeader}>
          {cmd.source_name}
        </Text>,
      );
    }
    if (cmd.group && (newSource || prev.group !== cmd.group)) {
      rows.push(
        <Text key={`grp:${cmd.source_id}:${i}`} style={styles.groupHeader}>
          {cmd.group}
        </Text>,
      );
    }

    const key = commandKey(cmd);
    const running = runningKey === key;
    const expanded = expandedKey === key;
    const result = results[key];
    const activeOption = cmd.options?.find(o => o.active);

    let subtitle: React.ReactNode = null;
    if (running) {
      subtitle = <Text style={styles.subtitle} numberOfLines={2}>{progress || 'running…'}</Text>;
    } else if (result) {
      subtitle = (
        <Text style={[styles.subtitle, !result.ok && styles.error]} numberOfLines={3}>
          {result.message || (result.ok ? 'done' : 'failed')}
        </Text>
      );
    } else if (!multiSource && !cmd.group) {
      subtitle = <Text style={styles.subtitle}>{cmd.source_name}</Text>;
    }

    rows.push(
      <Pressable
        key={key}
        style={[styles.row, (running || cmd.active) && styles.rowHighlight]}
        onPress={() => onPressRow(cmd)}
        disabled={runningKey !== null}>
        {running ? (
          <ActivityIndicator size="small" color={theme.text} style={styles.dot} />
        ) : (
          <View style={[styles.dot, cmd.active ? {backgroundColor: theme.primary} : styles.dotIdle]} />
        )}
        <View style={styles.textCol}>
          <Text style={styles.label}>{cmd.label || cmd.path}</Text>
          {subtitle}
        </View>
        {cmd.options?.length ? (
          <Text style={styles.value}>
            {activeOption ? activeOption.label || activeOption.value : ''} {expanded ? '▴' : '▾'}
          </Text>
        ) : null}
      </Pressable>,
    );

    if (expanded && cmd.options) {
      cmd.options.forEach(opt => {
        rows.push(
          <Pressable
            key={`${key}=${opt.value}`}
            style={styles.optionRow}
            onPress={() => run(cmd, opt)}
            disabled={runningKey !== null}>
            <Text style={[styles.check, {color: theme.primary}]}>{opt.active ? '✓' : ''}</Text>
            <Text style={[styles.optionLabel, opt.active && styles.optionActive]}>{opt.label || opt.value}</Text>
          </Pressable>,
        );
      });
    }
  });

  return <ScrollView style={styles.container}>{rows}</ScrollView>;
}

function commandKey(cmd: Command): string {
  return `${cmd.source_id}:${cmd.path}`;
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    sourceHeader: {
      fontSize: 13,
      fontWeight: '700',
      color: theme.text,
      paddingHorizontal: 16,
      paddingTop: 18,
      paddingBottom: 2,
    },
    groupHeader: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.textMuted,
      textTransform: 'uppercase',
      letterSpacing: 0.5,
      paddingHorizontal: 16,
      paddingTop: 14,
      paddingBottom: 6,
    },
    row: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 10,
      paddingHorizontal: 16,
      paddingVertical: 14,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
    },
    rowHighlight: {
      backgroundColor: theme.surfaceAlt,
    },
    dot: {
      width: 10,
      height: 10,
      borderRadius: 5,
    },
    dotIdle: {
      borderWidth: 1,
      borderColor: theme.textMuted,
    },
    textCol: {
      flex: 1,
    },
    label: {
      fontSize: 15,
      fontWeight: '600',
      color: theme.text,
    },
    subtitle: {
      fontSize: 12,
      color: theme.textMuted,
      marginTop: 2,
    },
    error: {
      color: theme.danger,
    },
    value: {
      fontSize: 13,
      color: theme.textMuted,
    },
    optionRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 10,
      paddingLeft: 36,
      paddingRight: 16,
      paddingVertical: 12,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
      backgroundColor: theme.surface,
    },
    check: {
      width: 14,
      fontSize: 14,
      fontWeight: '700',
    },
    optionLabel: {
      fontSize: 14,
      color: theme.text,
    },
    optionActive: {
      fontWeight: '600',
    },
  });
}
