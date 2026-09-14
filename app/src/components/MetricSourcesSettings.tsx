import React, {useEffect, useState} from 'react';
import {ActivityIndicator, Pressable, StyleSheet, Switch, Text, TextInput, View} from 'react-native';

import {createMetricSource, deleteMetricSource, listMetricSources, MetricSource, updateMetricSource} from '../api';
import {Theme, useTheme} from '../theme';

// Server-wide config for the Super Badger Station Standard API: each row is
// one external endpoint the server polls on its own interval (see
// server/metrics.Manager) and upserts into every matched station's data
// points. Not per-station — a given source can cover any number of stations
// at once (matched by id or name in its response body).
export function MetricSourcesSettings(): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [sources, setSources] = useState<MetricSource[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [pollSeconds, setPollSeconds] = useState('30');
  const [adding, setAdding] = useState(false);

  const refresh = () => {
    listMetricSources()
      .then(setSources)
      .catch(err => setError(String(err)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  const addSource = async () => {
    if (!name.trim() || !url.trim()) return;
    setAdding(true);
    try {
      await createMetricSource({
        name: name.trim(),
        url: url.trim(),
        api_key: apiKey.trim() || undefined,
        poll_interval_seconds: parseInt(pollSeconds, 10) || 30,
      });
      setName('');
      setUrl('');
      setApiKey('');
      setPollSeconds('30');
      refresh();
    } catch (err) {
      setError(String(err));
    } finally {
      setAdding(false);
    }
  };

  const toggleEnabled = async (source: MetricSource, enabled: boolean) => {
    setSources(prev => prev.map(s => (s.id === source.id ? {...s, enabled} : s)));
    try {
      await updateMetricSource(source.id, {
        name: source.name,
        url: source.url,
        api_key: source.api_key ?? undefined,
        poll_interval_seconds: source.poll_interval_seconds,
        enabled,
      });
    } catch (err) {
      setError(String(err));
    }
  };

  const remove = async (source: MetricSource) => {
    try {
      await deleteMetricSource(source.id);
      refresh();
    } catch (err) {
      setError(String(err));
    }
  };

  if (loading) {
    return <ActivityIndicator color={theme.text} />;
  }

  return (
    <View>
      {error && <Text style={styles.error}>{error}</Text>}
      {sources.map(source => (
        <View key={source.id} style={styles.row}>
          <View style={styles.rowText}>
            <Text style={styles.label}>{source.name}</Text>
            <Text style={styles.hint} numberOfLines={1}>
              {source.url} · every {source.poll_interval_seconds}s
            </Text>
          </View>
          <Switch value={source.enabled} onValueChange={v => toggleEnabled(source, v)} />
          <Pressable style={styles.removeButton} onPress={() => remove(source)}>
            <Text style={styles.removeButtonText}>Remove</Text>
          </Pressable>
        </View>
      ))}

      <View style={styles.field}>
        <Text style={styles.label}>Name</Text>
        <TextInput style={styles.input} value={name} onChangeText={setName} placeholder="wrx80" />
      </View>
      <View style={styles.field}>
        <Text style={styles.label}>URL</Text>
        <TextInput
          style={styles.input}
          value={url}
          onChangeText={setUrl}
          placeholder="http://wrx80.local:9998"
          autoCapitalize="none"
          autoCorrect={false}
        />
      </View>
      <View style={styles.field}>
        <Text style={styles.label}>API key (optional)</Text>
        <TextInput
          style={styles.input}
          value={apiKey}
          onChangeText={setApiKey}
          secureTextEntry
          autoCapitalize="none"
          autoCorrect={false}
        />
      </View>
      <View style={styles.field}>
        <Text style={styles.label}>Poll interval (seconds)</Text>
        <TextInput style={styles.input} value={pollSeconds} onChangeText={setPollSeconds} keyboardType="numeric" />
      </View>
      <Pressable style={styles.button} onPress={addSource} disabled={adding}>
        <Text style={styles.buttonText}>{adding ? 'Adding…' : 'Add source'}</Text>
      </Pressable>
    </View>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    error: {
      color: theme.danger,
      marginBottom: 12,
    },
    row: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 10,
      marginBottom: 12,
      padding: 10,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 8,
      backgroundColor: theme.surface,
    },
    rowText: {
      flex: 1,
      minWidth: 0,
      gap: 2,
    },
    label: {
      fontSize: 13,
      fontWeight: '600',
      color: theme.text,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
    },
    removeButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    removeButtonText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.danger,
    },
    field: {
      marginBottom: 12,
    },
    input: {
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 8,
      fontSize: 14,
      color: theme.text,
      backgroundColor: theme.surface,
    },
    button: {
      backgroundColor: theme.primary,
      borderRadius: 8,
      paddingVertical: 12,
      alignItems: 'center',
    },
    buttonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
  });
}
