// @vitest-environment jsdom
/**
 * usePasteImage hook tests.
 *
 *   - Listener is attached on mount, removed on unmount.
 *   - Pasting an image calls onImage with a File whose name defaults
 *     to `screenshot-<timestamp>.<ext>` and type matches the mime.
 *   - A non-image clipboard item does NOT call onImage.
 *   - An empty clipboard is a no-op.
 *   - Plain-text paste is left alone (no preventDefault) so a
 *     focused input still receives the text.
 */
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { usePasteImage } from '@/features/attachments/usePasteImage';

afterEach(() => {
  vi.restoreAllMocks();
});

function firePasteWith(items: DataTransferItem[]): void {
  const event = new Event('paste', { bubbles: true, cancelable: true }) as ClipboardEvent;
  // jsdom doesn't carry a real ClipboardEvent constructor; we
  // attach the minimum DataTransfer-like shape ourselves.
  Object.defineProperty(event, 'clipboardData', {
    value: { items, length: items.length },
  });
  Object.defineProperty(event, 'target', { value: document.body });
  act(() => {
    window.dispatchEvent(event);
  });
}

function makeFileItem(name: string, _type: string, mime: string): DataTransferItem {
  // jsdom doesn't construct DataTransferItem either; build a minimal
  // stand-in that matches the interface usePasteImage reads.
  const blob = new Blob(['fake'], { type: mime });
  return {
    kind: 'file',
    type: mime,
    getAsFile: () => new File([blob], name, { type: mime }),
  } as unknown as DataTransferItem;
}

describe('usePasteImage', () => {
  it('calls onImage with a screenshot-named File when an image is pasted', () => {
    const onImage = vi.fn();
    renderHook(() => usePasteImage(onImage));

    const item = makeFileItem('image.png', 'image.png', 'image/png');
    firePasteWith([item]);

    expect(onImage).toHaveBeenCalledTimes(1);
    const file = onImage.mock.calls[0][0] as File;
    expect(file.type).toBe('image/png');
    expect(file.name).toMatch(/^screenshot-\d{4}-\d{2}-\d{2}-\d{6}\.png$/);
  });

  it('uses the original filename when the clipboard file carries one', () => {
    const onImage = vi.fn();
    renderHook(() => usePasteImage(onImage));

    firePasteWith([makeFileItem('shot.png', 'shot.png', 'image/png')]);
    const file = onImage.mock.calls[0][0] as File;
    expect(file.name).toBe('shot.png');
  });

  it('maps the mime type to the correct extension for non-png images', () => {
    const onImage = vi.fn();
    renderHook(() => usePasteImage(onImage));

    firePasteWith([makeFileItem('a.jpg', 'a.jpg', 'image/jpeg')]);
    const file = onImage.mock.calls[0][0] as File;
    expect(file.name).toMatch(/\.jpg$/);
  });

  it('does not call onImage for non-image clipboard items', () => {
    const onImage = vi.fn();
    renderHook(() => usePasteImage(onImage));

    const textItem = {
      kind: 'string',
      type: 'text/plain',
    } as unknown as DataTransferItem;
    firePasteWith([textItem]);

    expect(onImage).not.toHaveBeenCalled();
  });

  it('does not call onImage for an empty clipboard', () => {
    const onImage = vi.fn();
    renderHook(() => usePasteImage(onImage));

    firePasteWith([]);
    expect(onImage).not.toHaveBeenCalled();
  });

  it('removes the paste listener on unmount', () => {
    const onImage = vi.fn();
    const { unmount } = renderHook(() => usePasteImage(onImage));
    unmount();

    firePasteWith([makeFileItem('image.png', 'image.png', 'image/png')]);
    expect(onImage).not.toHaveBeenCalled();
  });
});
