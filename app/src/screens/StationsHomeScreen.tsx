import React, {useState} from 'react';
import {ActivityIndicator, FlatList, Pressable, StyleSheet, Text, View} from 'react-native';

import {Command, Station} from '../api';
import {Icon} from '../components/Icon';
import {Overlay} from '../components/Overlay';
import {PixelField} from '../components/PixelField';
import {StationCard} from '../components/StationCard';
import {Route} from '../routes';
import {Theme, useTheme} from '../theme';

// The main screen: every station, over the same still pixel constellation
// super-chat uses, and a floating + that offers a new station or the
// commands screen. (It replaced the slide-out drawer, which used to be the
// only way to reach those.)
export function StationsHomeScreen({
  stations,
  commands,
  loading,
  onNavigate,
}: {
  stations: Station[];
  commands: Command[];
  loading: boolean;
  onNavigate: (route: Route) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);
  const [addOpen, setAddOpen] = useState(false);

  const go = (route: Route) => {
    setAddOpen(false);
    onNavigate(route);
  };

  return (
    <View style={styles.container}>
      <PixelField />
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
              <Text style={styles.emptyTitle}>No stations yet</Text>
              <Text style={styles.emptyText}>Tap + to add one.</Text>
            </View>
          )
        }
      />

      <Pressable
        style={({pressed}) => [styles.fab, pressed && styles.fabPressed]}
        onPress={() => setAddOpen(true)}
        accessibilityRole="button"
        accessibilityLabel="New station or commands">
        <Icon name="plus" size={26} color={theme.primaryText} />
      </Pressable>

      <Overlay visible={addOpen} onClose={() => setAddOpen(false)} style={styles.sheetBackdrop}>
        <View style={styles.sheet}>
          <Pressable style={styles.sheetRow} onPress={() => go({name: 'addStation'})}>
            <Icon name="plus" size={18} color={theme.text} />
            <Text style={styles.sheetText}>New station</Text>
          </Pressable>
          {/* Only when the metric endpoints actually advertise commands, so
              it never opens an empty screen. */}
          {commands.length > 0 && (
            <Pressable style={styles.sheetRow} onPress={() => go({name: 'commands'})}>
              <Icon name="terminal" size={18} color={theme.text} />
              <Text style={styles.sheetText}>Commands</Text>
            </Pressable>
          )}
        </View>
      </Overlay>
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
      // Room for the + button under the last card.
      paddingBottom: 96,
    },
    spinner: {
      marginTop: 40,
    },
    empty: {
      alignItems: 'center',
      marginTop: 40,
      gap: 10,
    },
    emptyTitle: {
      fontSize: 18,
      fontWeight: '800',
      color: theme.text,
    },
    emptyText: {
      color: theme.textMuted,
    },
    fab: {
      position: 'absolute',
      right: 20,
      bottom: 24,
      width: 58,
      height: 58,
      borderRadius: 29,
      backgroundColor: theme.primary,
      alignItems: 'center',
      justifyContent: 'center',
      elevation: 6,
      shadowColor: '#000',
      shadowOpacity: 0.25,
      shadowRadius: 6,
      shadowOffset: {width: 0, height: 3},
    },
    fabPressed: {
      opacity: 0.85,
    },
    // The menu opens just above the + button.
    sheetBackdrop: {
      justifyContent: 'flex-end',
      alignItems: 'flex-end',
      paddingRight: 20,
      paddingBottom: 92,
    },
    sheet: {
      minWidth: 200,
      borderRadius: 12,
      paddingVertical: 6,
      backgroundColor: theme.surface,
      borderWidth: 1,
      borderColor: theme.border,
    },
    sheetRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 12,
      paddingHorizontal: 16,
      paddingVertical: 14,
    },
    sheetText: {
      fontSize: 15,
      fontWeight: '600',
      color: theme.text,
    },
  });
}
