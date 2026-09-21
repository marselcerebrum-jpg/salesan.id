'use client';

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';

export type ThemeChoice = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'salesan.theme';

interface ThemeContextValue {
  /** What the operator picked, which may be "system". */
  choice: ThemeChoice;
  /** What is actually on screen right now. */
  resolved: 'light' | 'dark';
  setChoice: (choice: ThemeChoice) => void;
  toggle: () => void;
}

/**
 * Light until somebody says otherwise.
 *
 * The product is used in offices in daylight, and its screens are designed on
 * the light palette first; following the OS made a laptop set to dark for the
 * evening open the inbox dark at nine in the morning. "system" is still there
 * for whoever wants it, as a choice rather than a default.
 */
const DEFAULT_CHOICE: ThemeChoice = 'light';

const ThemeContext = createContext<ThemeContextValue>({
  choice: DEFAULT_CHOICE,
  resolved: 'light',
  setChoice: () => {},
  toggle: () => {},
});

/**
 * Inlined in <head> so the correct theme is painted on the very first frame.
 *
 * Without it the page renders light, then flips — a white flash straight into
 * the eyes of someone working in a dark room, which is exactly who dark mode
 * is for.
 */
export const themeBootstrapScript = `
(function () {
  try {
    var stored = localStorage.getItem('${STORAGE_KEY}');
    var dark = stored === 'dark' ||
      (stored === 'system' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches);
    if (dark) document.documentElement.setAttribute('data-theme', 'dark');
    else document.documentElement.removeAttribute('data-theme');
  } catch (e) {}
})();
`;

function systemPrefersDark() {
  return typeof window !== 'undefined' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function apply(choice: ThemeChoice): 'light' | 'dark' {
  const dark = choice === 'dark' || (choice === 'system' && systemPrefersDark());
  if (dark) document.documentElement.setAttribute('data-theme', 'dark');
  else document.documentElement.removeAttribute('data-theme');
  return dark ? 'dark' : 'light';
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [choice, setChoiceState] = useState<ThemeChoice>(DEFAULT_CHOICE);
  const [resolved, setResolved] = useState<'light' | 'dark'>('light');

  // Read the stored choice after mount: the bootstrap script already painted
  // the right theme, this only syncs React's copy of it.
  useEffect(() => {
    let stored: ThemeChoice = DEFAULT_CHOICE;
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (raw === 'light' || raw === 'dark' || raw === 'system') stored = raw;
    } catch {
      // Private browsing or blocked storage: the default stands.
    }
    setChoiceState(stored);
    setResolved(apply(stored));
  }, []);

  // Follow the OS while the choice is "system".
  useEffect(() => {
    if (choice !== 'system') return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => setResolved(apply('system'));
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, [choice]);

  const setChoice = useCallback((next: ThemeChoice) => {
    setChoiceState(next);
    setResolved(apply(next));
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Not persisting is survivable; the theme still applies for this session.
    }
  }, []);

  const toggle = useCallback(() => {
    setChoice(
      (document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark'),
    );
  }, [setChoice]);

  return (
    <ThemeContext.Provider value={{ choice, resolved, setChoice, toggle }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  return useContext(ThemeContext);
}
