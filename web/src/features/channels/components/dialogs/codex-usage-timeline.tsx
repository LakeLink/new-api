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
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  CartesianGrid,
  LabelList,
  Scatter,
  ScatterChart,
  XAxis,
  YAxis,
} from 'recharts'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ChartContainer } from '@/components/ui/chart'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  getCodexUsageTimeline,
  type CodexUsageWindow,
} from '../../lib/codex-usage-timeline'

export function CodexUsageTimeline(props: {
  windowData?: CodexUsageWindow | null
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const snapshot = useMemo(
    () => ({ windowData: props.windowData, receivedAt: Date.now() }),
    [props.windowData]
  )
  const receivedAt = snapshot.receivedAt
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 60000)
    return () => window.clearInterval(timer)
  }, [receivedAt])

  const data = getCodexUsageTimeline(
    props.windowData,
    Math.max(now, receivedAt),
    receivedAt
  )
  if (!data) return null
  const summary = t(
    '{{used}}% used · {{elapsed}} / {{duration}} days elapsed',
    {
      used: formatNumber(data.usedPercent, locale),
      elapsed: formatNumber(data.elapsedDays, locale),
      duration: formatNumber(data.durationDays, locale),
    }
  )

  return (
    <Card>
      <CardHeader className='gap-2'>
        <CardTitle>{t('Weekly usage pace')}</CardTitle>
        <p className='text-muted-foreground text-sm tabular-nums'>{summary}</p>
      </CardHeader>
      <CardContent>
        <ChartContainer
          config={{ usage: { label: t('Usage'), color: 'var(--chart-1)' } }}
          className='h-64 w-full'
          role='img'
          aria-label={summary}
        >
          <ScatterChart margin={{ top: 24, right: 20, bottom: 24, left: 0 }}>
            <CartesianGrid strokeDasharray='3 3' />
            <XAxis
              type='number'
              dataKey='elapsedDays'
              domain={[0, data.durationDays]}
              tickFormatter={(value: number) => formatNumber(value, locale)}
              label={{
                value: t('Elapsed window time (days)'),
                position: 'bottom',
              }}
            />
            <YAxis
              type='number'
              dataKey='usedPercent'
              domain={[0, 100]}
              ticks={[0, 25, 50, 75, 100]}
              tickFormatter={(value: number) =>
                `${formatNumber(value, locale)}%`
              }
            />
            <Scatter
              data={[
                { elapsedDays: 0, usedPercent: 0 },
                { elapsedDays: data.durationDays, usedPercent: 100 },
              ]}
              line={{
                stroke: 'var(--muted-foreground)',
                strokeDasharray: '6 4',
              }}
              shape={<g />}
              isAnimationActive={false}
            />
            <Scatter
              data={[data]}
              fill='var(--color-usage)'
              isAnimationActive={false}
            >
              <LabelList
                dataKey='usedPercent'
                position='top'
                formatter={(value) => `${formatNumber(Number(value), locale)}%`}
              />
            </Scatter>
          </ScatterChart>
        </ChartContainer>
        <p className='text-muted-foreground mt-3 text-xs'>
          {t('The dashed line shows an even usage pace across the window.')}
        </p>
      </CardContent>
    </Card>
  )
}
