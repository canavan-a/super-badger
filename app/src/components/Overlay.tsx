import React, {useEffect} from 'react';
import {BackHandler, Pressable, StyleSheet, View, ViewStyle} from 'react-native';

/**
 * A lightweight modal layer drawn inside the container it's written in (the
 * same one super-chat uses for its + menu): a tap-to-dismiss backdrop with
 * the content laid out over it, closed by Android back too.
 */
export function Overlay({
  visible,
  onClose,
  backdrop = 'rgba(0,0,0,0.35)',
  style,
  children,
}: {
  visible: boolean;
  onClose: () => void;
  backdrop?: string;
  /** Layout of the content area (e.g. justifyContent: 'flex-end'). */
  style?: ViewStyle;
  children: React.ReactNode;
}): React.JSX.Element | null {
  useEffect(() => {
    if (!visible) return;
    const sub = BackHandler.addEventListener('hardwareBackPress', () => {
      onClose();
      return true;
    });
    return () => sub.remove();
  }, [visible, onClose]);
  if (!visible) return null;
  return (
    <View style={[StyleSheet.absoluteFill, styles.layer]}>
      <Pressable style={[StyleSheet.absoluteFill, {backgroundColor: backdrop}]} onPress={onClose} />
      <View style={[styles.content, style]} pointerEvents="box-none">
        {children}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  layer: {zIndex: 50, elevation: 50},
  content: {flex: 1},
});
