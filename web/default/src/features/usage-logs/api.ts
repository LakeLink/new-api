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
import { api } from '@/lib/api'

import { buildQueryParams } from './lib/query-params'
import type {
  GetLogsParams,
  GetLogsResponse,
  GetLogStatsParams,
  GetLogStatsResponse,
  GetLogExpressionSchemaResponse,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
  LogExportFormat,
  UserInfo,
} from './types'

// ============================================================================
// Generic API Helpers
// ============================================================================

function buildApiPath(endpoint: string, isAdmin: boolean): string {
  return isAdmin ? endpoint : `${endpoint}/self`
}

async function fetchLogs<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
  signal?: AbortSignal
): Promise<GetLogsResponse> {
  const paramRecord = params as unknown as Record<string, unknown>
  const queryParams = buildQueryParams({
    p: paramRecord.p || 1,
    page_size: paramRecord.page_size || 20,
    ...params,
  })
  const path = buildApiPath(endpoint, isAdmin)
  const res = await api.get(`${path}?${queryParams}`, {
    signal,
    disableDuplicate: true,
  })
  return res.data
}

async function fetchLogStats<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
  signal?: AbortSignal
): Promise<GetLogStatsResponse> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>
  )
  const path = buildApiPath(endpoint, isAdmin)
  const res = await api.get(`${path}/stat?${queryParams}`, {
    signal,
    disableDuplicate: true,
  })
  return res.data
}

// ============================================================================
// Common Log APIs
// ============================================================================

export const getAllLogs = (params: GetLogsParams = {}, signal?: AbortSignal) =>
  fetchLogs('/api/log/', params, true, signal)

export const getUserLogs = (
  params: Omit<GetLogsParams, 'username' | 'channel'> = {},
  signal?: AbortSignal
) => fetchLogs('/api/log', params, false, signal)

export const getLogStats = (
  params: GetLogStatsParams = {},
  signal?: AbortSignal
) => fetchLogStats('/api/log', params, true, signal)

export const getUserLogStats = (
  params: Omit<GetLogStatsParams, 'username' | 'channel'> = {},
  signal?: AbortSignal
) => fetchLogStats('/api/log', params, false, signal)

export async function getLogExpressionSchema(
  signal?: AbortSignal
): Promise<GetLogExpressionSchemaResponse> {
  const res = await api.get('/api/log/expr/schema', {
    signal,
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return res.data
}

export async function exportLogs(
  format: LogExportFormat,
  params: GetLogsParams,
  isAdmin: boolean
): Promise<{ blob: Blob; filename?: string }> {
  const queryParams = buildQueryParams({ ...params, format })
  const path = isAdmin ? '/api/log/export' : '/api/log/self/export'
  const res = await api.get(`${path}?${queryParams}`, {
    responseType: 'blob',
    disableDuplicate: true,
    skipBusinessError: true,
  } as Record<string, unknown>)

  return {
    blob: res.data,
    filename: getFilenameFromContentDisposition(
      res.headers?.['content-disposition']
    ),
  }
}

function getFilenameFromContentDisposition(
  header: unknown
): string | undefined {
  if (typeof header !== 'string') return undefined
  const utf8Match = header.match(/filename\*=UTF-8''([^;]+)/i)
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1])
    } catch {
      return utf8Match[1]
    }
  }
  const quotedMatch = header.match(/filename="([^"]+)"/i)
  if (quotedMatch?.[1]) return quotedMatch[1]
  const plainMatch = header.match(/filename=([^;]+)/i)
  return plainMatch?.[1]?.trim()
}

export async function getUserInfo(
  userId: number
): Promise<{ success: boolean; message?: string; data?: UserInfo }> {
  const res = await api.get(`/api/user/${userId}`)
  return res.data
}

// ============================================================================
// MjProxy (Drawing) Logs API
// ============================================================================

export const getAllMidjourneyLogs = (
  params: GetMidjourneyLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/mj', params, true, signal)

export const getUserMidjourneyLogs = (
  params: GetMidjourneyLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/mj', params, false, signal)

// ============================================================================
// Task Logs API
// ============================================================================

export const getAllTaskLogs = (
  params: GetTaskLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/task', params, true, signal)

export const getUserTaskLogs = (
  params: GetTaskLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/task', params, false, signal)
