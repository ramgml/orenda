import { useEffect, useState } from 'react';

/**
 * Dark/light theme toggle. Persists to localStorage; respects the
 * prefers-color-scheme media query on first visit.
 */
export function ThemeToggle(): JSX.Element {
  const [dark, setDark] = useState<boolean>(() => {
    try {
      const saved = localStorage.getItem('orenda.theme');
      if (saved === 'dark') return true;
      if (saved === 'light') return false;
    } catch {
      // storage blocked
    }
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
  });

  useEffect(() => {
    const root = document.documentElement;
    if (dark) {
      root.classList.add('dark');
    } else {
      root.classList.remove('dark');
    }
    try {
      localStorage.setItem('orenda.theme', dark ? 'dark' : 'light');
    } catch {
      // storage blocked
    }
  }, [dark]);

  return (
    <button
      type="button"
      onClick={() => setDark((v) => !v)}
      className="px-2 py-1 rounded text-xs border border-border"
      aria-label="Toggle theme"
      title="Toggle dark/light theme"
    >
      {dark ? '☾' : '☀'}
    </button>
  );
}
