import { useEffect } from 'react'

/**
 * Listens for a paste of image data anywhere on the page and hands the
 * resulting File off to `onImage`. Plain-text pastes are always passed
 * through to the focused element so comment boxes still get Ctrl+V.
 *
 * If the clipboard contains an image, we treat it as a screenshot
 * upload regardless of where focus is — typing in a comment box and
 * then pasting a screenshot still drops it into the attachments. The
 * default paste behaviour (which would drop a binary into the text
 * field anyway) is suppressed for the image branch.
 *
 * File name defaults to `screenshot-YYYY-MM-DD-HHMMSS.png` (or the
 * clipboard's filename if it carries one).
 *
 * Caller is responsible for actually uploading the file — this hook
 * only extracts the image from the event.
 */
export function usePasteImage(onImage: (file: File) => void | Promise<void>): void {
  useEffect(() => {
    function onPaste(e: ClipboardEvent): void {
      const items = e.clipboardData?.items
      if (!items) return

      // Look for an image in the payload first; only that branch hijacks
      // paste regardless of focus.
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
        const file = new File([blob], name, { type: blob.type || 'image/png' })
        e.preventDefault()
        void onImage(file)
        return
      }

      // No image: if focus is inside an editable surface, leave the
      // event alone so the user can paste text into a comment / search
      // field. Otherwise there's nothing for us to do.
      const target = e.target as HTMLElement | null
      if (target) {
        const tag = target.tagName
        if (tag === 'INPUT' || tag === 'TEXTAREA' || target.isContentEditable) {
          return
        }
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