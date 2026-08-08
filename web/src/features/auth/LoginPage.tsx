import { FormEvent, useState } from 'react'
import { Navigate, useLocation } from 'react-router-dom'

import { AxiosError } from '@/shared/api/client'
import { useAuth } from '@/features/auth/AuthContext'

/**
 * /login page — email + password form. Submits to POST /api/v1/auth/login.
 *
 * On success the AuthProvider updates state, the cookie is set by the
 * server, and we navigate to the originally-requested URL (or /).
 */
export function LoginPage(): JSX.Element {
  const { status, login } = useAuth()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (status === 'authenticated') {
    return <Navigate to={from} replace />
  }

  async function onSubmit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await login(email.trim(), password)
    } catch (err) {
      if (err instanceof AxiosError && err.response?.status === 401) {
        setError('Invalid email or password.')
      } else {
        setError('Login failed. Please try again.')
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="min-h-[60vh] flex items-center justify-center">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-6 shadow-sm"
      >
        <h1 className="text-xl font-semibold mb-1">Sign in to Orenda</h1>
        <p className="text-sm text-slate-500 mb-4">
          Local-first; only this device talks to the server.
        </p>

        {error && (
          <div className="mb-4 rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
            {error}
          </div>
        )}

        <label className="block text-sm font-medium mb-1" htmlFor="email">
          Email
        </label>
        <input
          id="email"
          type="email"
          required
          autoFocus
          autoComplete="username"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="w-full mb-3 px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
        />

        <label className="block text-sm font-medium mb-1" htmlFor="password">
          Password
        </label>
        <input
          id="password"
          type="password"
          required
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="w-full mb-4 px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
        />

        <button
          type="submit"
          disabled={submitting || !email || !password}
          className="w-full py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white font-medium"
        >
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>

        <p className="mt-4 text-xs text-slate-500">
          No account? Bootstrap one from the CLI:{' '}
          <code className="px-1 py-0.5 bg-slate-100 dark:bg-slate-800 rounded">
            orenda user create --email … --display-name …
          </code>
        </p>
      </form>
    </div>
  )
}