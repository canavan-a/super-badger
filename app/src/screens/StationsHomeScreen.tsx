import React from 'react';
import {ActivityIndicator, FlatList, Pressable, StyleSheet, Text, View} from 'react-native';

import {Station} from '../api';
import {StationCard} from '../components/StationCard';
import {Route} from '../routes';
import {Theme, useTheme} from '../theme';

// The home route used to be a static "pick from the menu" placeholder — the
// list lived only in the drawer, so seeing what stations exist meant opening
// the hamburger every time. This shows the same data (already fetched once
// in App.tsx) directly on the page a user lands on first.
export function StationsHomeScreen({
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
      <FlatList
        data={stations}
        keyExtractor={s => String(s.id)}
        contentContainerStyle={styles.list}
        renderItem={({item}) => (
          <StationCard station={item} onPress={() => onNavigate({name: 'stationDetail', id: item.id})} />
        )}
        ListEmptyComponent={
          loading ? (
            <ActivityIndicator style={styles.spinner} color={theme.text} />
          ) : (
            <View style={styles.empty}>
              <Text style={styles.emptyText}>No stations yet.</Text>
              <Pressable style={styles.addButton} onPress={() => onNavigate({name: 'addStation'})}>
                <Text style={styles.addButtonText}>+ Add Station</Text>
              </Pressable>
            </View>
          )
        }
      />

      {stations.length > 0 && (
        <Pressable style={styles.fab} onPress={() => onNavigate({name: 'addStation'})}>
          <Text style={styles.fabText}>+ Add Station</Text>
        </Pressable>
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
    list: {
      padding: 16,
      gap: 10,
    },
    spinner: {
      marginTop: 40,
    },
    empty: {
      alignItems: 'center',
      marginTop: 40,
      gap: 16,
    },
    emptyText: {
      color: theme.textMuted,
    },
    addButton: {
      paddingHorizontal: 20,
      paddingVertical: 12,
      borderRadius: 8,
      backgroundColor: theme.primary,
    },
    addButtonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
    fab: {
      position: 'absolute',
      right: 20,
      bottom: 20,
      paddingHorizontal: 18,
      paddingVertical: 12,
      borderRadius: 24,
      backgroundColor: theme.primary,
      shadowColor: '#000',
      shadowOpacity: 0.2,
      shadowRadius: 6,
      shadowOffset: {width: 0, height: 2},
      elevation: 3,
    },
    fabText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
  });
}
