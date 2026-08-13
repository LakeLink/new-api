import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  buildLogFilterExpression,
  enterLogExpressionMode,
  mergeLogExpressionUrl,
  regenerateLogExpressionForScopeChange,
  resolveLogExpressionUrlChange,
} from './expression-search.ts'

const fixedNow = new Date('2026-07-11T12:34:56Z')
const fixedDayStart = new Date(fixedNow)
fixedDayStart.setHours(0, 0, 0, 0)
const defaultTimeExpression = `created_at >= ${Math.floor(fixedDayStart.getTime() / 1000)} && created_at <= ${Math.floor(fixedNow.getTime() / 1000) + 3600}`

describe('log field filter expression conversion', () => {
  test('preserves the field-filter API semantics for every supported filter', () => {
    assert.equal(
      buildLogFilterExpression({
        filters: {
          startTime: new Date('2026-07-11T00:00:00.900Z'),
          endTime: new Date('2026-07-11T01:02:03.999Z'),
          model: '  gpt  ',
          token: ' primary ',
          group: ' vip ',
          username: ' alice ',
          channel: '12',
          requestId: ' req_123 ',
          upstreamRequestId: 'upstream_456',
        },
        logType: '2',
        isAdmin: true,
      }),
      'created_at >= 1783728000 && created_at <= 1783731723 && type == 2 && model_name contains "gpt" && token_name contains "primary" && group == " vip " && request_id == " req_123 " && upstream_request_id == "upstream_456" && username contains "alice" && channel == 12'
    )
  })

  test('quotes string values without allowing expression injection', () => {
    assert.equal(
      buildLogFilterExpression({
        filters: {
          model: 'gpt" || type == 5 || model_name == "',
          group: 'line\\break\nnext',
        },
        logType: '0',
        isAdmin: false,
        now: fixedNow,
      }),
      `${defaultTimeExpression} && model_name contains "gpt\\" || type == 5 || model_name == \\"" && group == "line\\\\break\\nnext"`
    )
  })

  test('does not expose admin-only filters in a self-user expression', () => {
    assert.equal(
      buildLogFilterExpression({
        filters: { username: 'alice', channel: '12', model: 'claude' },
        logType: '0',
        isAdmin: false,
        now: fixedNow,
      }),
      `${defaultTimeExpression} && model_name contains "claude"`
    )
  })

  test('uses the list API default time range only when both dates are empty', () => {
    assert.equal(
      buildLogFilterExpression({
        filters: {},
        logType: '0',
        isAdmin: false,
        now: fixedNow,
      }),
      defaultTimeExpression
    )

    assert.equal(
      buildLogFilterExpression({
        filters: { endTime: new Date('2026-07-12T00:00:00Z') },
        logType: '0',
        isAdmin: false,
        now: fixedNow,
      }),
      'created_at <= 1783814400'
    )
  })

  test('regenerates untouched drafts but preserves user-authored drafts', () => {
    const generated = enterLogExpressionMode({
      draft: { origin: 'generated', value: 'old generated value' },
      filters: { model: 'gpt' },
      logType: '2',
      isAdmin: false,
      now: fixedNow,
    })
    assert.deepEqual(generated, {
      origin: 'generated',
      value: `${defaultTimeExpression} && type == 2 && model_name contains "gpt"`,
    })

    const user = enterLogExpressionMode({
      draft: { origin: 'user', value: 'quota > 1000' },
      filters: { model: 'gpt' },
      logType: '2',
      isAdmin: false,
    })
    assert.deepEqual(user, { origin: 'user', value: 'quota > 1000' })
  })

  test('regenerates only untouched drafts when the admin view scope changes', () => {
    const filters = {
      startTime: new Date('2026-07-11T00:00:00Z'),
      endTime: new Date('2026-07-11T01:00:00Z'),
      username: 'alice',
      channel: '12',
      model: 'gpt',
    }

    assert.deepEqual(
      regenerateLogExpressionForScopeChange({
        draft: {
          origin: 'generated',
          value:
            'created_at >= 1783728000 && username contains "alice" && channel == 12',
        },
        filters,
        logType: '2',
        isAdmin: false,
      }),
      {
        origin: 'generated',
        value:
          'created_at >= 1783728000 && created_at <= 1783731600 && type == 2 && model_name contains "gpt"',
      }
    )

    assert.equal(
      regenerateLogExpressionForScopeChange({
        draft: { origin: 'user', value: 'username == "alice"' },
        filters,
        logType: '2',
        isAdmin: false,
      }),
      undefined
    )
  })

  test('preserves field state only for the expression navigation it initiated', () => {
    assert.deepEqual(
      resolveLogExpressionUrlChange({
        urlExpression: 'type == 2',
        previousUrlExpression: undefined,
        currentOrigin: 'generated',
        pendingNavigation: { origin: 'generated', value: 'type == 2' },
      }),
      {
        origin: 'generated',
        pendingNavigation: undefined,
        isOwnNavigation: true,
      }
    )

    assert.deepEqual(
      resolveLogExpressionUrlChange({
        urlExpression: 'type == 2',
        previousUrlExpression: undefined,
        currentOrigin: 'generated',
        pendingNavigation: undefined,
      }),
      {
        origin: 'user',
        pendingNavigation: undefined,
        isOwnNavigation: false,
      }
    )
  })

  test('keeps field and time drafts across its own expression URL update', () => {
    const startTime = new Date('2026-07-11T00:00:00Z')
    const endTime = new Date('2026-07-11T01:00:00Z')
    assert.deepEqual(
      mergeLogExpressionUrl({
        previousFilters: {
          startTime,
          endTime,
          model: 'gpt',
          group: 'vip',
          expr: 'generated expression',
        },
        routeFilters: {},
        urlExpression: 'generated expression',
        origin: 'generated',
        isOwnNavigation: true,
      }),
      {
        startTime,
        endTime,
        model: 'gpt',
        group: 'vip',
        expr: 'generated expression',
      }
    )
  })

  test('never overwrites a user-authored expression when field URLs change', () => {
    assert.deepEqual(
      mergeLogExpressionUrl({
        previousFilters: { model: 'old', expr: 'quota > 1000' },
        routeFilters: { model: 'new' },
        urlExpression: undefined,
        origin: 'user',
        isOwnNavigation: false,
      }),
      { model: 'new', expr: 'quota > 1000' }
    )
  })
})
