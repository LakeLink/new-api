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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  evalExprLocally,
  generateExprFromVisualConfig,
  getVisualConfigForModeSwitch,
  type EstimatorOptions,
  type ExtraTokenValues,
} from './tier-expr.ts'

const extras: ExtraTokenValues = {
  cacheReadTokens: 200,
  cacheCreateTokens: 100,
  cacheCreate1hTokens: 50,
  imageTokens: 80,
  imageOutputTokens: 20,
  audioInputTokens: 40,
  audioOutputTokens: 10,
}

const openAIOptions: EstimatorOptions = {
  usageSemantics: 'openai',
  quotaPerUnit: 500_000,
  groupRatio: 1.5,
}

describe('tiered billing expression preview', () => {
  test('refuses to invent a zero-price visual config for a complex expression', () => {
    const complexExpression =
      'max(p * 2, 1) + (param("batch_size") ?? 1) * 0.25'

    assert.equal(getVisualConfigForModeSwitch(complexExpression), null)
    assert.equal(generateExprFromVisualConfig(null), 'p * 0 + c * 0')
  })

  test('creates a default visual config only for an empty expression', () => {
    const visualConfig = getVisualConfigForModeSwitch('')

    assert.notEqual(visualConfig, null)
    assert.equal(
      generateExprFromVisualConfig(visualConfig),
      'tier("base", p * 0 + c * 0)'
    )
  })

  test('matches OpenAI automatic exclusion, len, conversion, and rounding', () => {
    const result = evalExprLocally(
      'tier("base", p * 2 + c * 10 + cr * 0.5 + img * 3 + ao * 20)',
      1_000,
      500,
      extras,
      openAIOptions
    )

    assert.equal(result.error, null)
    assert.equal(result.billablePromptTokens, 720)
    assert.equal(result.billableCompletionTokens, 490)
    assert.equal(result.inputLength, 1_000)
    assert.equal(result.expressionOutput, 6_880)
    assert.equal(result.quotaBeforeGroup, 3_440)
    assert.equal(result.quotaAfterGroup, 5_160)
    assert.equal(result.matchedTier, 'base')
  })

  test('keeps Anthropic text input intact and adds cache tokens to len', () => {
    const result = evalExprLocally(
      'len > 1200 ? tier("long", p * 3 + c * 0 + cr * 0.3) : tier("short", p * 1 + c * 0)',
      1_000,
      0,
      extras,
      {
        usageSemantics: 'anthropic',
        quotaPerUnit: 500_000,
        groupRatio: 1,
      }
    )

    assert.equal(result.error, null)
    assert.equal(result.billablePromptTokens, 1_000)
    assert.equal(result.inputLength, 1_350)
    assert.equal(result.expressionOutput, 3_060)
    assert.equal(result.quotaAfterGroup, 1_530)
    assert.equal(result.matchedTier, 'long')
  })

  test('does not exclude a category unless the expression prices it separately', () => {
    const result = evalExprLocally(
      'tier("base", p * 2 + c * 4)',
      1_000,
      500,
      extras,
      openAIOptions
    )

    assert.equal(result.billablePromptTokens, 1_000)
    assert.equal(result.billableCompletionTokens, 500)
  })

  test('supports versioned expressions and half-away-from-zero rounding', () => {
    const versionedExpression = 'v1:tier("base", p * 3 + c * 0)'
    const result = evalExprLocally(
      versionedExpression,
      1,
      0,
      { ...extras, cacheReadTokens: 0 },
      {
        usageSemantics: 'openai',
        quotaPerUnit: 500_000,
        groupRatio: 1,
      }
    )

    assert.equal(result.error, null)
    assert.equal(result.quotaBeforeGroup, 1.5)
    assert.equal(result.quotaAfterGroup, 2)
    assert.equal(
      generateExprFromVisualConfig(
        getVisualConfigForModeSwitch(versionedExpression)
      ),
      versionedExpression
    )
  })

  test('rejects negative expression results like the backend runtime', () => {
    const result = evalExprLocally(
      'tier("negative", p * -0.8 + c * 0)',
      1,
      0,
      extras,
      {
        usageSemantics: 'openai',
        quotaPerUnit: 500_000,
        groupRatio: 1,
      }
    )

    assert.equal(result.error, 'negative_result')
    assert.equal(result.quotaAfterGroup, 0)
  })

  test('rejects unsupported versions instead of silently stripping them', () => {
    const expression = 'v2:tier("base", p * 1 + c * 0)'
    const result = evalExprLocally(expression, 1, 0, extras, openAIOptions)

    assert.equal(getVisualConfigForModeSwitch(expression), null)
    assert.equal(result.error, 'unsupported_expression')
    assert.equal(result.quotaAfterGroup, 0)
  })

  test('never executes arbitrary JavaScript while generating a preview', () => {
    const marker = '__newApiBillingPreviewExecuted'
    const globals = globalThis as Record<string, unknown>
    globals[marker] = false
    const result = evalExprLocally(
      `tier("base", p * 1 + c * 0); globalThis.${marker} = true`,
      1,
      0,
      extras,
      openAIOptions
    )

    assert.equal(result.error, 'unsupported_expression')
    assert.equal(globals[marker], false)
    delete globals[marker]
  })

  test('rejects an unconditional non-final tier that backend syntax rejects', () => {
    const expression =
      'tier("first", p * 1 + c * 0) : tier("second", p * 2 + c * 0)'
    const result = evalExprLocally(expression, 10, 0, extras, openAIOptions)

    assert.equal(getVisualConfigForModeSwitch(expression), null)
    assert.equal(result.error, 'unsupported_expression')
  })

  test('does not let invalid token quantities create a preview charge', () => {
    const result = evalExprLocally(
      'tier("base", p * 1 + c * 1 + cr * 1 + cc * 1 + cc1h * 1 + img * 1 + img_o * 1 + ai * 1 + ao * 1)',
      -1,
      Number.NaN,
      {
        cacheReadTokens: -1,
        cacheCreateTokens: Number.NaN,
        cacheCreate1hTokens: Number.POSITIVE_INFINITY,
        imageTokens: -1,
        imageOutputTokens: Number.NaN,
        audioInputTokens: Number.NEGATIVE_INFINITY,
        audioOutputTokens: -1,
      },
      openAIOptions
    )

    assert.equal(result.error, null)
    assert.equal(result.expressionOutput, 0)
    assert.equal(result.quotaAfterGroup, 0)
  })
})
