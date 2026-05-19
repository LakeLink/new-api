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

export const BALANCE_BURN_FORECAST_DAYS = 7

export type BalanceBurnForecastStatus = 'active' | 'idle' | 'exhausted'

export interface BalanceBurnForecast {
  status: BalanceBurnForecastStatus
  dailyBurnQuota: number
  daysRemaining: number | null
  estimatedEmptyAt: Date | null
  lookbackDays: number
  trend: number[]
}

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

export function calculateBalanceBurnForecast(
  data: QuotaDataItem[],
  currentBalance: number,
  start: number,
  end: number,
  bucketCount = BALANCE_BURN_FORECAST_DAYS
): BalanceBurnForecast {
  const lookbackDays = Math.max((end - start) / SECONDS_PER_DAY, 1 / 24)
  const trend = Array.from({ length: bucketCount }, () => 0)
  let totalUsage = 0

  for (const item of data) {
    const quota = getPositiveQuota(item)
    totalUsage += quota

    const timestamp = Number(item.created_at) || start
    const index = getBucketIndex(timestamp, start, end, bucketCount)
    trend[index] += quota
  }

  const balance = Math.max(0, Number(currentBalance) || 0)
  const dailyBurnQuota = totalUsage / lookbackDays

  if (balance <= 0) {
    return {
      status: 'exhausted',
      dailyBurnQuota,
      daysRemaining: 0,
      estimatedEmptyAt: new Date(),
      lookbackDays,
      trend,
    }
  }

  if (dailyBurnQuota <= 0) {
    return {
      status: 'idle',
      dailyBurnQuota: 0,
      daysRemaining: null,
      estimatedEmptyAt: null,
      lookbackDays,
      trend,
    }
  }

  const daysRemaining = balance / dailyBurnQuota

  return {
    status: 'active',
    dailyBurnQuota,
    daysRemaining,
    estimatedEmptyAt: new Date(
      Date.now() + daysRemaining * SECONDS_PER_DAY * 1000
    ),
    lookbackDays,
    trend,
  }
}
