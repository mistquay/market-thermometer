import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

export type Theme = 'dark' | 'light'

type ThemeCtx = {
  theme: Theme
  setTheme: (t: Theme) => void
  toggleTheme: () => void
}

const STORAGE_KEY = 'mt-theme'

function initialTheme(): Theme {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // ignore storage errors
  }
  return 'dark'
}

const Ctx = createContext<ThemeCtx>({
  theme: 'dark',
  setTheme: () => {},
  toggleTheme: () => {},
})

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(initialTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
    try {
      localStorage.setItem(STORAGE_KEY, theme)
    } catch {
      // ignore
    }
  }, [theme])

  const setTheme = useCallback((t: Theme) => {
    document.documentElement.dataset.theme = t
    document.documentElement.style.colorScheme = t
    setThemeState(t)
  }, [])
  const toggleTheme = useCallback(() => {
    setThemeState((prev) => {
      const next = prev === 'dark' ? 'light' : 'dark'
      document.documentElement.dataset.theme = next
      document.documentElement.style.colorScheme = next
      return next
    })
  }, [])

  return <Ctx.Provider value={{ theme, setTheme, toggleTheme }}>{children}</Ctx.Provider>
}

export function useTheme() {
  return useContext(Ctx)
}
