"use client";

import {
  createContext,
  useContext,
  useEffect,
  useCallback,
  useSyncExternalStore,
} from "react";

export type Theme = "light" | "dark" | "system";

type ThemeContextType = {
  theme: Theme;
  setTheme: (t: Theme) => void;
};

const ThemeContext = createContext<ThemeContextType>({
  theme: "system",
  setTheme: () => {},
});

export function useTheme() {
  return useContext(ThemeContext);
}

// Native `storage` events fire on other tabs only; we also dispatch a
// custom event so writes in the current tab notify subscribers.
const THEME_CHANGED = "e2a:theme-changed";

function subscribeToTheme(callback: () => void) {
  window.addEventListener("storage", callback);
  window.addEventListener(THEME_CHANGED, callback);
  return () => {
    window.removeEventListener("storage", callback);
    window.removeEventListener(THEME_CHANGED, callback);
  };
}

function readStoredTheme(): Theme {
  const stored = window.localStorage.getItem("theme");
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : "system";
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  // localStorage is the source of truth — mirror it into React via
  // useSyncExternalStore. The server snapshot is "system" to match
  // pre-hydration HTML; the real value loads on the client first paint.
  const theme = useSyncExternalStore<Theme>(
    subscribeToTheme,
    readStoredTheme,
    () => "system",
  );

  const applyTheme = useCallback((t: Theme) => {
    const root = document.documentElement;
    if (t === "system") {
      const isDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
      root.classList.toggle("dark", isDark);
    } else {
      root.classList.toggle("dark", t === "dark");
    }
  }, []);

  useEffect(() => {
    applyTheme(theme);

    if (theme === "system") {
      const mq = window.matchMedia("(prefers-color-scheme: dark)");
      const handler = () => applyTheme("system");
      mq.addEventListener("change", handler);
      return () => mq.removeEventListener("change", handler);
    }
  }, [theme, applyTheme]);

  const setTheme = useCallback((t: Theme) => {
    localStorage.setItem("theme", t);
    window.dispatchEvent(new Event(THEME_CHANGED));
  }, []);

  return (
    <ThemeContext.Provider value={{ theme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}
