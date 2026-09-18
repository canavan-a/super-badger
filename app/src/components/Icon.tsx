import React from 'react';
import {
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Copy,
  GripVertical,
  LucideIcon,
  Menu,
  MoreVertical,
  Send,
  Settings,
  Square,
  Trash2,
  X,
} from 'lucide-react-native';

// lucide-react-native is the icon set used everywhere in the app — one
// standard, widely-used library instead of hand-drawn/emoji glyphs, so
// every icon is drawn the same way and comes from the same visual language.
// It renders through react-native-svg, whose web resolution is fixed in
// vite.config.ts (aliased to its real DOM-based web build) — see that
// alias's comment for why the default resolution silently produced
// invisible icons on the web build.
export type IconName =
  | 'menu'
  | 'kebab'
  | 'gear'
  | 'chevron-left'
  | 'chevron-right'
  | 'chevron-down'
  | 'trash'
  | 'close'
  | 'check'
  | 'drag-handle'
  | 'copy'
  | 'send'
  | 'stop';

const ICONS: Record<IconName, LucideIcon> = {
  menu: Menu,
  kebab: MoreVertical,
  gear: Settings,
  'chevron-left': ChevronLeft,
  'chevron-right': ChevronRight,
  'chevron-down': ChevronDown,
  trash: Trash2,
  close: X,
  check: Check,
  'drag-handle': GripVertical,
  copy: Copy,
  send: Send,
  stop: Square,
};

// Icons that read better solid than outlined (a hollow square doesn't read
// as clearly as "stop" as a filled one does).
const FILLED: Partial<Record<IconName, boolean>> = {stop: true};

export function Icon({
  name,
  size = 20,
  color = '#000',
  strokeWidth = 2,
}: {
  name: IconName;
  size?: number;
  color?: string;
  strokeWidth?: number;
}): React.JSX.Element {
  const IconComponent = ICONS[name];
  return (
    <IconComponent
      size={size}
      color={color}
      strokeWidth={strokeWidth}
      fill={FILLED[name] ? color : 'none'}
    />
  );
}
