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
export interface CodexUsageWindow {
  used_percent?: number
  reset_at?: number
  reset_after_seconds?: number
  limit_window_seconds?: number
}

export interface CodexUsageTimelineData {
  usedPercent: number
  elapsedDays: number
  durationDays: number
}

export function getCodexUsageTimeline(
  windowData: CodexUsageWindow | null | undefined,
  now: number,
  receivedAt: number = now
): CodexUsageTimelineData | null {
  const duration = windowData?.limit_window_seconds
  const used = windowData?.used_percent
  if (
    typeof duration !== 'number' ||
    !Number.isFinite(duration) ||
    duration <= 0 ||
    typeof used !== 'number' ||
    !Number.isFinite(used)
  ) {
    return null
  }

  const resetAt = windowData?.reset_at
  const resetAfter = windowData?.reset_after_seconds
  let remaining: number
  if (typeof resetAt === 'number' && Number.isFinite(resetAt) && resetAt > 0) {
    remaining = resetAt - now / 1000
  } else if (typeof resetAfter === 'number' && Number.isFinite(resetAfter)) {
    remaining = resetAfter - (now - receivedAt) / 1000
  } else {
    return null
  }

  return {
    usedPercent: Math.max(0, Math.min(100, used)),
    elapsedDays: Math.max(0, Math.min(duration, duration - remaining)) / 86400,
    durationDays: duration / 86400,
  }
}
