import React from 'react';
import {
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  GripVertical,
  LucideIcon,
  Menu,
  MoreVertical,
  Settings,
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
  | 'drag-handle';

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
};

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
  return <IconComponent size={size} color={color} strokeWidth={strokeWidth} />;
}
