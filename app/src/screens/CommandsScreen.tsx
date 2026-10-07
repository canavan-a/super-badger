import React, {useCallback, useEffect, useState} from 'react';
import {ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View} from 'react-native';

import {Command, invokeCommand, listCommands} from '../api';
import {Theme, useTheme} from '../theme';

// CommandsScreen renders the command list the metric endpoints advertise
// (see docs/command-spec.md). Each row is a POST the server streams back —
// model toggles in particular can run for up to a minute, so the row shows
// a busy state for the duration and the list is refetched afterwards (a
// completed toggle often changes what the endpoint advertises next).
export function CommandsScreen(): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [commands, setCommands] = useState<Command[]>([]);
  const [runningPath, setRunningPath] = useState<string | null>(null);

  const refresh = useCallback(() => {
    listCommands().then(setCommands).catch(() => setCommands([]));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const run = (cmd: Command) => {
    if (runningPath !== null) return; // one command at a time
    setRunningPath(cmd.path);
    invokeCommand(cmd.station_id, cmd.path)
      .catch(() => {
        // A failed command is still "done" — surface it by refreshing; the
        // endpoint may have dropped it (or added it) as a side effect.
      })
      .finally(() => {
        setRunningPath(null);
        refresh();
      });
  };

  return (
    <ScrollView style={styles.container}>
      {commands.map((cmd, i) => {
        const running = runningPath === cmd.path;
        return (
          <Pressable
            key={`${cmd.station_id}:${cmd.path}`}
            style={[styles.row, running && styles.rowRunning]}
            onPress={() => run(cmd)}
            disabled={runningPath !== null}>
            {running ? (
              <ActivityIndicator size="small" color={theme.text} style={styles.dot} />
            ) : (
              <View style={[styles.dot, {backgroundColor: cmd.station_color}]} />
            )}
            <View style={styles.textCol}>
              <Text style={styles.label}>{cmd.label}</Text>
              <Text style={styles.subtitle}>
                {running ? 'running…' : cmd.station_name}
              </Text>
            </View>
          </Pressable>
        );
      })}
    </ScrollView>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: theme.bg,
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
    rowRunning: {
      backgroundColor: theme.surfaceAlt,
    },
    dot: {
      width: 10,
      height: 10,
      borderRadius: 5,
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
  });
}
