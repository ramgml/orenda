/**
 * Shared, cross-feature constants.
 *
 * `INBOX_PROJECT_ID` is the system-managed workspace that calendar
 * events and orphan tasks route into. Archiving or deleting it would
 * orphan those tasks, so the value is treated as a magic constant
 * across the UI.
 */
export const INBOX_PROJECT_ID = '00000000-0000-0000-0000-00000000cafe'
