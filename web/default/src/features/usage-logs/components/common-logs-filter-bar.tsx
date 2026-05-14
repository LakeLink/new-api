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
import { useState, useEffect, useCallback } from 'react'
import { useQueryClient, useIsFetching } from '@tanstack/react-query'
import { useNavigate, getRouteApi } from '@tanstack/react-router'
import { type Table } from '@tanstack/react-table'
import {
  CircleQuestionMark,
  Download,
  Eye,
  EyeOff,
  ListFilter,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { useIsAdmin } from '@/hooks/use-admin'
import { useStatus } from '@/hooks/use-status'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
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
import { DataTableToolbar } from '@/components/data-table'
import { exportLogs } from '../api'
import {
  DEFAULT_LOG_EXPORT_ROW_LIMIT,
  LOG_EXPORT_ROW_OPTIONS,
  LOG_TYPES,
} from '../constants'
import { buildSearchParams } from '../lib/filter'
import { buildApiParams, getDefaultTimeRange } from '../lib/utils'
import type { CommonLogFilters, LogExportFormat } from '../types'
import { CommonLogsStats } from './common-logs-stats'
import { CompactDateTimeRangePicker } from './compact-date-time-range-picker'
import { useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')
const logTypeValues = ['0', '1', '2', '3', '4', '5', '6'] as const

type LogTypeValue = (typeof logTypeValues)[number]
type LogExportRowLimit = (typeof LOG_EXPORT_ROW_OPTIONS)[number]

const exportFormatOptions: Array<{
  value: LogExportFormat
  label: string
}> = [
  { value: 'jsonl', label: 'Export JSONL' },
  { value: 'json', label: 'Export JSON' },
  { value: 'csv', label: 'Export CSV' },
]

const exprFieldRows = [
  {
    fields: 'id',
    type: 'Number',
    scope: 'All users',
    description: 'Log record ID.',
  },
  {
    fields: 'user_id',
    type: 'Number',
    scope: 'All users',
    description: 'User ID that owns the log.',
  },
  {
    fields: 'created_at, createdAt, timestamp',
    type: 'Number',
    scope: 'All users',
    description:
      'Creation time as Unix seconds; date(...) is also accepted.',
  },
  {
    fields: 'type, log_type',
    type: 'Number',
    scope: 'All users',
    description:
      'Log type code: 1 top-up, 2 consume, 3 manage, 4 system, 5 error, 6 refund.',
  },
  {
    fields: 'content',
    type: 'String',
    scope: 'All users',
    description: 'Main log content or error message text.',
  },
  {
    fields: 'token_name, token',
    type: 'String',
    scope: 'All users',
    description: 'API token name recorded on the log.',
  },
  {
    fields: 'model_name, model',
    type: 'String',
    scope: 'All users',
    description: 'Requested model name.',
  },
  {
    fields: 'quota',
    type: 'Number',
    scope: 'All users',
    description: 'Charged quota amount.',
  },
  {
    fields: 'prompt_tokens',
    type: 'Number',
    scope: 'All users',
    description: 'Prompt or input token count.',
  },
  {
    fields: 'completion_tokens',
    type: 'Number',
    scope: 'All users',
    description: 'Completion or output token count.',
  },
  {
    fields: 'use_time',
    type: 'Number',
    scope: 'All users',
    description: 'Response time in seconds.',
  },
  {
    fields: 'is_stream, stream',
    type: 'Boolean',
    scope: 'All users',
    description: 'Whether the request used streaming.',
  },
  {
    fields: 'today, yesterday',
    type: 'Boolean',
    scope: 'All users',
    description:
      'Shortcut filters for the current or previous local day.',
  },
  {
    fields: 'token_id',
    type: 'Number',
    scope: 'All users',
    description: 'Numeric API token ID.',
  },
  {
    fields: 'group',
    type: 'String',
    scope: 'All users',
    description: 'Billing or request group name.',
  },
  {
    fields: 'ip',
    type: 'String',
    scope: 'All users',
    description: 'Client IP address if IP logging is enabled.',
  },
  {
    fields: 'request_id, requestId',
    type: 'String',
    scope: 'All users',
    description: 'Request ID for tracing one call.',
  },
  {
    fields: 'other',
    type: 'String',
    scope: 'All users',
    description: 'Additional metadata saved with the log.',
  },
  {
    fields: 'username',
    type: 'String',
    scope: 'Admins only',
    description: 'Username associated with the log.',
  },
  {
    fields: 'channel, channel_id',
    type: 'Number',
    scope: 'Admins only',
    description: 'Channel ID used by the request.',
  },
  {
    fields: 'channel_name, channelName',
    type: 'String',
    scope: 'Admins only',
    description: 'Channel name used by the request.',
  },
] as const

const exprOperatorRows = [
  {
    syntax: '&&, ||, !, and, or, not',
    description: 'Combine conditions with boolean logic and parentheses.',
  },
  {
    syntax: '==, !=, >, >=, <, <=',
    description:
      'Compare a field with a string, integer, boolean, nil, or date(...) literal.',
  },
  {
    syntax: 'contains, startsWith, endsWith',
    description: 'Match string fields with SQL LIKE using escaped wildcards.',
  },
  {
    syntax: 'in, not in',
    description: 'Match a field against a literal array with up to 100 values.',
  },
  {
    syntax: 'nil',
    description: 'Use nil only with == or != to check for null values.',
  },
] as const

const exprExamples = [
  {
    title: 'GPT consumption logs',
    expression: 'model_name contains "gpt" && type == 2',
    description: 'Find consumption records for GPT-family models.',
  },
  {
    title: 'GPT logs today',
    expression: 'model_name contains "gpt-5.5" and today',
    description: 'Find matching model logs from the current local day.',
  },
  {
    title: 'One day by date',
    expression:
      'created_at >= date("2025-01-01") && created_at < date("2025-01-02")',
    description:
      'Use date(...) for readable day boundaries; add a timezone argument when needed.',
  },
  {
    title: 'High quota usage',
    expression: 'quota > 1000 && type == 2',
    description: 'Find expensive consumption records.',
  },
  {
    title: 'Large token requests',
    expression: 'prompt_tokens > 8000 || completion_tokens > 2000',
    description:
      'Find calls with unusually large input or output token counts.',
  },
  {
    title: 'Streaming Claude calls',
    expression: 'is_stream == true && model_name contains "claude"',
    description: 'Find streamed requests for Claude-family models.',
  },
  {
    title: 'One request ID',
    expression: 'request_id == "req_xxx"',
    description: 'Jump to a single traced request.',
  },
  {
    title: 'Specific groups',
    expression: 'group in ["default", "vip"]',
    description: 'Find records from one of several groups.',
  },
  {
    title: 'Model families',
    expression:
      'model_name startsWith "gpt-4" || model_name startsWith "claude"',
    description: 'Compare several model prefixes in one search.',
  },
  {
    title: 'Errors and rate limits',
    expression: 'content contains "timeout" || other contains "429"',
    description: 'Look for timeout messages or upstream rate-limit metadata.',
  },
  {
    title: 'Exclude embeddings',
    expression: 'not (model_name contains "embedding") && type == 2',
    description: 'Keep normal consumption logs while hiding embedding calls.',
  },
  {
    title: 'Named tokens after a time',
    expression: 'token_name != "" && created_at >= date("2025-01-01")',
    description: 'Find logs that have a token name after a readable date.',
  },
  {
    title: 'One client IP',
    expression: 'ip == "1.2.3.4"',
    description: 'Find requests recorded from one client IP address.',
  },
  {
    title: 'Admin: user on channel',
    expression: 'username == "alice" && channel == 12',
    description: 'For admins, filter one user on a numeric channel ID.',
  },
  {
    title: 'Admin: channel name',
    expression: 'channel_name contains "openai" && type == 2',
    description: 'For admins, filter by channel name and log type.',
  },
] as const

function isLogTypeValue(value: string): value is LogTypeValue {
  return (logTypeValues as readonly string[]).includes(value)
}

interface CommonLogsFilterBarProps<TData> {
  table: Table<TData>
}

export function CommonLogsFilterBar<TData>(
  props: CommonLogsFilterBarProps<TData>
) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const searchParams = route.useSearch()
  const isAdmin = useIsAdmin()
  const userRole = useAuthStore((state) => state.auth.user?.role ?? 0)
  const { status } = useStatus()
  const { sensitiveVisible, setSensitiveVisible } = useUsageLogsContext()
  const fetchingLogs = useIsFetching({ queryKey: ['logs'] })

  const [filters, setFilters] = useState<CommonLogFilters>(() => {
    const { start, end } = getDefaultTimeRange()
    return { startTime: start, endTime: end }
  })
  const [logType, setLogType] = useState<LogTypeValue | ''>('')
  const [exprMode, setExprMode] = useState(false)
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
    const next: Partial<CommonLogFilters> = {}
    if (searchParams.startTime)
      next.startTime = new Date(searchParams.startTime)
    if (searchParams.endTime) next.endTime = new Date(searchParams.endTime)
    if (searchParams.channel) next.channel = String(searchParams.channel)
    if (searchParams.model) next.model = searchParams.model
    if (searchParams.token) next.token = searchParams.token
    if (searchParams.group) next.group = searchParams.group
    if (searchParams.username) next.username = searchParams.username
    if (searchParams.requestId) next.requestId = searchParams.requestId
    if (searchParams.expr) next.expr = searchParams.expr

    if (Object.keys(next).length > 0) {
      setFilters((prev) => ({ ...prev, ...next }))
    }
    setExprMode(!!searchParams.expr)

    const typeArr = searchParams.type
    if (Array.isArray(typeArr) && typeArr.length === 1) {
      setLogType(typeArr[0])
    }
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
    searchParams.type,
  ])

  const handleChange = useCallback(
    (field: keyof CommonLogFilters, value: Date | string | undefined) => {
      setFilters((prev) => ({ ...prev, [field]: value }))
    },
    []
  )

  const handleApply = useCallback(() => {
    const activeFilters = exprMode
      ? {
          expr: filters.expr,
        }
      : { ...filters, expr: undefined }
    const filterParams = buildSearchParams(activeFilters, 'common')
    navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: {
        ...filterParams,
        ...(!exprMode && logType ? { type: [logType] } : {}),
        page: 1,
      },
    })
    queryClient.invalidateQueries({ queryKey: ['logs'] })
    queryClient.invalidateQueries({ queryKey: ['usage-logs-stats'] })
  }, [exprMode, filters, logType, navigate, queryClient])

  const handleReset = useCallback(() => {
    const { start, end } = getDefaultTimeRange()
    const resetFilters: CommonLogFilters = { startTime: start, endTime: end }
    setFilters(resetFilters)
    setLogType('')
    setExprMode(false)

    navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: {
        page: 1,
        startTime: start.getTime(),
        endTime: end.getTime(),
      },
    })
    queryClient.invalidateQueries({ queryKey: ['logs'] })
    queryClient.invalidateQueries({ queryKey: ['usage-logs-stats'] })
  }, [navigate, queryClient])

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
      !!filters.requestId)

  const hasAdditionalFilters =
    exprMode ||
    !!filters.expr ||
    (!exprMode &&
      (!!filters.model || !!filters.group || !!logType || hasExpandedFilters))

  const inputClass = 'w-full sm:w-[140px] lg:w-[160px]'
  const sensitiveType = sensitiveVisible ? 'text' : 'password'

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
    </div>
  )

  const modeToggle = (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant={exprMode ? 'default' : 'outline'}
            size='icon'
            onClick={() => setExprMode((prev) => !prev)}
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

  return (
    <DataTableToolbar
      table={props.table}
      leftActions={statsBar}
      customSearch={
        exprMode ? (
          <Input
            placeholder={t(
              'Expression search, e.g. model_name contains "gpt" && type == 2'
            )}
            value={filters.expr || ''}
            onChange={(e) => handleChange('expr', e.target.value)}
            onKeyDown={handleKeyDown}
            className='w-full sm:w-[360px] lg:w-[520px]'
          />
        ) : (
          <CompactDateTimeRangePicker
            start={filters.startTime}
            end={filters.endTime}
            onChange={({ start, end }) => {
              handleChange('startTime', start)
              handleChange('endTime', end)
            }}
            className='w-full sm:w-[340px]'
          />
        )
      }
      additionalSearch={
        exprMode ? null : (
          <>
            <Input
              placeholder={t('Model Name')}
              value={filters.model || ''}
              onChange={(e) => handleChange('model', e.target.value)}
              onKeyDown={handleKeyDown}
              className={inputClass}
            />
            <Input
              placeholder={t('Group')}
              type={sensitiveType}
              value={filters.group || ''}
              onChange={(e) => handleChange('group', e.target.value)}
              onKeyDown={handleKeyDown}
              className={inputClass}
            />
            <Select
              items={[
                { value: 'all', label: t('All Types') },
                ...LOG_TYPES.map((type) => ({
                  value: String(type.value),
                  label: t(type.label),
                })),
              ]}
              value={logType}
              onValueChange={(value) => {
                setLogType(value !== null && isLogTypeValue(value) ? value : '')
              }}
            >
              <SelectTrigger className={inputClass}>
                <SelectValue placeholder={t('All Types')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='all'>{t('All Types')}</SelectItem>
                  {LOG_TYPES.map((type) => (
                    <SelectItem key={type.value} value={String(type.value)}>
                      {t(type.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </>
        )
      }
      expandable={
        exprMode ? null : (
          <>
            <Input
              placeholder={t('Token Name')}
              type={sensitiveType}
              value={filters.token || ''}
              onChange={(e) => handleChange('token', e.target.value)}
              onKeyDown={handleKeyDown}
              className={inputClass}
            />
            {isAdmin && (
              <Input
                placeholder={t('Username')}
                type={sensitiveType}
                value={filters.username || ''}
                onChange={(e) => handleChange('username', e.target.value)}
                onKeyDown={handleKeyDown}
                className={inputClass}
              />
            )}
            {isAdmin && (
              <Input
                placeholder={t('Channel ID')}
                value={filters.channel || ''}
                onChange={(e) => handleChange('channel', e.target.value)}
                onKeyDown={handleKeyDown}
                className={inputClass}
              />
            )}
            <Input
              placeholder={t('Request ID')}
              value={filters.requestId || ''}
              onChange={(e) => handleChange('requestId', e.target.value)}
              onKeyDown={handleKeyDown}
              className={inputClass}
            />
          </>
        )
      }
      preActions={preActions}
      hasExpandedActiveFilters={hasExpandedFilters}
      hasAdditionalFilters={hasAdditionalFilters}
      onSearch={handleApply}
      searchLoading={fetchingLogs > 0}
      onReset={handleReset}
    />
  )
}

interface ExpressionSearchHelpDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function ExpressionSearchHelpDialog({
  open,
  onOpenChange,
}: ExpressionSearchHelpDialogProps) {
  const { t } = useTranslation()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle>{t('Expression Search Reference')}</DialogTitle>
          <DialogDescription>
            {t(
              'Expression search is parsed from the AST and translated into SQL with an allowed field list, placeholders, and escaped LIKE patterns.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-5'>
          <section className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Quick Syntax')}</h3>
            <ul className='text-muted-foreground list-disc space-y-1 ps-5 text-sm'>
              <li>
                {t(
                  'Strings use double quotes, numbers are integers, booleans are true or false, and nil can check null values.'
                )}
              </li>
              <li>
                {t(
                  'Use parentheses to group logic, for example not (model_name contains "embedding") && type == 2.'
                )}
              </li>
              <li>
                {t(
                  'Boolean fields can be written directly, such as is_stream, or compared explicitly with true or false.'
                )}
              </li>
            </ul>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Available Fields')}</h3>
            <div className='overflow-x-auto rounded-md border'>
              <table className='w-full text-left text-sm'>
                <thead className='bg-muted/50 text-muted-foreground'>
                  <tr>
                    <th className='px-3 py-2 font-medium'>{t('Field')}</th>
                    <th className='px-3 py-2 font-medium'>{t('Type')}</th>
                    <th className='px-3 py-2 font-medium'>
                      {t('Availability')}
                    </th>
                    <th className='px-3 py-2 font-medium'>
                      {t('Description')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {exprFieldRows.map((field) => (
                    <tr key={field.fields} className='border-t'>
                      <td className='px-3 py-2 align-top'>
                        <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
                          {field.fields}
                        </code>
                      </td>
                      <td className='px-3 py-2 align-top'>{t(field.type)}</td>
                      <td className='px-3 py-2 align-top'>{t(field.scope)}</td>
                      <td className='text-muted-foreground px-3 py-2 align-top'>
                        {t(field.description)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Operators')}</h3>
            <div className='overflow-x-auto rounded-md border'>
              <table className='w-full text-left text-sm'>
                <thead className='bg-muted/50 text-muted-foreground'>
                  <tr>
                    <th className='px-3 py-2 font-medium'>{t('Operator')}</th>
                    <th className='px-3 py-2 font-medium'>{t('Usage')}</th>
                  </tr>
                </thead>
                <tbody>
                  {exprOperatorRows.map((operator) => (
                    <tr key={operator.syntax} className='border-t'>
                      <td className='px-3 py-2 align-top'>
                        <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
                          {operator.syntax}
                        </code>
                      </td>
                      <td className='text-muted-foreground px-3 py-2 align-top'>
                        {t(operator.description)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Useful Expressions')}</h3>
            <div className='grid gap-3 md:grid-cols-2'>
              {exprExamples.map((example) => (
                <div key={example.title} className='rounded-md border p-3'>
                  <div className='font-medium'>{t(example.title)}</div>
                  <code className='bg-muted mt-2 block rounded px-2 py-1.5 font-mono text-xs break-all'>
                    {example.expression}
                  </code>
                  <p className='text-muted-foreground mt-2 text-sm'>
                    {t(example.description)}
                  </p>
                </div>
              ))}
            </div>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Safety and Limits')}</h3>
            <ul className='text-muted-foreground list-disc space-y-1 ps-5 text-sm'>
              <li>
                {t(
                  'Only the fields listed above are accepted; unknown identifiers are rejected before SQL is built.'
                )}
              </li>
              <li>
                {t(
                  'Values are bound as SQL parameters, and LIKE searches escape %, _, and ! characters.'
                )}
              </li>
              <li>
                {t(
                  'Expressions are limited to 4096 characters, string literals to 1024 characters, and in arrays to 100 items.'
                )}
              </li>
              <li>
                {t(
                  'Regex matches, arithmetic, unsupported function calls, and field-to-field comparisons are not supported.'
                )}
              </li>
            </ul>
          </section>
        </div>
      </DialogContent>
    </Dialog>
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
