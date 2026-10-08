/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { getCodexUsageTimeline } from '../../lib/codex-usage-timeline'
import { CodexUsageTimeline } from '../dialogs/codex-usage-timeline'

const day = 86400
const now = 2000000000000

afterEach(() => vi.useRealTimers())

describe('Codex usage timeline', () => {
  test('plots used quota against elapsed weekly time', () => {
    expect(
      getCodexUsageTimeline(
        {
          used_percent: 40,
          limit_window_seconds: 7 * day,
          reset_at: now / 1000 + 5 * day,
        },
        now
      )
    ).toEqual({ usedPercent: 40, elapsedDays: 2, durationDays: 7 })
  })
  test('prefers absolute reset and keeps the complete window duration', () => {
    expect(
      getCodexUsageTimeline(
        {
          used_percent: 10,
          limit_window_seconds: 30 * day,
          reset_at: now / 1000 + 20 * day,
          reset_after_seconds: 0,
        },
        now
      )
    ).toEqual({ usedPercent: 10, elapsedDays: 10, durationDays: 30 })
  })
  test('relative reset advances from the receipt time', () => {
    expect(
      getCodexUsageTimeline(
        {
          used_percent: 60,
          limit_window_seconds: 7 * day,
          reset_after_seconds: 4 * day,
        },
        now + day * 1000,
        now
      )
    ).toEqual({ usedPercent: 60, elapsedDays: 4, durationDays: 7 })
  })
  test.each([
    undefined,
    {},
    { used_percent: 10, limit_window_seconds: 7 * day },
    {
      used_percent: Number.NaN,
      limit_window_seconds: 7 * day,
      reset_after_seconds: 0,
    },
    { used_percent: 10, limit_window_seconds: 0, reset_after_seconds: 0 },
  ])('does not invent missing or invalid data: %j', (value) => {
    expect(getCodexUsageTimeline(value, now)).toBeNull()
  })
  test('clamps expired and out-of-range windows to chart bounds', () => {
    expect(
      getCodexUsageTimeline(
        {
          used_percent: 120,
          limit_window_seconds: 7 * day,
          reset_at: now / 1000 - day,
        },
        now
      )
    ).toEqual({ usedPercent: 100, elapsedDays: 7, durationDays: 7 })
    expect(
      getCodexUsageTimeline(
        {
          used_percent: -3,
          limit_window_seconds: 7 * day,
          reset_after_seconds: 9 * day,
        },
        now
      )
    ).toEqual({ usedPercent: 0, elapsedDays: 0, durationDays: 7 })
  })
  test('renders an accessible summary and responds to refreshed data', () => {
    vi.useFakeTimers()
    vi.setSystemTime(now)
    const view = render(
      <CodexUsageTimeline
        windowData={{
          used_percent: 40,
          limit_window_seconds: 7 * day,
          reset_after_seconds: 5 * day,
        }}
      />
    )
    expect(screen.getByText('Weekly usage pace')).toBeVisible()
    expect(screen.getByRole('img')).toHaveAccessibleName(
      '40% used · 2 / 7 days elapsed'
    )
    act(() => vi.advanceTimersByTime(day * 1000))
    expect(screen.getByRole('img')).toHaveAccessibleName(
      '40% used · 3 / 7 days elapsed'
    )
    view.rerender(
      <CodexUsageTimeline
        windowData={{
          used_percent: 50,
          limit_window_seconds: 7 * day,
          reset_after_seconds: 3 * day,
        }}
      />
    )
    expect(screen.getByRole('img')).toHaveAccessibleName(
      '50% used · 4 / 7 days elapsed'
    )
    view.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
  test('hides the chart without timing information', () => {
    render(
      <CodexUsageTimeline
        windowData={{ used_percent: 40, limit_window_seconds: 7 * day }}
      />
    )
    expect(screen.queryByText('Weekly usage pace')).not.toBeInTheDocument()
  })
})
