import axios, { type AxiosInstance } from 'axios';

/**
 * Typed wrapper around axios for the Orenda REST API.
 *
 * Cookie-based auth is configured via `withCredentials: true`. A 401 response
 * triggers the onUnauthorized callback so the AuthProvider can drop state and
 * redirect to /login.
 *
 * The domain endpoint groups live in sibling modules (tasks.ts,
 * projects.ts, …) as `ThisType<ApiClient>` objects; client.ts merges
 * them onto the prototype, so `this.http` is the shared transport for
 * every domain method.
 */
export class ApiClient {
  /** Shared axios transport; domain modules reach it as `this.http`. */
  readonly http: AxiosInstance;
  private onUnauthorized: (() => void) | null = null;

  constructor(baseURL: string = '') {
    this.http = axios.create({
      baseURL,
      withCredentials: true,
      timeout: 15_000,
      headers: { 'Content-Type': 'application/json' },
    });
    this.http.interceptors.response.use(
      (r) => r,
      (err: unknown) => {
        if (axios.isAxiosError(err) && err.response?.status === 401) {
          this.onUnauthorized?.();
        }
        return Promise.reject(err);
      },
    );
  }

  /** Register a callback invoked on every 401 response. */
  onAuthFailure(cb: () => void): void {
    this.onUnauthorized = cb;
  }
}
