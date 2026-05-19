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
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Loader2, TimerReset } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatDateStr, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { getUserQuotaDates } from '@/features/dashboard/api'
import {
  BALANCE_BURN_FORECAST_DAYS,
  calculateBalanceBurnForecast,
  type BalanceBurnForecast,
} from '@/features/dashboard/lib/stats'
import type { User } from '../../types'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: User | null
}

function getForecastValue(
  forecast: BalanceBurnForecast,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (forecast.status === 'exhausted') return t('Exhausted')
  if (forecast.status === 'idle') return t('No active burn')

  const daysRemaining = forecast.daysRemaining ?? 0
  if (daysRemaining < 1) return t('Less than 1 day remaining')

  return t('{{count}} days remaining', {
    count: Math.ceil(daysRemaining),
  })
}

function getForecastDetail(
  forecast: BalanceBurnForecast,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (forecast.status === 'exhausted') {
    return t('Balance is already exhausted')
  }

  if (forecast.status === 'idle') {
    return t('No quota consumption in the last {{count}} days', {
      count: Math.round(forecast.lookbackDays),
    })
  }

  return t('Estimated empty on {{date}}', {
    date: forecast.estimatedEmptyAt
      ? formatDateStr(forecast.estimatedEmptyAt)
      : '-',
  })
}

function normalizeTrend(values: number[]): number[] {
  const sanitized = values.map((value) => Math.max(0, Number(value) || 0))
  const max = Math.max(...sanitized, 0)
  if (max <= 0) return sanitized.map(() => 0)
  return sanitized.map((value) => Math.max(10, (value / max) * 100))
}

export function UserBurnForecastDialog(props: Props) {
  const { t } = useTranslation()
  const user = props.user
  const range = useMemo(() => {
    const openedUserId = props.open ? user?.id : null
    if (openedUserId) return computeTimeRange(BALANCE_BURN_FORECAST_DAYS)
    return computeTimeRange(BALANCE_BURN_FORECAST_DAYS)
  }, [props.open, user?.id])

  const usageQuery = useQuery({
    queryKey: [
      'users',
      'balance-burn-forecast',
      user?.id,
      user?.username,
      range.start_timestamp,
      range.end_timestamp,
    ],
    queryFn: async () =>
      getUserQuotaDates(
        {
          username: user?.username ?? '',
          start_timestamp: range.start_timestamp,
          end_timestamp: range.end_timestamp,
          default_time: 'hour',
        },
        true
      ),
    enabled: props.open && Boolean(user?.username),
    staleTime: 60 * 1000,
  })

  const forecast = useMemo(
    () =>
      calculateBalanceBurnForecast(
        usageQuery.data?.data ?? [],
        Number(user?.quota ?? 0),
        range.start_timestamp,
        range.end_timestamp
      ),
    [
      range.end_timestamp,
      range.start_timestamp,
      usageQuery.data?.data,
      user?.quota,
    ]
  )

  const trend = normalizeTrend(forecast.trend)
  const recentUsage = forecast.dailyBurnQuota * forecast.lookbackDays

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Balance burn forecast')}</DialogTitle>
          <DialogDescription>
            {user?.username || '-'} (ID: {user?.id || '-'})
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4'>
          <div className='bg-warning/10 rounded-lg border p-4'>
            <div className='text-muted-foreground flex items-center gap-2 text-sm'>
              <TimerReset className='size-4' aria-hidden='true' />
              {t('Balance burn forecast')}
            </div>
            <div className='mt-2 font-mono text-2xl font-semibold tracking-tight'>
              {usageQuery.isLoading ? (
                <span className='flex items-center gap-2 text-base'>
                  <Loader2 className='size-4 animate-spin' aria-hidden='true' />
                  {t('Calculating...')}
                </span>
              ) : (
                getForecastValue(forecast, t)
              )}
            </div>
            <p className='text-muted-foreground mt-1 text-sm'>
              {usageQuery.isLoading
                ? t('Loading recent usage data')
                : getForecastDetail(forecast, t)}
            </p>
          </div>

          <div className='grid gap-2 sm:grid-cols-3'>
            <div className='rounded-lg border p-3'>
              <div className='text-muted-foreground text-xs'>
                {t('Current Balance')}
              </div>
              <div className='mt-1 font-mono text-sm font-semibold break-all'>
                {formatQuota(Number(user?.quota ?? 0))}
              </div>
            </div>
            <div className='rounded-lg border p-3'>
              <div className='text-muted-foreground text-xs'>
                {t('Historical Usage')}
              </div>
              <div className='mt-1 font-mono text-sm font-semibold break-all'>
                {formatQuota(Number(user?.used_quota ?? 0))}
              </div>
            </div>
            <div className='rounded-lg border p-3'>
              <div className='text-muted-foreground text-xs'>
                {t('Recent Usage')}
              </div>
              <div className='mt-1 font-mono text-sm font-semibold break-all'>
                {formatQuota(recentUsage)}
              </div>
            </div>
          </div>

          <div className='rounded-lg border p-3'>
            <div className='text-muted-foreground text-xs'>
              {t('Average daily burn: {{value}}', {
                value: formatQuota(forecast.dailyBurnQuota),
              })}
            </div>
            <div className='mt-3 flex h-12 items-end gap-1' aria-hidden='true'>
              {trend.map((height, index) => (
                <span
                  key={`${user?.id ?? 'user'}-burn-${index}`}
                  className='bg-warning/70 flex-1 rounded-t-sm'
                  style={{ height: `${height}%` }}
                />
              ))}
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
