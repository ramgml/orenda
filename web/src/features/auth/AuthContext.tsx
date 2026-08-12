import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

import { api, type UserProfile } from '@/shared/api/client'

/**
 * Auth state and actions exposed via React context.
 *
 * The provider calls GET /api/v1/me on mount to determine whether the user
 * already has a valid cookie session. `login`/`logout` drive the same
 * state.
 *
 * Phase 27.2: there is no separate JWT to keep in memory — the session
 * lives in the HttpOnly `orenda_session` cookie that's sent automatically
 * on the WebSocket upgrade and on every API call. UI-side token storage
 * has been removed (it was never populated in the first place, see
 * SESSION.md 2026-08-12 audit).
 */
interface AuthContextValue {
  user: UserProfile | null
  status: 'loading' | 'authenticated' | 'anonymous'
  login: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }): JSX.Element {
  const [user, setUser] = useState<UserProfile | null>(null)
  const [status, setStatus] = useState<'loading' | 'authenticated' | 'anonymous'>('loading')

  const refresh = useCallback(async () => {
    try {
      const profile = await api.me()
      setUser(profile)
      setStatus('authenticated')
    } catch {
      setUser(null)
      setStatus('anonymous')
    }
  }, [])

  const login = useCallback(async (email: string, password: string) => {
    const resp = await api.login(email, password)
    setUser({
      user_id: resp.user_id,
      email: resp.email,
      display_name: resp.display_name,
      role: resp.role,
      scopes: resp.scopes,
    })
    // The session JWT is set by the server via Set-Cookie: orenda_session;
    // we don't (and can't — it's HttpOnly) keep it in JS memory.
    setStatus('authenticated')
  }, [])

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      setUser(null)
      setStatus('anonymous')
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  // Drop local state on any 401 anywhere in the app.
  useEffect(() => {
    api.onAuthFailure(() => {
      setUser(null)
      setStatus('anonymous')
    })
  }, [])

  const value = useMemo<AuthContextValue>(
    () => ({ user, status, login, logout, refresh }),
    [user, status, login, logout, refresh],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

/** useAuth returns the current AuthContextValue. Throws if no provider. */
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used inside <AuthProvider>')
  }
  return ctx
}
