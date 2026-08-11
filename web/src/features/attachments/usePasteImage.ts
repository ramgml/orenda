import { useEffect } from 'react'

/**
 * Listens for a paste of image data anywhere on the page and hands the
 * resulting File off to `onImage`. Ignores pastes while focus is inside
 * an editable element so it doesn't hijack Ctrl+V in comment boxes or
 * any other text input.
 *
 * File name defaults to `screenshot-YYYY-MM-DD-HHMMSS.png` (or the
 * clipboard's filename if it carries one).
 *
 * Caller is responsible for actually uploading the file — this hook only
 * extracts the image from the event.
 */
export function usePasteImage(onImage: (file: File) => void | Promise<void>): void {
  useEffect(() => {
    function onPaste(e: ClipboardEvent): void {
      const target = e.target as HTMLElement | null
      // Don't hijack paste inside any editable surface: the user is
      // pasting text into a comment, a search box, etc.
      if (target) {
        const tag = target.tagName
        if (tag === 'INPUT' || tag === 'TEXTAREA' || target.isContentEditable) {
          return
        }
      }

      const items = e.clipboardData?.items
      if (!items) return

      for (let i = 0; i < items.length; i++) {
        const item = items[i]
        if (item.kind !== 'file') continue
        if (!item.type.startsWith('image/')) continue

        const blob = item.getAsFile()
        if (!blob) continue

        const name =
          blob.name && blob.name !== 'image.png'
            ? blob.name
            : `screenshot-${stamp(new Date())}.${ext(blob.type)}`
        // Rebuild as a File so the upload pipeline sees a real filename
        // and the right MIME type.
        const file = new File([blob], name, { type: blob.type || 'image/png' })
        e.preventDefault()
        void onImage(file)
        return
      }
    }

    window.addEventListener('paste', onPaste)
    return () => window.removeEventListener('paste', onPaste)
  }, [onImage])
}

function stamp(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
  )
}

function ext(mime: string): string {
  switch (mime) {
    case 'image/jpeg':
      return 'jpg'
    case 'image/webp':
      return 'webp'
    case 'image/gif':
      return 'gif'
    default:
      return 'png'
  }
}