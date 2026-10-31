import { resolvedTheme, setPreferences, usePreferences } from '../app/preferences';

// The sidebar's light/dark switch is a shortcut for the theme preference (see Settings › Preferences).
export function useTheme() {
  const prefs = usePreferences();
  return [resolvedTheme(prefs), theme => setPreferences({ theme })];
}
