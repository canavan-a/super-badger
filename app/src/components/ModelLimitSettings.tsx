import React, {useCallback, useEffect, useState} from 'react';
import {ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View} from 'react-native';

import {formatTokens, getLimits, ModelLimitInfo, setLimit} from '../api';
import {Theme, useTheme} from '../theme';

const SOURCE_LABELS: Record<ModelLimitInfo['source'], string> = {
  override: 'your override',
  detected: 'detected from the engine',
  default: "opencode's default",
};

// Blank means "no override"; anything else must be a whole number.
function parseLimit(draft: string): number | null | 'invalid' {
  const s = draft.trim().replace(/[,_\s]/g, '');
  if (!s) return null;
  if (!/^\d+$/.test(s)) return 'invalid';
  return Number(s);
}

// The model's effective context window (what opencode compacts against) and
// where it comes from, with an override. Limits are per provider/model, not
// per station: an override here applies to every station on that model, and
// opencode picks it up live within ~10s (see server/api/limits.go).
export function ModelLimitSettings({
  providerId,
  modelId,
}: {
  providerId: string;
  modelId: string;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [info, setInfo] = useState<ModelLimitInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [contextDraft, setContextDraft] = useState('');
  const [outputDraft, setOutputDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  const load = useCallback(() => {
    getLimits(providerId, modelId)
      .then(rows => {
        const row = rows.find(r => r.provider_id === providerId && r.model_id === modelId) ?? null;
        setInfo(row);
        setContextDraft(row?.override?.context != null ? String(row.override.context) : '');
        setOutputDraft(row?.override?.output != null ? String(row.override.output) : '');
      })
      .catch(err => setError(String(err)))
      .finally(() => setLoading(false));
  }, [providerId, modelId]);

  useEffect(() => {
    setLoading(true);
    setInfo(null);
    setError(null);
    setSaved(false);
    load();
  }, [load]);

  const write = async (context: number | null, output: number | null) => {
    setError(null);
    setSaved(false);
    setSaving(true);
    try {
      await setLimit({provider_id: providerId, model_id: modelId, context, output});
      setSaved(true);
      load();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const save = () => {
    const context = parseLimit(contextDraft);
    const output = parseLimit(outputDraft);
    if (context === 'invalid' || output === 'invalid') {
      setError('Limits must be whole numbers of tokens.');
      return;
    }
    write(context, output);
  };

  const hasOverride = !!info?.override && (info.override.context != null || info.override.output != null);

  if (loading) {
    return <ActivityIndicator color={theme.text} />;
  }

  return (
    <View style={styles.container}>
      {info ? (
        <>
          <View style={styles.rowBetween}>
            <Text style={styles.subLabel}>Context window</Text>
            <Text style={styles.value}>{formatTokens(info.context)} tokens</Text>
          </View>
          <Text style={styles.hint}>
            From {SOURCE_LABELS[info.source]}
            {info.detected > 0 && info.source !== 'detected' ? ` · engine reports ${formatTokens(info.detected)}` : ''}
            {info.output > 0 ? ` · max output ${formatTokens(info.output)}` : ''}
          </Text>
        </>
      ) : (
        <Text style={styles.hint}>opencode doesn't list this model's limits.</Text>
      )}

      <View style={styles.rowBetween}>
        <Text style={styles.subLabel}>Context override</Text>
        <TextInput
          style={styles.input}
          value={contextDraft}
          onChangeText={setContextDraft}
          placeholder={info ? String(info.detected || info.context || '') : 'tokens'}
          placeholderTextColor={theme.textMuted}
          keyboardType="number-pad"
        />
      </View>
      <View style={styles.rowBetween}>
        <Text style={styles.subLabel}>Output override (optional)</Text>
        <TextInput
          style={styles.input}
          value={outputDraft}
          onChangeText={setOutputDraft}
          placeholder={info?.output ? String(info.output) : 'tokens'}
          placeholderTextColor={theme.textMuted}
          keyboardType="number-pad"
        />
      </View>

      <View style={styles.actions}>
        {saving ? <ActivityIndicator color={theme.text} /> : null}
        {hasOverride ? (
          <Pressable style={styles.clearButton} disabled={saving} onPress={() => write(null, null)}>
            <Text style={styles.clearButtonText}>Clear override</Text>
          </Pressable>
        ) : null}
        <Pressable style={styles.saveButton} disabled={saving} onPress={save}>
          <Text style={styles.saveButtonText}>Save</Text>
        </Pressable>
      </View>

      {error ? <Text style={styles.error}>{error}</Text> : null}
      {saved && !error ? <Text style={styles.hint}>Saved — opencode applies it within about 10 seconds.</Text> : null}
      <Text style={styles.hint}>
        Applies to every station on {providerId}/{modelId}, not just this one. Leave blank to use the detected or
        default limit.
      </Text>
    </View>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      gap: 8,
    },
    rowBetween: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: 12,
    },
    subLabel: {
      fontSize: 13,
      color: theme.text,
    },
    value: {
      fontSize: 13,
      fontWeight: '700',
      color: theme.text,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
    },
    error: {
      fontSize: 12,
      color: theme.danger,
    },
    input: {
      flex: 1,
      maxWidth: 140,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 6,
      fontSize: 13,
      color: theme.text,
      backgroundColor: theme.surface,
      textAlign: 'right',
    },
    actions: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'flex-end',
      gap: 8,
    },
    saveButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.primary,
    },
    saveButtonText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.primaryText,
    },
    clearButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    clearButtonText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.text,
    },
  });
}
