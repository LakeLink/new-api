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
import { useIsFetching } from '@tanstack/react-query'
import { useNavigate, getRouteApi } from '@tanstack/react-router'
import type { Table } from '@tanstack/react-table'
import {
  CircleQuestionMark,
  Download,
  Eye,
  EyeOff,
  ListFilter,
} from 'lucide-react'
import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useStatus } from '@/hooks/use-status'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { exportLogs } from '../api'
import {
  DEFAULT_LOG_EXPORT_ROW_LIMIT,
  LOG_EXPORT_ROW_OPTIONS,
  LOG_TYPE_ALL_VALUE,
  LOG_TYPE_FILTERS,
} from '../constants'
import {
  enterLogExpressionMode,
  mergeLogExpressionUrl,
  regenerateLogExpressionForScopeChange,
  resolveLogExpressionUrlChange,
  type LogExpressionDraftOrigin,
  type PendingLogExpressionNavigation,
} from '../lib/expression-search'
import { buildSearchParams } from '../lib/filter'
import { buildApiParams, getDefaultTimeRange } from '../lib/utils'
import type { CommonLogFilters, LogExportFormat } from '../types'
import { CommonLogsStats } from './common-logs-stats'
import { CompactDateTimeRangePicker } from './compact-date-time-range-picker'
import { ExpressionSearchHelpDialog } from './expression-search-help-dialog'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from './logs-filter-toolbar'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

type LogTypeValue = (typeof LOG_TYPE_FILTERS)[number]['value']
type LogExportRowLimit = (typeof LOG_EXPORT_ROW_OPTIONS)[number]
const logTypeValueSet = new Set<string>(
  LOG_TYPE_FILTERS.map((type) => type.value)
)

const exportFormatOptions: Array<{
  value: LogExportFormat
  label: string
}> = [
  { value: 'jsonl', label: 'Export JSONL' },
  { value: 'json', label: 'Export JSON' },
  { value: 'csv', label: 'Export CSV' },
]

function isLogTypeValue(value: string): value is LogTypeValue {
  return logTypeValueSet.has(value)
}

interface CommonLogsFilterBarProps<TData> {
  table: Table<TData>
}

export function CommonLogsFilterBar<TData>(
  props: CommonLogsFilterBarProps<TData>
) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const searchParams = route.useSearch()
  const { isAdminView: isAdmin } = useLogsViewScope()
  const userRole = useAuthStore((state) => state.auth.user?.role ?? 0)
  const { status } = useStatus()
  const { sensitiveVisible, setSensitiveVisible } = useUsageLogsContext()
  const fetchingLogs = useIsFetching({ queryKey: ['logs'] })

  const [filters, setFilters] = useState<CommonLogFilters>(() => {
    const { start, end } = getDefaultTimeRange()
    return { startTime: start, endTime: end }
  })
  const [logType, setLogType] = useState<LogTypeValue>(LOG_TYPE_ALL_VALUE)
  const [exprMode, setExprMode] = useState(false)
  const exprOriginRef = useRef<LogExpressionDraftOrigin>('generated')
  const pendingExpressionUrlRef =
    useRef<PendingLogExpressionNavigation>(undefined)
  const lastUrlExpressionRef = useRef<string | undefined>(undefined)
  const previousIsAdminRef = useRef(isAdmin)
  const [exprHelpOpen, setExprHelpOpen] = useState(false)
  const [exportingFormat, setExportingFormat] =
    useState<LogExportFormat | null>(null)
  const [exportDialogOpen, setExportDialogOpen] = useState(false)
  const [exportFormat, setExportFormat] = useState<LogExportFormat>('jsonl')
  const [exportRowLimit, setExportRowLimit] = useState<LogExportRowLimit>(
    DEFAULT_LOG_EXPORT_ROW_LIMIT
  )
  const parsedLogExportPermission = Number(
    status?.log_export_permission ?? ROLE.ADMIN
  )
  const logExportPermission = Number.isFinite(parsedLogExportPermission)
    ? parsedLogExportPermission
    : ROLE.ADMIN
  const canExportLogs = isAdmin && userRole >= logExportPermission

  useEffect(() => {
    const { start, end } = getDefaultTimeRange()
    const hasUrlTimeRange =
      searchParams.startTime !== undefined || searchParams.endTime !== undefined
    let routeStartTime: Date | undefined = start
    let routeEndTime: Date | undefined = end
    if (hasUrlTimeRange) {
      routeStartTime =
        searchParams.startTime !== undefined
          ? new Date(searchParams.startTime)
          : undefined
      routeEndTime =
        searchParams.endTime !== undefined
          ? new Date(searchParams.endTime)
          : undefined
    }
    const urlExpression = searchParams.expr || undefined
    const previousUrlExpression = lastUrlExpressionRef.current
    lastUrlExpressionRef.current = urlExpression
    const resolvedUrlChange = resolveLogExpressionUrlChange({
      urlExpression,
      previousUrlExpression,
      currentOrigin: exprOriginRef.current,
      pendingNavigation: pendingExpressionUrlRef.current,
    })
    exprOriginRef.current = resolvedUrlChange.origin
    pendingExpressionUrlRef.current = resolvedUrlChange.pendingNavigation
    const isOwnExpressionNavigation = resolvedUrlChange.isOwnNavigation

    setFilters((previousFilters) =>
      mergeLogExpressionUrl({
        previousFilters,
        urlExpression,
        origin: exprOriginRef.current,
        isOwnNavigation: isOwnExpressionNavigation,
        routeFilters: {
          startTime: routeStartTime,
          endTime: routeEndTime,
          channel: searchParams.channel || undefined,
          model: searchParams.model || undefined,
          token: searchParams.token || undefined,
          group: searchParams.group || undefined,
          username: searchParams.username || undefined,
          requestId: searchParams.requestId || undefined,
          upstreamRequestId: searchParams.upstreamRequestId || undefined,
        },
      })
    )
    setExprMode(!!urlExpression)

    const typeArr = searchParams.type
    const nextLogType =
      Array.isArray(typeArr) &&
      typeArr.length === 1 &&
      isLogTypeValue(typeArr[0])
        ? typeArr[0]
        : LOG_TYPE_ALL_VALUE
    if (!isOwnExpressionNavigation) setLogType(nextLogType)
  }, [
    searchParams.startTime,
    searchParams.endTime,
    searchParams.channel,
    searchParams.model,
    searchParams.token,
    searchParams.group,
    searchParams.username,
    searchParams.requestId,
    searchParams.expr,
    searchParams.upstreamRequestId,
    searchParams.type,
  ])

  useEffect(() => {
    const previousIsAdmin = previousIsAdminRef.current
    previousIsAdminRef.current = isAdmin
    if (previousIsAdmin === isAdmin || !exprMode) return

    const previousExpression = filters.expr || ''
    const nextDraft = regenerateLogExpressionForScopeChange({
      draft: {
        origin: exprOriginRef.current,
        value: previousExpression,
      },
      filters,
      logType,
      isAdmin,
    })
    if (!nextDraft || nextDraft.value === previousExpression) return

    exprOriginRef.current = nextDraft.origin
    setFilters((previousFilters) => ({
      ...previousFilters,
      expr: nextDraft.value,
    }))

    if (!searchParams.expr || searchParams.expr !== previousExpression) return

    pendingExpressionUrlRef.current = nextDraft
    navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: {
        expr: nextDraft.value,
        page: 1,
        ...(searchParams.pageSize ? { pageSize: searchParams.pageSize } : {}),
      },
      replace: true,
    })
  }, [
    exprMode,
    filters,
    isAdmin,
    logType,
    navigate,
    searchParams.expr,
    searchParams.pageSize,
  ])

  const handleChange = useCallback(
    (field: keyof CommonLogFilters, value: Date | string | undefined) => {
      setFilters((prev) => ({ ...prev, [field]: value }))
    },
    []
  )

  const handleExpressionChange = useCallback((value: string) => {
    exprOriginRef.current = 'user'
    pendingExpressionUrlRef.current = undefined
    setFilters((previousFilters) => ({ ...previousFilters, expr: value }))
  }, [])

  const handleModeToggle = useCallback(() => {
    if (exprMode) {
      setExprMode(false)
      return
    }

    const nextDraft = enterLogExpressionMode({
      draft: {
        origin: exprOriginRef.current,
        value: filters.expr || '',
      },
      filters,
      logType,
      isAdmin,
    })
    exprOriginRef.current = nextDraft.origin
    setFilters((previousFilters) => ({
      ...previousFilters,
      expr: nextDraft.value,
    }))
    setExprMode(true)
  }, [exprMode, filters, isAdmin, logType])

  const handleApply = useCallback(() => {
    const activeFilters = exprMode
      ? {
          expr: filters.expr,
        }
      : { ...filters, expr: undefined }
    const filterParams = buildSearchParams(activeFilters, 'common')
    pendingExpressionUrlRef.current =
      exprMode && filters.expr && filters.expr !== searchParams.expr
        ? { value: filters.expr, origin: exprOriginRef.current }
        : undefined
    navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: {
        ...filterParams,
        ...(!exprMode ? { type: [logType] } : {}),
        page: 1,
      },
    })
  }, [exprMode, filters, logType, navigate, searchParams.expr])

  const handleReset = useCallback(() => {
    const { start, end } = getDefaultTimeRange()
    const resetFilters: CommonLogFilters = { startTime: start, endTime: end }
    setFilters(resetFilters)
    setExprMode(false)
    setLogType(LOG_TYPE_ALL_VALUE)
    exprOriginRef.current = 'generated'
    pendingExpressionUrlRef.current = undefined

    navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: {
        page: 1,
        type: [LOG_TYPE_ALL_VALUE],
        startTime: start.getTime(),
        endTime: end.getTime(),
      },
    })
  }, [navigate])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') handleApply()
    },
    [handleApply]
  )

  const hasExpandedFilters =
    !exprMode &&
    (!!filters.token ||
      !!filters.username ||
      !!filters.channel ||
      !!filters.requestId ||
      !!filters.upstreamRequestId)

  const hasTypeFilter = logType !== LOG_TYPE_ALL_VALUE
  const hasAdditionalFilters =
    exprMode ||
    !!filters.expr ||
    (!exprMode &&
      (!!filters.model ||
        !!filters.group ||
        hasTypeFilter ||
        hasExpandedFilters))

  const expandedFilterCount = [
    filters.token,
    isAdmin ? filters.username : undefined,
    isAdmin ? filters.channel : undefined,
    filters.requestId,
    filters.upstreamRequestId,
  ].filter(Boolean).length
  const expressionFilterCount = filters.expr ? 1 : 0
  const mobileFilterCount = exprMode
    ? expressionFilterCount
    : [filters.model, filters.group, hasTypeFilter].filter(Boolean).length +
      expandedFilterCount
  const sensitiveType = sensitiveVisible ? 'text' : 'password'
  const logTypeItems = useMemo(
    () =>
      LOG_TYPE_FILTERS.map((type) => ({
        value: type.value,
        label: t(type.label),
      })),
    [t]
  )
  const logTypeLabel =
    logTypeItems.find((type) => type.value === logType)?.label ?? t('All Types')

  const modeToggle = (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            type='button'
            variant={exprMode ? 'default' : 'outline'}
            size='icon'
            onClick={handleModeToggle}
            aria-label={
              exprMode ? t('Use field filters') : t('Use expression search')
            }
          />
        }
      >
        <ListFilter />
      </TooltipTrigger>
      <TooltipContent>
        {exprMode ? t('Use field filters') : t('Use expression search')}
      </TooltipContent>
    </Tooltip>
  )

  const exprHelpButton = (
    <>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='outline'
              size='icon'
              onClick={() => setExprHelpOpen(true)}
              aria-label={t('Expression search help')}
            />
          }
        >
          <CircleQuestionMark />
        </TooltipTrigger>
        <TooltipContent>{t('Expression search help')}</TooltipContent>
      </Tooltip>
      <ExpressionSearchHelpDialog
        open={exprHelpOpen}
        onOpenChange={setExprHelpOpen}
        isAdminView={isAdmin}
      />
    </>
  )

  const handleExport = useCallback(
    async (format: LogExportFormat, rowLimit: LogExportRowLimit) => {
      if (exportingFormat) return
      setExportingFormat(format)
      try {
        const params = buildApiParams({
          page: 1,
          pageSize: 1,
          searchParams,
          columnFilters: [],
          isAdmin,
        })
        delete params.p
        delete params.page_size
        params.limit = rowLimit === 'all' ? 'all' : Number(rowLimit)
        const { blob, filename } = await exportLogs(format, params, isAdmin)
        downloadBlob(blob, filename || `call-logs.${format}`)
        setExportDialogOpen(false)
        toast.success(t('Export downloaded'))
      } catch {
        toast.error(t('Export failed'))
      } finally {
        setExportingFormat(null)
      }
    },
    [exportingFormat, isAdmin, searchParams, t]
  )

  const exportDialog = (
    <Dialog open={exportDialogOpen} onOpenChange={setExportDialogOpen}>
      <DialogTrigger
        render={<Button variant='outline' disabled={!!exportingFormat} />}
      >
        <Download />
        {t('Export')}
      </DialogTrigger>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Export Call Logs')}</DialogTitle>
        </DialogHeader>
        <div className='grid gap-4 py-1'>
          <div className='grid gap-2'>
            <Label htmlFor='log-export-format'>{t('Format')}</Label>
            <Select
              items={exportFormatOptions.map((option) => ({
                value: option.value,
                label: t(option.label),
              }))}
              value={exportFormat}
              onValueChange={(value) => {
                if (value) setExportFormat(value as LogExportFormat)
              }}
              disabled={!!exportingFormat}
            >
              <SelectTrigger id='log-export-format' className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {exportFormatOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className='grid gap-2'>
            <Label htmlFor='log-export-row-limit'>{t('Rows to export')}</Label>
            <Select
              items={LOG_EXPORT_ROW_OPTIONS.map((option) => ({
                value: option,
                label: option === 'all' ? t('All') : option,
              }))}
              value={exportRowLimit}
              onValueChange={(value) => {
                if (value) setExportRowLimit(value as LogExportRowLimit)
              }}
              disabled={!!exportingFormat}
            >
              <SelectTrigger id='log-export-row-limit' className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {LOG_EXPORT_ROW_OPTIONS.map((option) => (
                    <SelectItem key={option} value={option}>
                      {option === 'all' ? t('All') : option}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        </div>
        <DialogFooter>
          <Button
            variant='outline'
            onClick={() => setExportDialogOpen(false)}
            disabled={!!exportingFormat}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={() => handleExport(exportFormat, exportRowLimit)}
            disabled={!!exportingFormat}
          >
            <Download />
            {exportingFormat ? t('Exporting...') : t('Export')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )

  const preActions = (
    <>
      {modeToggle}
      {exprHelpButton}
      {canExportLogs ? exportDialog : null}
    </>
  )

  const statsBar = (
    <div className='flex flex-wrap items-center gap-2'>
      <CommonLogsStats />
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon'
              onClick={() => setSensitiveVisible(!sensitiveVisible)}
              aria-label={sensitiveVisible ? t('Hide') : t('Show')}
              className='text-muted-foreground hover:text-foreground size-7'
            />
          }
        >
          {sensitiveVisible ? <Eye /> : <EyeOff />}
        </TooltipTrigger>
        <TooltipContent>
          {sensitiveVisible ? t('Hide') : t('Show')}
        </TooltipContent>
      </Tooltip>
      {preActions}
    </div>
  )

  const dateRangeFilter = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={filters.startTime}
        end={filters.endTime}
        onChange={({ start, end }) => {
          handleChange('startTime', start)
          handleChange('endTime', end)
        }}
      />
    </LogsFilterField>
  )
  const modelFilter = (
    <LogsFilterField>
      <LogsFilterInput
        placeholder={t('Model Name')}
        value={filters.model || ''}
        onChange={(e) => handleChange('model', e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )
  const groupFilter = (
    <LogsFilterField>
      <LogsFilterInput
        placeholder={t('Group')}
        type={sensitiveType}
        value={filters.group || ''}
        onChange={(e) => handleChange('group', e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )
  const typeFilter = (
    <LogsFilterField>
      <Select
        items={logTypeItems}
        value={logType}
        onValueChange={(value) => {
          setLogType(
            value !== null && isLogTypeValue(value) ? value : LOG_TYPE_ALL_VALUE
          )
        }}
      >
        <SelectTrigger>
          <SelectValue>{logTypeLabel}</SelectValue>
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {LOG_TYPE_FILTERS.map((type) => (
              <SelectItem key={type.value} value={type.value}>
                {t(type.label)}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </LogsFilterField>
  )
  const expressionFilter = (
    <LogsFilterField wide className='sm:col-span-3'>
      <LogsFilterInput
        placeholder={t(
          'Expression search, e.g. model_name contains "gpt" && type == 2'
        )}
        value={filters.expr || ''}
        onChange={(e) => handleExpressionChange(e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )
  const advancedFilters = (
    <>
      <LogsFilterField>
        <LogsFilterInput
          placeholder={t('Token Name')}
          type={sensitiveType}
          value={filters.token || ''}
          onChange={(e) => handleChange('token', e.target.value)}
          onKeyDown={handleKeyDown}
        />
      </LogsFilterField>
      {isAdmin && (
        <LogsFilterField>
          <LogsFilterInput
            placeholder={t('Username')}
            type={sensitiveType}
            value={filters.username || ''}
            onChange={(e) => handleChange('username', e.target.value)}
            onKeyDown={handleKeyDown}
          />
        </LogsFilterField>
      )}
      {isAdmin && (
        <LogsFilterField>
          <LogsFilterInput
            placeholder={t('Channel ID')}
            value={filters.channel || ''}
            onChange={(e) => handleChange('channel', e.target.value)}
            onKeyDown={handleKeyDown}
          />
        </LogsFilterField>
      )}
      <LogsFilterField>
        <LogsFilterInput
          placeholder={t('Request ID')}
          value={filters.requestId || ''}
          onChange={(e) => handleChange('requestId', e.target.value)}
          onKeyDown={handleKeyDown}
        />
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          placeholder={t('Upstream Request ID')}
          value={filters.upstreamRequestId || ''}
          onChange={(e) => handleChange('upstreamRequestId', e.target.value)}
          onKeyDown={handleKeyDown}
        />
      </LogsFilterField>
    </>
  )

  return (
    <LogsFilterToolbar
      table={props.table}
      stats={statsBar}
      primaryFilters={
        exprMode ? (
          expressionFilter
        ) : (
          <>
            {dateRangeFilter}
            {modelFilter}
            {groupFilter}
            {typeFilter}
          </>
        )
      }
      advancedFilters={exprMode ? undefined : advancedFilters}
      mobilePinnedFilters={exprMode ? expressionFilter : dateRangeFilter}
      mobileFilters={
        exprMode ? (
          expressionFilter
        ) : (
          <>
            {modelFilter}
            {groupFilter}
            {typeFilter}
            {advancedFilters}
          </>
        )
      }
      mobileFilterCount={mobileFilterCount}
      hasAdvancedActiveFilters={!exprMode && hasExpandedFilters}
      advancedFilterCount={exprMode ? 0 : expandedFilterCount}
      hasActiveFilters={hasAdditionalFilters}
      onSearch={handleApply}
      searchLoading={fetchingLogs > 0}
      onReset={handleReset}
    />
  )
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}
