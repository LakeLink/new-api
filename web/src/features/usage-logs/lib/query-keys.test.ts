import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  canReuseUsageLogsPlaceholder,
  logExpressionSchemaQueryKey,
} from './query-keys.ts'

describe('usage log query keys', () => {
  test('scopes expression schema cache entries by user and role', () => {
    assert.notDeepEqual(
      logExpressionSchemaQueryKey(1, 10, true),
      logExpressionSchemaQueryKey(2, 10, true)
    )
    assert.notDeepEqual(
      logExpressionSchemaQueryKey(1, 10, true),
      logExpressionSchemaQueryKey(1, 1, false)
    )
  })

  test('reuses placeholders only for the same viewer and category', () => {
    const previousKey = ['logs', 'common', 1, false, { expr: 'type == 2' }]
    assert.equal(
      canReuseUsageLogsPlaceholder(previousKey, 'common', 1, false),
      true
    )
    assert.equal(
      canReuseUsageLogsPlaceholder(previousKey, 'common', 2, false),
      false
    )
    assert.equal(
      canReuseUsageLogsPlaceholder(previousKey, 'common', 1, true),
      false
    )
    assert.equal(
      canReuseUsageLogsPlaceholder(previousKey, 'task', 1, false),
      false
    )
  })
})
