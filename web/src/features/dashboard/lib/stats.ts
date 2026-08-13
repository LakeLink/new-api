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
import type { QuotaDataItem } from '@/features/dashboard/types'

const SECONDS_PER_DAY = 24 * 60 * 60
const SECONDS_PER_HOUR = 60 * 60
const SECONDS_PER_MINUTE = 60

export const BALANCE_BURN_FORECAST_DAYS = 7

export type BalanceBurnForecastStatus = 'active' | 'idle' | 'exhausted'

export interface BalanceBurnForecast {
  status: BalanceBurnForecastStatus
  dailyBurnQuota: number
  hourlyBurnQuota: number
  daysRemaining: number | null
  secondsRemaining: number | null
  estimatedEmptyAt: Date | null
  lookbackDays: number
  totalUsageQuota: number
  recentUsageQuota: number
  trend: number[]
}

export interface DurationParts {
  days: number
  hours: number
  minutes: number
  seconds: number
}

type Translate = (key: string, options?: Record<string, unknown>) => string

/**
 * Safe division: handles NaN and Infinity cases
 */
export function safeDivide(
  value: number,
  divisor: number,
  precision: number = 3
): number {
  const result = value / divisor
  if (isNaN(result) || !isFinite(result)) return 0
  const factor = Math.pow(10, precision)
  return Math.round(result * factor) / factor
}

/**
 * Calculate aggregated statistics from quota data
 */
export function calculateDashboardStats(data: QuotaDataItem[]) {
  return data.reduce(
    (acc, item) => ({
      totalQuota: acc.totalQuota + (Number(item.quota) || 0),
      totalCount: acc.totalCount + (Number(item.count) || 0),
      totalTokens: acc.totalTokens + (Number(item.token_used) || 0),
    }),
    { totalQuota: 0, totalCount: 0, totalTokens: 0 }
  )
}

function getBucketIndex(
  timestamp: number,
  start: number,
  end: number,
  bucketCount: number
): number {
  if (end <= start) return 0
  const ratio = (timestamp - start) / (end - start)
  return Math.min(bucketCount - 1, Math.max(0, Math.floor(ratio * bucketCount)))
}

function getPositiveQuota(item: QuotaDataItem): number {
  return Math.max(0, Number(item.quota) || 0)
}

function calculateWindowUsage(
  data: QuotaDataItem[],
  windowStart: number,
  effectiveEnd: number
): number {
  return data.reduce((total, item) => {
    const timestamp = Number(item.created_at) || 0
    if (timestamp < windowStart || timestamp > effectiveEnd) return total
    return total + getPositiveQuota(item)
  }, 0)
}

function calculateHourlyBurnRate(
  totalUsage: number,
  recent24Usage: number,
  recent48Usage: number,
  lookbackSeconds: number
): number {
  const lookbackHours = Math.max(lookbackSeconds / SECONDS_PER_HOUR, 1)
  const fullRate = totalUsage / lookbackHours
  const recent24Rate = recent24Usage / Math.min(24, lookbackHours)
  const recent48Rate = recent48Usage / Math.min(48, lookbackHours)

  if (recent24Usage > 0) {
    return recent24Rate * 0.55 + recent48Rate * 0.3 + fullRate * 0.15
  }

  if (recent48Usage > 0) {
    return recent48Rate * 0.65 + fullRate * 0.35
  }

  return fullRate
}

export function getDurationParts(totalSeconds: number): DurationParts {
  const normalized = Math.max(0, Math.floor(Number(totalSeconds) || 0))
  const days = Math.floor(normalized / SECONDS_PER_DAY)
  const hours = Math.floor((normalized % SECONDS_PER_DAY) / SECONDS_PER_HOUR)
  const minutes = Math.floor(
    (normalized % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
  )
  const seconds = normalized % SECONDS_PER_MINUTE

  return { days, hours, minutes, seconds }
}

export function formatBurnDurationCompact(
  forecast: BalanceBurnForecast,
  t: Translate
): string {
  if (forecast.status === 'exhausted') return t('Exhausted')
  if (forecast.status === 'idle' || forecast.secondsRemaining === null) {
    return t('No active burn')
  }

  if (forecast.secondsRemaining < SECONDS_PER_MINUTE) return t('in <1min')

  if (forecast.secondsRemaining < 2 * SECONDS_PER_DAY) {
    const totalMinutes = Math.max(
      1,
      Math.ceil(forecast.secondsRemaining / SECONDS_PER_MINUTE)
    )
    const hours = Math.floor(totalMinutes / 60)
    const minutes = totalMinutes % 60
    return t('in {{hours}}h{{minutes}}min', { hours, minutes })
  }

  return t('in {{count}} days', {
    count: Math.ceil(forecast.secondsRemaining / SECONDS_PER_DAY),
  })
}

export function formatBurnDurationPrecise(
  forecast: BalanceBurnForecast,
  t: Translate
): string {
  if (forecast.status === 'idle' || forecast.secondsRemaining === null) {
    return t('No active burn')
  }

  const parts = getDurationParts(forecast.secondsRemaining)
  return t('{{days}}d {{hours}}h {{minutes}}m {{seconds}}s', { ...parts })
}

export function calculateBalanceBurnForecast(
  data: QuotaDataItem[],
  currentBalance: number,
  start: number,
  end: number,
  bucketCount = BALANCE_BURN_FORECAST_DAYS
): BalanceBurnForecast {
  const nowSeconds = Math.floor(Date.now() / 1000)
  const effectiveEnd = Math.max(
    start + SECONDS_PER_HOUR,
    Math.min(end, nowSeconds)
  )
  const lookbackSeconds = Math.max(effectiveEnd - start, SECONDS_PER_HOUR)
  const lookbackDays = lookbackSeconds / SECONDS_PER_DAY
  const trend = Array.from({ length: bucketCount }, () => 0)
  let totalUsage = 0

  for (const item of data) {
    const timestamp = Number(item.created_at) || start
    if (timestamp < start || timestamp > effectiveEnd) continue

    const quota = getPositiveQuota(item)
    totalUsage += quota

    const index = getBucketIndex(timestamp, start, effectiveEnd, bucketCount)
    trend[index] += quota
  }

  const balance = Math.max(0, Number(currentBalance) || 0)
  const recent24Usage = calculateWindowUsage(
    data,
    effectiveEnd - SECONDS_PER_DAY,
    effectiveEnd
  )
  const recent48Usage = calculateWindowUsage(
    data,
    effectiveEnd - 2 * SECONDS_PER_DAY,
    effectiveEnd
  )
  const hourlyBurnQuota = calculateHourlyBurnRate(
    totalUsage,
    recent24Usage,
    recent48Usage,
    lookbackSeconds
  )
  const dailyBurnQuota = hourlyBurnQuota * 24

  if (balance <= 0) {
    return {
      status: 'exhausted',
      dailyBurnQuota,
      hourlyBurnQuota,
      daysRemaining: 0,
      secondsRemaining: 0,
      estimatedEmptyAt: new Date(),
      lookbackDays,
      totalUsageQuota: totalUsage,
      recentUsageQuota: recent24Usage,
      trend,
    }
  }

  if (hourlyBurnQuota <= 0) {
    return {
      status: 'idle',
      dailyBurnQuota: 0,
      hourlyBurnQuota: 0,
      daysRemaining: null,
      secondsRemaining: null,
      estimatedEmptyAt: null,
      lookbackDays,
      totalUsageQuota: totalUsage,
      recentUsageQuota: recent24Usage,
      trend,
    }
  }

  const secondsRemaining = (balance / hourlyBurnQuota) * SECONDS_PER_HOUR
  const daysRemaining = secondsRemaining / SECONDS_PER_DAY

  return {
    status: 'active',
    dailyBurnQuota,
    hourlyBurnQuota,
    daysRemaining,
    secondsRemaining,
    estimatedEmptyAt: new Date(Date.now() + secondsRemaining * 1000),
    lookbackDays,
    totalUsageQuota: totalUsage,
    recentUsageQuota: recent24Usage,
    trend,
  }
}
