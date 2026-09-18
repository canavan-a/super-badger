import React from 'react';
import {ActivityIndicator, FlatList, Pressable, StyleSheet, Text, View} from 'react-native';

import {Station} from '../api';
import {Route} from '../routes';
import {Theme, useTheme} from '../theme';

export function StationsDrawerContent({
  stations,
  loading,
  onNavigate,
}: {
  stations: Station[];
  loading: boolean;
  onNavigate: (route: Route) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  return (
    <View style={styles.container}>
      <Pressable style={styles.header} onPress={() => onNavigate({name: 'stations'})}>
        <Text style={styles.title}>Stations</Text>
      </Pressable>

      <FlatList
        style={styles.list}
        data={stations}
        keyExtractor={s => String(s.id)}
        renderItem={({item}) => (
          <Pressable
            style={styles.row}
            onPress={() => onNavigate({name: 'stationDetail', id: item.id})}>
            <Text style={styles.rowTitle}>{item.name}</Text>
            <View style={styles.rowStatusRow}>
              <View
                style={[styles.rowDot, {backgroundColor: item.reachable ? theme.success : theme.danger}]}
              />
              <Text style={styles.rowSubtitle}>
                {item.reachable ? item.status : 'unreachable'}
              </Text>
            </View>
          </Pressable>
        )}
        ListEmptyComponent={
          loading ? (
            <ActivityIndicator style={styles.loadingSpinner} color={theme.text} />
          ) : (
            <Text style={styles.empty}>No stations yet.</Text>
          )
        }
      />

      <Pressable
        style={styles.addButton}
        onPress={() => onNavigate({name: 'addStation'})}>
        <Text style={styles.addButtonText}>+ Add Station</Text>
      </Pressable>

      <Pressable
        style={styles.settingsButton}
        onPress={() => onNavigate({name: 'settings'})}>
        <Text style={styles.settingsIcon}>⚙</Text>
        <Text style={styles.settingsText}>Settings</Text>
      </Pressable>
    </View>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    header: {
      paddingHorizontal: 16,
      paddingTop: 20,
      paddingBottom: 12,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
    },
    title: {
      fontSize: 18,
      fontWeight: '700',
      color: theme.text,
    },
    list: {
      flex: 1,
    },
    row: {
      paddingHorizontal: 16,
      paddingVertical: 12,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
    },
    rowTitle: {
      fontSize: 15,
      fontWeight: '600',
      color: theme.text,
    },
    rowStatusRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 6,
      marginTop: 2,
    },
    rowDot: {
      width: 6,
      height: 6,
      borderRadius: 3,
    },
    rowSubtitle: {
      fontSize: 12,
      color: theme.textMuted,
    },
    empty: {
      padding: 16,
      color: theme.textMuted,
    },
    loadingSpinner: {
      padding: 24,
    },
    addButton: {
      margin: 12,
      paddingVertical: 12,
      borderRadius: 8,
      backgroundColor: theme.primary,
      alignItems: 'center',
    },
    addButtonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
    settingsButton: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 8,
      paddingHorizontal: 16,
      paddingVertical: 14,
      borderTopWidth: 1,
      borderTopColor: theme.border,
    },
    settingsIcon: {
      fontSize: 16,
      color: theme.text,
    },
    settingsText: {
      fontSize: 14,
      fontWeight: '600',
      color: theme.text,
    },
  });
}
