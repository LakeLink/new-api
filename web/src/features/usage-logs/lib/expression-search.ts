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
import type { CommonLogFilters } from '../types'
import { getDefaultTimeRange } from './time-range'

export type LogExpressionDraftOrigin = 'generated' | 'user'

export interface LogExpressionDraft {
  value: string
  origin: LogExpressionDraftOrigin
}

export type PendingLogExpressionNavigation = LogExpressionDraft

interface ResolveLogExpressionUrlChangeOptions {
  urlExpression: string | undefined
  previousUrlExpression: string | undefined
  currentOrigin: LogExpressionDraftOrigin
  pendingNavigation: PendingLogExpressionNavigation | undefined
}

interface ResolvedLogExpressionUrlChange {
  origin: LogExpressionDraftOrigin
  pendingNavigation: PendingLogExpressionNavigation | undefined
  isOwnNavigation: boolean
}

interface MergeLogExpressionUrlOptions {
  previousFilters: CommonLogFilters
  routeFilters: CommonLogFilters
  urlExpression: string | undefined
  origin: LogExpressionDraftOrigin
  isOwnNavigation: boolean
}

interface BuildLogFilterExpressionOptions {
  filters: CommonLogFilters
  logType: string
  isAdmin: boolean
  now?: Date
}

interface EnterLogExpressionModeOptions extends BuildLogFilterExpressionOptions {
  draft: LogExpressionDraft
}

function quoteStringLiteral(value: string): string {
  return JSON.stringify(value)
}

function unixSeconds(value: Date | undefined): number | undefined {
  if (!value) return undefined

  const milliseconds = value.getTime()
  return Number.isFinite(milliseconds)
    ? Math.floor(milliseconds / 1000)
    : undefined
}

function numericFilter(value: string | undefined): number | undefined {
  if (!value?.trim()) return undefined

  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed !== 0 ? parsed : undefined
}

/**
 * Convert the field-filter form into the expression language without changing
 * the semantics of the existing list API filters.
 */
export function buildLogFilterExpression(
  options: BuildLogFilterExpressionOptions
): string {
  const clauses: string[] = []
  let startDate = options.filters.startTime
  let endDate = options.filters.endTime
  if (!startDate && !endDate) {
    const defaultRange = getDefaultTimeRange(options.now)
    startDate = defaultRange.start
    endDate = defaultRange.end
  }
  const startTime = unixSeconds(startDate)
  const endTime = unixSeconds(endDate)

  if (startTime !== undefined) clauses.push(`created_at >= ${startTime}`)
  if (endTime !== undefined) clauses.push(`created_at <= ${endTime}`)

  const logType = numericFilter(options.logType)
  if (logType !== undefined) clauses.push(`type == ${logType}`)

  const model = options.filters.model?.trim()
  if (model) {
    clauses.push(`model_name contains ${quoteStringLiteral(model)}`)
  }
  const token = options.filters.token?.trim()
  if (token) {
    clauses.push(`token_name contains ${quoteStringLiteral(token)}`)
  }
  if (options.filters.group) {
    clauses.push(`group == ${quoteStringLiteral(options.filters.group)}`)
  }
  if (options.filters.requestId) {
    clauses.push(
      `request_id == ${quoteStringLiteral(options.filters.requestId)}`
    )
  }
  if (options.filters.upstreamRequestId) {
    clauses.push(
      `upstream_request_id == ${quoteStringLiteral(options.filters.upstreamRequestId)}`
    )
  }

  if (options.isAdmin) {
    const username = options.filters.username?.trim()
    if (username) {
      clauses.push(`username contains ${quoteStringLiteral(username)}`)
    }
    const channel = numericFilter(options.filters.channel)
    if (channel !== undefined) clauses.push(`channel == ${channel}`)
  }

  return clauses.join(' && ')
}

/** Keep authored expressions stable; regenerate only untouched generated drafts. */
export function enterLogExpressionMode(
  options: EnterLogExpressionModeOptions
): LogExpressionDraft {
  if (options.draft.origin === 'user') return options.draft

  return {
    origin: 'generated',
    value: buildLogFilterExpression(options),
  }
}

/** Rebuild an untouched generated draft when the effective log scope changes. */
export function regenerateLogExpressionForScopeChange(
  options: EnterLogExpressionModeOptions
): LogExpressionDraft | undefined {
  if (options.draft.origin === 'user') return undefined

  return {
    origin: 'generated',
    value: buildLogFilterExpression(options),
  }
}

/** Distinguish an in-component navigation from a URL loaded by the user. */
export function resolveLogExpressionUrlChange(
  options: ResolveLogExpressionUrlChangeOptions
): ResolvedLogExpressionUrlChange {
  const isOwnNavigation =
    !!options.urlExpression &&
    options.pendingNavigation?.value === options.urlExpression

  if (isOwnNavigation && options.pendingNavigation) {
    return {
      origin: options.pendingNavigation.origin,
      pendingNavigation: undefined,
      isOwnNavigation: true,
    }
  }

  const isNewUrlExpression =
    !!options.urlExpression &&
    options.previousUrlExpression !== options.urlExpression
  if (isNewUrlExpression) {
    return {
      origin: 'user',
      pendingNavigation: undefined,
      isOwnNavigation: false,
    }
  }

  return {
    origin: options.currentOrigin,
    pendingNavigation: options.pendingNavigation,
    isOwnNavigation: false,
  }
}

/** Merge applied URL state without discarding the field-form draft. */
export function mergeLogExpressionUrl(
  options: MergeLogExpressionUrlOptions
): CommonLogFilters {
  if (options.isOwnNavigation) {
    return { ...options.previousFilters, expr: options.urlExpression }
  }

  return {
    ...options.routeFilters,
    expr:
      options.urlExpression ??
      (options.origin === 'user' ? options.previousFilters.expr : undefined),
  }
}
