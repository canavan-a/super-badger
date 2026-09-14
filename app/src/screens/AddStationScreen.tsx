import React, {useEffect, useMemo, useState} from 'react';
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';

import {
  createStation,
  listProviders,
  OpencodeProvider,
  testProviderConnection,
} from '../api';
import {Route} from '../routes';
import {Theme, useTheme} from '../theme';

type TestState = {status: 'idle'} | {status: 'testing'} | {status: 'ok'} | {status: 'error'; message: string};

export function AddStationScreen({
  onNavigate,
}: {
  onNavigate: (route: Route) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [name, setName] = useState('');
  const [directory, setDirectory] = useState('');

  const [providers, setProviders] = useState<OpencodeProvider[]>([]);
  const [providersError, setProvidersError] = useState<string | null>(null);
  const [loadingProviders, setLoadingProviders] = useState(true);
  const [providerId, setProviderId] = useState<string | null>(null);
  const [modelId, setModelId] = useState<string | null>(null);

  const [testState, setTestState] = useState<TestState>({status: 'idle'});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listProviders()
      .then(res => setProviders(res.providers ?? []))
      .catch(err => setProvidersError(String(err)))
      .finally(() => setLoadingProviders(false));
  }, []);

  const selectedProvider = useMemo(
    () => providers.find(p => p.id === providerId) ?? null,
    [providers, providerId],
  );
  const models = useMemo(
    () => (selectedProvider ? Object.keys(selectedProvider.models ?? {}) : []),
    [selectedProvider],
  );

  const selectProvider = (id: string) => {
    setProviderId(id);
    setModelId(null);
    setTestState({status: 'idle'});
  };

  const runTest = async () => {
    const baseURL = selectedProvider?.options?.baseURL;
    if (!baseURL) {
      setTestState({status: 'error', message: 'This provider has no baseURL to test.'});
      return;
    }
    setTestState({status: 'testing'});
    try {
      const res = await testProviderConnection({
        base_url: baseURL,
        api_key: selectedProvider?.options?.apiKey,
      });
      setTestState(res.ok ? {status: 'ok'} : {status: 'error', message: res.error ?? 'unreachable'});
    } catch (err) {
      setTestState({status: 'error', message: String(err)});
    }
  };

  const canSave = name.trim() && providerId && modelId;

  const submit = async () => {
    if (!providerId || !modelId) {
      return;
    }
    setError(null);
    setSaving(true);
    try {
      const station = await createStation({
        name: name.trim(),
        provider_id: providerId,
        model_id: modelId,
        directory: directory.trim() || undefined,
      });
      onNavigate({name: 'stationDetail', id: station.id});
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <ScrollView style={styles.scroll} contentContainerStyle={styles.container}>
      <Text style={styles.title}>Add Station</Text>

      <Field label="Name" value={name} onChangeText={setName} placeholder="gpu-a" />

      <View style={styles.field}>
        <Text style={styles.label}>Provider</Text>
        {loadingProviders && <ActivityIndicator style={styles.inlineSpinner} color={theme.text} />}
        {providersError && <Text style={styles.error}>{providersError}</Text>}
        <View style={styles.chipRow}>
          {providers.map(p => (
            <Pressable
              key={p.id}
              style={[styles.chip, providerId === p.id && styles.chipSelected]}
              onPress={() => selectProvider(p.id)}>
              <Text style={[styles.chipText, providerId === p.id && styles.chipTextSelected]}>
                {p.id}
              </Text>
            </Pressable>
          ))}
        </View>
      </View>

      {selectedProvider && (
        <View style={styles.field}>
          <Text style={styles.label}>Model</Text>
          <View style={styles.chipRow}>
            {models.map(id => (
              <Pressable
                key={id}
                style={[styles.chip, modelId === id && styles.chipSelected]}
                onPress={() => setModelId(id)}>
                <Text style={[styles.chipText, modelId === id && styles.chipTextSelected]}>
                  {id}
                </Text>
              </Pressable>
            ))}
          </View>
        </View>
      )}

      {selectedProvider && (
        <View style={styles.field}>
          <Pressable
            style={styles.testButton}
            onPress={runTest}
            disabled={testState.status === 'testing'}>
            {testState.status === 'testing' ? (
              <ActivityIndicator color={theme.text} />
            ) : (
              <Text style={styles.testButtonText}>Test Connection</Text>
            )}
          </Pressable>
          {testState.status === 'ok' && <Text style={styles.testOk}>✓ Connected</Text>}
          {testState.status === 'error' && (
            <Text style={styles.testError}>✗ {testState.message}</Text>
          )}
          {selectedProvider.options?.baseURL && (
            <Text style={styles.hint}>{selectedProvider.options.baseURL}</Text>
          )}
        </View>
      )}

      <Field
        label="Directory (optional)"
        value={directory}
        onChangeText={setDirectory}
        placeholder="/home/user/project"
      />

      {error && <Text style={styles.error}>{error}</Text>}

      <Pressable
        style={[styles.button, !canSave && styles.buttonDisabled]}
        disabled={!canSave || saving}
        onPress={submit}>
        {saving ? (
          <ActivityIndicator color={theme.primaryText} />
        ) : (
          <Text style={styles.buttonText}>Create Station</Text>
        )}
      </Pressable>
    </ScrollView>
  );
}

function Field({
  label,
  value,
  onChangeText,
  placeholder,
}: {
  label: string;
  value: string;
  onChangeText: (v: string) => void;
  placeholder?: string;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);
  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      <TextInput
        style={styles.input}
        value={value}
        onChangeText={onChangeText}
        placeholder={placeholder}
        placeholderTextColor={theme.textMuted}
        autoCapitalize="none"
        autoCorrect={false}
      />
    </View>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    scroll: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    container: {
      padding: 20,
      maxWidth: 480,
    },
    title: {
      fontSize: 20,
      fontWeight: '700',
      marginBottom: 20,
      color: theme.text,
    },
    field: {
      marginBottom: 16,
    },
    label: {
      fontSize: 13,
      fontWeight: '600',
      marginBottom: 6,
      color: theme.textMuted,
    },
    input: {
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 8,
      fontSize: 15,
      color: theme.text,
      backgroundColor: theme.surface,
    },
    inlineSpinner: {
      marginBottom: 6,
    },
    chipRow: {
      flexDirection: 'row',
      flexWrap: 'wrap',
      gap: 8,
    },
    chip: {
      paddingHorizontal: 12,
      paddingVertical: 6,
      borderRadius: 16,
      borderWidth: 1,
      borderColor: theme.border,
      backgroundColor: theme.surface,
    },
    chipSelected: {
      backgroundColor: theme.primary,
      borderColor: theme.primary,
    },
    chipText: {
      fontSize: 13,
      color: theme.text,
    },
    chipTextSelected: {
      color: theme.primaryText,
      fontWeight: '600',
    },
    testButton: {
      alignSelf: 'flex-start',
      paddingHorizontal: 14,
      paddingVertical: 8,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    testButtonText: {
      fontWeight: '600',
      fontSize: 13,
      color: theme.text,
    },
    testOk: {
      color: theme.success,
      marginTop: 6,
      fontSize: 13,
    },
    testError: {
      color: theme.danger,
      marginTop: 6,
      fontSize: 13,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
      marginTop: 6,
    },
    error: {
      color: theme.danger,
      marginBottom: 12,
    },
    button: {
      backgroundColor: theme.primary,
      borderRadius: 8,
      paddingVertical: 12,
      alignItems: 'center',
      marginTop: 8,
    },
    buttonDisabled: {
      opacity: 0.5,
    },
    buttonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
  });
}
