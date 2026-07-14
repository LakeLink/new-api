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
import { BILLING_CACHE_VAR_MAP } from './billing-expr'

export const CACHE_MODE_TIMED = 'timed'
export const CACHE_MODE_GENERIC = 'generic'
export type CacheMode = typeof CACHE_MODE_TIMED | typeof CACHE_MODE_GENERIC

export type TierConditionInput = {
  editorId?: string
  var: 'p' | 'c' | 'len'
  op: '<' | '<=' | '>' | '>='
  value: number | string
}

export type VisualTier = {
  editorId?: string
  label: string
  conditions: TierConditionInput[]
  input_unit_cost: number
  output_unit_cost: number
  cache_mode: CacheMode
  cache_read_unit_cost?: number
  cache_create_unit_cost?: number
  cache_create_1h_unit_cost?: number
  image_unit_cost?: number
  image_output_unit_cost?: number
  audio_input_unit_cost?: number
  audio_output_unit_cost?: number
  [field: string]: unknown
}

export type VisualConfig = {
  versionPrefix?: 'v1:'
  tiers: VisualTier[]
}

let visualEditorIdSequence = 0

function nextVisualEditorId(prefix: 'condition' | 'tier'): string {
  visualEditorIdSequence += 1
  return `${prefix}-${visualEditorIdSequence}`
}

export function getTierCacheMode(
  tier: Partial<VisualTier> | null | undefined
): CacheMode {
  if (tier?.cache_mode === CACHE_MODE_TIMED) return CACHE_MODE_TIMED
  if (tier?.cache_mode === CACHE_MODE_GENERIC) return CACHE_MODE_GENERIC
  return Number(tier?.cache_create_1h_unit_cost) > 0
    ? CACHE_MODE_TIMED
    : CACHE_MODE_GENERIC
}

export function normalizeVisualTier(
  tier: Partial<VisualTier> = {}
): VisualTier {
  const conditions = Array.isArray(tier.conditions)
    ? tier.conditions.map((condition) => ({
        ...condition,
        editorId: condition.editorId ?? nextVisualEditorId('condition'),
      }))
    : []

  return {
    label: tier.label ?? '',
    input_unit_cost: Number(tier.input_unit_cost) || 0,
    output_unit_cost: Number(tier.output_unit_cost) || 0,
    cache_mode: getTierCacheMode(tier),
    ...tier,
    editorId: tier.editorId ?? nextVisualEditorId('tier'),
    conditions,
    cache_read_unit_cost: Number(tier.cache_read_unit_cost) || 0,
    cache_create_unit_cost: Number(tier.cache_create_unit_cost) || 0,
    cache_create_1h_unit_cost: Number(tier.cache_create_1h_unit_cost) || 0,
    image_unit_cost: Number(tier.image_unit_cost) || 0,
    image_output_unit_cost: Number(tier.image_output_unit_cost) || 0,
    audio_input_unit_cost: Number(tier.audio_input_unit_cost) || 0,
    audio_output_unit_cost: Number(tier.audio_output_unit_cost) || 0,
  }
}

export function createDefaultVisualConfig(): VisualConfig {
  return {
    tiers: [
      normalizeVisualTier({
        conditions: [],
        input_unit_cost: 0,
        output_unit_cost: 0,
        label: 'base',
        cache_mode: CACHE_MODE_GENERIC,
      }),
    ],
  }
}

export function normalizeVisualConfig(
  config: VisualConfig | null | undefined
): VisualConfig {
  if (!config || !Array.isArray(config.tiers) || config.tiers.length === 0) {
    return createDefaultVisualConfig()
  }
  return {
    ...config,
    tiers: config.tiers.map((tier) => normalizeVisualTier(tier)),
  }
}

function buildConditionStr(conditions: TierConditionInput[]): string {
  if (!conditions || conditions.length === 0) return ''
  return conditions
    .filter((c) => c.var && c.op && c.value != null && c.value !== '')
    .map((c) => `${c.var} ${c.op} ${c.value}`)
    .join(' && ')
}

function buildTierBodyExpr(tier: VisualTier): string {
  const parts: string[] = []
  const ic = Number(tier.input_unit_cost) || 0
  const oc = Number(tier.output_unit_cost) || 0
  parts.push(`p * ${ic}`)
  parts.push(`c * ${oc}`)
  for (const cv of BILLING_CACHE_VAR_MAP) {
    const v = Number((tier as Record<string, unknown>)[cv.field]) || 0
    if (v !== 0) parts.push(`${cv.exprVar} * ${v}`)
  }
  return parts.join(' + ')
}

export function generateExprFromVisualConfig(
  config: VisualConfig | null | undefined
): string {
  if (!config || !config.tiers || config.tiers.length === 0) {
    return 'p * 0 + c * 0'
  }
  const versionPrefix = config.versionPrefix ?? ''
  const tiers = config.tiers

  if (tiers.length === 1) {
    const tier = tiers[0]
    const label = tier.label || 'default'
    const body = `tier("${label}", ${buildTierBodyExpr(tier)})`
    const cond = buildConditionStr(tier.conditions)
    if (cond) {
      return `${versionPrefix}${cond} ? ${body} : p * 0 + c * 0`
    }
    return `${versionPrefix}${body}`
  }

  const parts: string[] = []
  for (let i = 0; i < tiers.length; i++) {
    const tier = tiers[i]
    const label = tier.label || `tier_${i + 1}`
    const body = `tier("${label}", ${buildTierBodyExpr(tier)})`
    const cond = buildConditionStr(tier.conditions)

    if (i < tiers.length - 1 && cond) {
      parts.push(`${cond} ? ${body}`)
    } else {
      parts.push(body)
    }
  }
  return `${versionPrefix}${parts.join(' : ')}`
}

export function tryParseVisualConfig(
  exprStr: string | null | undefined
): VisualConfig | null {
  if (!exprStr) return null
  try {
    let body = exprStr
    let versionPrefix: VisualConfig['versionPrefix']
    const versionMatch = body.match(/^v(\d+):([\s\S]*)$/)
    if (versionMatch) {
      if (versionMatch[1] !== '1') return null
      versionPrefix = 'v1:'
      body = versionMatch[2]
    }
    const cacheVarNames = BILLING_CACHE_VAR_MAP.map((cv) => cv.exprVar)
    const optCacheStr = cacheVarNames
      .map((v) => `(?:\\s*\\+\\s*${v}\\s*\\*\\s*([\\d.eE+-]+))?`)
      .join('')

    const bodyPat = `p\\s*\\*\\s*([\\d.eE+-]+)\\s*\\+\\s*c\\s*\\*\\s*([\\d.eE+-]+)${optCacheStr}`

    const singleRe = new RegExp(`^tier\\("([^"]*)",\\s*${bodyPat}\\)$`)
    const simple = body.match(singleRe)
    if (simple) {
      const tier: Record<string, unknown> = {
        conditions: [],
        input_unit_cost: Number(simple[2]),
        output_unit_cost: Number(simple[3]),
        label: simple[1],
      }
      BILLING_CACHE_VAR_MAP.forEach((cv, i) => {
        const val = simple[4 + i]
        if (val != null) tier[cv.field] = Number(val)
      })
      const config = normalizeVisualConfig({
        versionPrefix,
        tiers: [normalizeVisualTier(tier as Partial<VisualTier>)],
      })
      if (
        generateExprFromVisualConfig(config).replaceAll(/\s+/g, '') !==
        exprStr.replaceAll(/\s+/g, '')
      ) {
        return null
      }
      return config
    }

    const condGroup =
      `((?:(?:p|c|len)\\s*(?:<|<=|>|>=)\\s*[\\d.eE+]+)` +
      `(?:\\s*&&\\s*(?:p|c|len)\\s*(?:<|<=|>|>=)\\s*[\\d.eE+]+)*)`
    const tierRe = new RegExp(
      `(?:${condGroup}\\s*\\?\\s*)?tier\\("([^"]*)",\\s*${bodyPat}\\)`,
      'g'
    )
    const tiers: VisualTier[] = []
    let match: RegExpExecArray | null
    while ((match = tierRe.exec(body)) !== null) {
      const condStr = match[1] || ''
      const conditions: TierConditionInput[] = []
      if (condStr) {
        for (const cp of condStr.split(/\s*&&\s*/)) {
          const cm = cp.trim().match(/^(p|c|len)\s*(<|<=|>|>=)\s*([\d.eE+]+)$/)
          if (cm) {
            conditions.push({
              var: cm[1] as TierConditionInput['var'],
              op: cm[2] as TierConditionInput['op'],
              value: Number(cm[3]),
            })
          }
        }
      }
      const tier: Record<string, unknown> = {
        conditions,
        input_unit_cost: Number(match[3]),
        output_unit_cost: Number(match[4]),
        label: match[2],
      }
      const m = match
      BILLING_CACHE_VAR_MAP.forEach((cv, i) => {
        const val = m[5 + i]
        if (val != null) tier[cv.field] = Number(val)
      })
      tiers.push(normalizeVisualTier(tier as Partial<VisualTier>))
    }
    if (tiers.length === 0) return null
    if (tiers.slice(0, -1).some((tier) => tier.conditions.length === 0)) {
      return null
    }

    const cfg = normalizeVisualConfig({ versionPrefix, tiers })
    const regenerated = generateExprFromVisualConfig(cfg)
    if (regenerated.replaceAll(/\s+/g, '') !== exprStr.replaceAll(/\s+/g, '')) {
      return null
    }
    return cfg
  } catch {
    return null
  }
}

export function getVisualConfigForModeSwitch(
  exprStr: string | null | undefined
): VisualConfig | null {
  if (!exprStr?.trim()) {
    return createDefaultVisualConfig()
  }
  return tryParseVisualConfig(exprStr)
}

// ---------------------------------------------------------------------------
// Local cost evaluator (for the estimator preview)
// ---------------------------------------------------------------------------

const ESTIMATOR_VARS = [
  { var: 'cr', stateKey: 'cacheReadTokens' },
  { var: 'cc', stateKey: 'cacheCreateTokens' },
  { var: 'cc1h', stateKey: 'cacheCreate1hTokens' },
  { var: 'img', stateKey: 'imageTokens' },
  { var: 'img_o', stateKey: 'imageOutputTokens' },
  { var: 'ai', stateKey: 'audioInputTokens' },
  { var: 'ao', stateKey: 'audioOutputTokens' },
] as const

export type ExtraTokenValues = Record<
  (typeof ESTIMATOR_VARS)[number]['stateKey'],
  number
>

export type EvalResult = {
  expressionOutput: number
  quotaBeforeGroup: number
  quotaAfterGroup: number
  billablePromptTokens: number
  billableCompletionTokens: number
  inputLength: number
  quotaClamped: boolean
  matchedTier: string
  error: EvalErrorCode | null
}

export type EvalErrorCode =
  | 'unsupported_expression'
  | 'non_finite_result'
  | 'negative_result'
  | 'invalid_result'

export type UsageSemantics = 'openai' | 'anthropic'

export type EstimatorOptions = {
  usageSemantics: UsageSemantics
  quotaPerUnit: number
  groupRatio: number
}

const MAX_QUOTA = 2_147_483_647
const MIN_QUOTA = -2_147_483_648

function expressionUsesVariable(exprStr: string, variable: string): boolean {
  const withoutStrings = exprStr.replaceAll(/"(?:\\.|[^"\\])*"/g, '')
  return new RegExp(`\\b${variable}\\b`).test(withoutStrings)
}

function quotaRound(value: number): { value: number; clamped: boolean } {
  if (Number.isNaN(value)) {
    return { value: 0, clamped: true }
  }

  const rounded = Math.sign(value) * Math.floor(Math.abs(value) + 0.5)
  if (Object.is(rounded, -0)) {
    return { value: 0, clamped: false }
  }
  if (rounded >= MAX_QUOTA) {
    return { value: MAX_QUOTA, clamped: true }
  }
  if (rounded <= MIN_QUOTA) {
    return { value: MIN_QUOTA, clamped: true }
  }
  return { value: rounded, clamped: false }
}

function visualTierMatches(
  tier: VisualTier,
  variables: Record<TierConditionInput['var'], number>
): boolean {
  return tier.conditions.every((condition) => {
    const left = variables[condition.var]
    const right = Number(condition.value)
    if (!Number.isFinite(right)) return false
    switch (condition.op) {
      case '<':
        return left < right
      case '<=':
        return left <= right
      case '>':
        return left > right
      case '>=':
        return left >= right
    }
  })
}

function evaluateVisualTier(
  tier: VisualTier,
  variables: Record<string, number>
): number {
  let value =
    variables.p * Number(tier.input_unit_cost) +
    variables.c * Number(tier.output_unit_cost)
  for (const cacheVariable of BILLING_CACHE_VAR_MAP) {
    value +=
      variables[cacheVariable.exprVar] *
      Number((tier as Record<string, unknown>)[cacheVariable.field] ?? 0)
  }
  return value
}

function evaluateVisualConfig(
  config: VisualConfig,
  variables: Record<string, number>
): { expressionOutput: number; matchedTier: string } {
  const tiers = config.tiers
  if (tiers.length === 1) {
    const tier = tiers[0]
    if (
      tier.conditions.length > 0 &&
      !visualTierMatches(tier, {
        p: variables.p,
        c: variables.c,
        len: variables.len,
      })
    ) {
      return { expressionOutput: 0, matchedTier: '' }
    }
    return {
      expressionOutput: evaluateVisualTier(tier, variables),
      matchedTier: tier.label || 'default',
    }
  }

  for (let index = 0; index < tiers.length; index += 1) {
    const tier = tiers[index]
    const isFallback =
      index === tiers.length - 1 || tier.conditions.length === 0
    if (
      isFallback ||
      visualTierMatches(tier, {
        p: variables.p,
        c: variables.c,
        len: variables.len,
      })
    ) {
      return {
        expressionOutput: evaluateVisualTier(tier, variables),
        matchedTier: tier.label || `tier_${index + 1}`,
      }
    }
  }

  return { expressionOutput: 0, matchedTier: '' }
}

export function evalExprLocally(
  exprStr: string,
  promptTokens: number,
  completionTokens: number,
  extraTokenValues: ExtraTokenValues,
  options: EstimatorOptions
): EvalResult {
  let errorCode: EvalErrorCode = 'invalid_result'
  try {
    if (!exprStr || !exprStr.trim()) {
      return {
        expressionOutput: 0,
        quotaBeforeGroup: 0,
        quotaAfterGroup: 0,
        billablePromptTokens: 0,
        billableCompletionTokens: 0,
        inputLength: 0,
        quotaClamped: false,
        matchedTier: '',
        error: null,
      }
    }
    const visualConfig = tryParseVisualConfig(exprStr)
    if (!visualConfig) {
      errorCode = 'unsupported_expression'
      throw new Error(errorCode)
    }
    const expressionBody = exprStr.startsWith('v1:')
      ? exprStr.slice(3)
      : exprStr
    const normalizedPromptTokens = Number.isFinite(promptTokens)
      ? Math.max(0, promptTokens)
      : 0
    const normalizedCompletionTokens = Number.isFinite(completionTokens)
      ? Math.max(0, completionTokens)
      : 0
    const normalizedExtraTokenValues: ExtraTokenValues = {
      cacheReadTokens: Number.isFinite(extraTokenValues.cacheReadTokens)
        ? Math.max(0, extraTokenValues.cacheReadTokens)
        : 0,
      cacheCreateTokens: Number.isFinite(extraTokenValues.cacheCreateTokens)
        ? Math.max(0, extraTokenValues.cacheCreateTokens)
        : 0,
      cacheCreate1hTokens: Number.isFinite(extraTokenValues.cacheCreate1hTokens)
        ? Math.max(0, extraTokenValues.cacheCreate1hTokens)
        : 0,
      imageTokens: Number.isFinite(extraTokenValues.imageTokens)
        ? Math.max(0, extraTokenValues.imageTokens)
        : 0,
      imageOutputTokens: Number.isFinite(extraTokenValues.imageOutputTokens)
        ? Math.max(0, extraTokenValues.imageOutputTokens)
        : 0,
      audioInputTokens: Number.isFinite(extraTokenValues.audioInputTokens)
        ? Math.max(0, extraTokenValues.audioInputTokens)
        : 0,
      audioOutputTokens: Number.isFinite(extraTokenValues.audioOutputTokens)
        ? Math.max(0, extraTokenValues.audioOutputTokens)
        : 0,
    }
    const {
      cacheReadTokens,
      cacheCreateTokens,
      cacheCreate1hTokens,
      imageTokens,
      imageOutputTokens,
      audioInputTokens,
      audioOutputTokens,
    } = normalizedExtraTokenValues

    let billablePromptTokens = normalizedPromptTokens
    let billableCompletionTokens = normalizedCompletionTokens
    if (options.usageSemantics === 'openai') {
      if (expressionUsesVariable(expressionBody, 'cr')) {
        billablePromptTokens -= cacheReadTokens
      }
      if (expressionUsesVariable(expressionBody, 'cc')) {
        billablePromptTokens -= cacheCreateTokens
      }
      if (expressionUsesVariable(expressionBody, 'cc1h')) {
        billablePromptTokens -= cacheCreate1hTokens
      }
      if (expressionUsesVariable(expressionBody, 'img')) {
        billablePromptTokens -= imageTokens
      }
      if (expressionUsesVariable(expressionBody, 'ai')) {
        billablePromptTokens -= audioInputTokens
      }
      if (expressionUsesVariable(expressionBody, 'img_o')) {
        billableCompletionTokens -= imageOutputTokens
      }
      if (expressionUsesVariable(expressionBody, 'ao')) {
        billableCompletionTokens -= audioOutputTokens
      }
    }
    billablePromptTokens = Math.max(0, billablePromptTokens)
    billableCompletionTokens = Math.max(0, billableCompletionTokens)

    const inputLength =
      options.usageSemantics === 'anthropic'
        ? normalizedPromptTokens +
          cacheReadTokens +
          cacheCreateTokens +
          cacheCreate1hTokens
        : normalizedPromptTokens
    const variables: Record<string, number> = {
      p: billablePromptTokens,
      c: billableCompletionTokens,
      len: inputLength,
    }
    for (const field of ESTIMATOR_VARS) {
      variables[field.var] = normalizedExtraTokenValues[field.stateKey]
    }
    const { expressionOutput, matchedTier } = evaluateVisualConfig(
      visualConfig,
      variables
    )
    if (!Number.isFinite(expressionOutput)) {
      errorCode = 'non_finite_result'
      throw new Error(errorCode)
    }
    if (expressionOutput < 0) {
      errorCode = 'negative_result'
      throw new Error(errorCode)
    }
    const quotaPerUnit = Number.isFinite(options.quotaPerUnit)
      ? options.quotaPerUnit
      : 0
    const groupRatio = Number.isFinite(options.groupRatio)
      ? options.groupRatio
      : 0
    const quotaBeforeGroup = (expressionOutput / 1_000_000) * quotaPerUnit
    const roundedQuota = quotaRound(quotaBeforeGroup * groupRatio)

    return {
      expressionOutput,
      quotaBeforeGroup,
      quotaAfterGroup: roundedQuota.value,
      billablePromptTokens,
      billableCompletionTokens,
      inputLength,
      quotaClamped: roundedQuota.clamped,
      matchedTier,
      error: null,
    }
  } catch {
    return {
      expressionOutput: 0,
      quotaBeforeGroup: 0,
      quotaAfterGroup: 0,
      billablePromptTokens: 0,
      billableCompletionTokens: 0,
      inputLength: 0,
      quotaClamped: false,
      matchedTier: '',
      error: errorCode,
    }
  }
}

export function exprUsesExtraVars(exprStr: string): boolean {
  if (!exprStr) return false
  const varNames = ESTIMATOR_VARS.map((f) => f.var).join('|')
  return new RegExp(`\\b(${varNames})\\b`).test(exprStr)
}

export const ESTIMATOR_EXTRA_FIELDS = ESTIMATOR_VARS
