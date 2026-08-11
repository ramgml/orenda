// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'

import { TaskTagChip, TaskTagChips } from './TaskTagChip'
import type { Tag } from '@/shared/api/client'

const tag = (overrides: Partial<Tag> = {}): Tag => ({
  id: 'tag-1',
  name: 'frontend',
  ...overrides,
})

describe('TaskTagChip', () => {
  it('renders the tag name', () => {
    const { container } = render(<TaskTagChip tag={tag({ name: 'bug' })} />)
    expect(container.textContent).toContain('bug')
  })

  it('uses the tag colour as background when provided', () => {
    const { container } = render(
      <TaskTagChip tag={tag({ color: '#22c55e' })} />,
    )
    const chip = container.querySelector('span') as HTMLElement
    expect(chip.style.backgroundColor).toBeTruthy()
    // Hex "#22c55e" → dark → text should be white-ish for contrast.
    expect(chip.style.color).toBe('rgb(255, 255, 255)')
  })

  it('falls back to a neutral pill when no colour is set', () => {
    const { container } = render(<TaskTagChip tag={tag({ color: undefined })} />)
    const chip = container.querySelector('span') as HTMLElement
    // No background-color inline; Tailwind class is applied instead.
    expect(chip.className).toContain('rounded')
    expect(chip.className).toContain('border')
  })

  it('renders both 3-digit and 6-digit hex colours without crashing', () => {
    expect(() =>
      render(<TaskTagChip tag={tag({ color: '#0ea5e9' })} />),
    ).not.toThrow()
    expect(() => render(<TaskTagChip tag={tag({ color: '#abc' })} />)).not.toThrow()
  })
})

describe('TaskTagChips', () => {
  it('renders nothing when the list is empty or undefined', () => {
    const { container: c1 } = render(<TaskTagChips tags={[]} />)
    const { container: c2 } = render(<TaskTagChips tags={undefined} />)
    expect(c1.querySelectorAll('span').length).toBe(0)
    expect(c2.querySelectorAll('span').length).toBe(0)
  })

  it('renders one chip per tag', () => {
    const tags: Tag[] = [
      { id: '1', name: 'alpha', color: '#22c55e' },
      { id: '2', name: 'bravo' },
    ]
    const { container } = render(<TaskTagChips tags={tags} />)
    // Outer wrapper div + one span per chip = 3 spans total.
    expect(container.querySelectorAll('span').length).toBeGreaterThanOrEqual(2)
    expect(container.textContent).toContain('alpha')
    expect(container.textContent).toContain('bravo')
  })
})