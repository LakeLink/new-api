/*
Copyright (C) 2025 QuantumNous

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

function expressionString(value) {
  return JSON.stringify(String(value));
}

function unixSeconds(value) {
  if (value instanceof Date) {
    return Number.isNaN(value.getTime())
      ? undefined
      : Math.floor(value.getTime() / 1000);
  }

  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return undefined;
    return Math.floor(value > 1e12 ? value / 1000 : value);
  }

  if (typeof value !== 'string' || value.trim() === '') return undefined;
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? undefined : Math.floor(timestamp / 1000);
}

function appendContains(parts, field, value) {
  if (typeof value !== 'string' || value.trim() === '') return;
  parts.push(`${field} contains ${expressionString(value.trim())}`);
}

function appendEquals(parts, field, value) {
  if (typeof value !== 'string' || value.trim() === '') return;
  parts.push(`${field} == ${expressionString(value)}`);
}

function integerFilter(value) {
  const raw = String(value ?? '');
  if (!/^[+-]?\d+$/.test(raw)) return undefined;

  try {
    const parsed = BigInt(raw);
    const minInt64 = -(2n ** 63n);
    const maxInt64 = 2n ** 63n - 1n;
    if (parsed === 0n || parsed < minInt64 || parsed > maxInt64) {
      return undefined;
    }
    return parsed.toString();
  } catch {
    return undefined;
  }
}

/**
 * Converts the Classic log field filters into an equivalent expression.
 * The caller owns the generated-vs-edited lifecycle; this function is pure so
 * both the transition behavior and literal escaping can be tested directly.
 */
export function buildLogFilterExpression(
  values,
  isAdminUser,
  defaultDateRange = [],
) {
  const parts = [];
  const range =
    Array.isArray(values?.dateRange) && values.dateRange.length === 2
      ? values.dateRange
      : defaultDateRange;
  const start = unixSeconds(range[0]);
  const end = unixSeconds(range[1]);

  if (start !== undefined) parts.push(`created_at >= ${start}`);
  if (end !== undefined) parts.push(`created_at <= ${end}`);

  const type = integerFilter(values?.logType);
  if (type !== undefined) parts.push(`type == ${type}`);

  appendContains(parts, 'token_name', values?.token_name);
  appendContains(parts, 'model_name', values?.model_name);
  appendEquals(parts, 'group', values?.group);
  appendEquals(parts, 'request_id', values?.request_id);
  appendEquals(parts, 'upstream_request_id', values?.upstream_request_id);

  if (isAdminUser) {
    appendContains(parts, 'username', values?.username);
    const channel = integerFilter(values?.channel);
    if (channel !== undefined) {
      parts.push(`channel == ${channel}`);
    }
  }

  return parts.join(' && ');
}
