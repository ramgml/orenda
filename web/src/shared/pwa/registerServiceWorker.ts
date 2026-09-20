/**
 * App-managed service worker registration and update flow (Task 351).
 *
 * Root cause of the stale-bundle bug (QA T348): the generated worker was
 * registered by the plugin's silent `registerSW.js` index.html inject —
 * a script that only calls `navigator.serviceWorker.register()` and
 * never drives the update lifecycle. After a deploy the freshly
 * precached bundle sat in an installing/waiting worker while the page
 * kept running the old controller's assets; nobody sent SKIP_WAITING
 * and nobody reacted to the takeover, so the update never reached the
 * user — only manual DevTools cleanup (unregister + caches.delete) did.
 *
 * The fix: register via the plugin's supported client helper
 * (`virtual:pwa-register`) from app code. With `registerType:
 * 'autoUpdate'` (vite.config.ts) this drives the whole documented loop:
 *   - the generated worker self-activates (workbox skipWaiting +
 *     clientsClaim are baked in at build time while `injectRegister`
 *     stays at its 'auto' default),
 *   - the helper sends SKIP_WAITING to an update as soon as it is
 *     waiting (un-sticks exactly the state QA observed) and reloads
 *     the page once when the new worker activates, so the next paint
 *     comes from the new precache — deploy → next reload → new bundle,
 *     no manual action.
 *
 * Importing this module (via main.tsx) is what makes the plugin skip
 * the silent index.html script (`useImportRegister`), so this is the
 * single registration path.
 *
 * Failure containment: unsupported browsers and insecure contexts
 * (plain-http non-localhost previews) get a no-op — no SW means no
 * precache to go stale. Registration errors are logged and swallowed:
 * a broken SW must never break app boot.
 */
import { registerSW } from 'virtual:pwa-register';

/**
 * Install the app-managed SW registration. Safe to call unconditionally
 * from main.tsx; returns true only when the update loop was wired
 * (no-op on unsupported browsers / insecure contexts).
 */
export function registerServiceWorker(): boolean {
  if (typeof window === 'undefined') return false;
  if (!('serviceWorker' in navigator)) return false;
  // SW needs https or localhost; http LAN previews must stay SW-free.
  if (!window.isSecureContext) return false;

  void registerSW({
    immediate: true,
    onRegisterError(err: unknown) {
      console.warn('[pwa] service worker registration failed:', err);
    },
  });
  return true;
}
