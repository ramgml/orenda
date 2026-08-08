import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

import { api, type UserProfile } from '@/shared/api/client'

/**
 * Auth state and actions exposed via React context.
 *
 * The provider calls GET /api/v1/me on mount to determine whether the user
 * already has a valid cookie session. `login`/`logout` drive the same state.
 */
interface AuthContextValue {
  user: UserProfile | null
  status: 'loading' | 'authenticated' | 'anonymous'
  /** JWT from the most recent /auth/login; used by the WS client. */
  token: string | null
  login: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }): JSX.Element {
  const [user, setUser] = useState<UserProfile | null>(null)
  const [token, setToken] = useState<string | null>(null)
  const [status, setStatus] = useState<'loading' | 'authenticated' | 'anonymous'>('loading')

  const refresh = useCallback(async () => {
    try {
      const profile = await api.me()
      setUser(profile)
      setStatus('authenticated')
      // Cookie-only sessions have no JWT in memory; WS uses cookie via
      // the /auth/ws-token endpoint in Phase 3, so leaving token=null is
      // fine.
    } catch {
      setUser(null)
      setToken(null)
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
    setToken(resp.token)
    setStatus('authenticated')
  }, [])

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      setUser(null)
      setToken(null)
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
      setToken(null)
      setStatus('anonymous')
    })
  }, [])

  const value = useMemo<AuthContextValue>(
    () => ({ user, status, token, login, logout, refresh }),
    [user, status, token, login, logout, refresh],
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