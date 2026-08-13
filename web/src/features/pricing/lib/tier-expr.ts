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
  var: 'p' | 'c' | 'len'
  op: '<' | '<=' | '>' | '>='
  value: number | string
}

export type VisualTier = {
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
  tiers: VisualTier[]
  version?: 'v1'
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
  return {
    label: tier.label ?? '',
    input_unit_cost: Number(tier.input_unit_cost) || 0,
    output_unit_cost: Number(tier.output_unit_cost) || 0,
    cache_mode: getTierCacheMode(tier),
    conditions: Array.isArray(tier.conditions) ? tier.conditions : [],
    ...tier,
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
  const tiers = config.tiers

  if (tiers.length === 1) {
    const tier = tiers[0]
    const label = tier.label || 'default'
    const body = `tier("${label}", ${buildTierBodyExpr(tier)})`
    const cond = buildConditionStr(tier.conditions)
    if (cond) {
      const expr = `${cond} ? ${body} : p * 0 + c * 0`
      return config.version ? `${config.version}:${expr}` : expr
    }
    return config.version ? `${config.version}:${body}` : body
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
  const expr = parts.join(' : ')
  return config.version ? `${config.version}:${expr}` : expr
}

export function tryParseVisualConfig(
  exprStr: string | null | undefined
): VisualConfig | null {
  if (!exprStr) return null
  try {
    let body = exprStr
    let version: VisualConfig['version']
    const versionMatch = body.match(/^v(\d+):([\s\S]*)$/)
    if (versionMatch) {
      if (versionMatch[1] !== '1') return null
      version = 'v1'
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
      return normalizeVisualConfig({
        version,
        tiers: [normalizeVisualTier(tier as Partial<VisualTier>)],
      })
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
    if (
      tiers.length > 1 &&
      tiers.slice(0, -1).some((tier) => tier.conditions.length === 0)
    ) {
      return null
    }

    const cfg = normalizeVisualConfig({ version, tiers })
    const regenerated = generateExprFromVisualConfig(cfg)
    if (regenerated.replace(/\s+/g, '') !== exprStr.replace(/\s+/g, '')) {
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
  if (!exprStr || !exprStr.trim()) return createDefaultVisualConfig()
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

export type EstimatorOptions = {
  usageSemantics?: 'openai' | 'anthropic'
  quotaPerUnit?: number
  groupRatio?: number
}

export type EvalResult = {
  cost: number
  matchedTier: string
  error: string | null
  billablePromptTokens: number
  billableCompletionTokens: number
  inputLength: number
  expressionOutput: number
  quotaBeforeGroup: number
  quotaAfterGroup: number
}

type ExprToken =
  | { type: 'number'; value: number }
  | { type: 'string'; value: string }
  | { type: 'identifier'; value: string }
  | { type: 'operator'; value: string }
  | { type: 'eof' }

type ExprNode =
  | { kind: 'number'; value: number }
  | { kind: 'string'; value: string }
  | { kind: 'identifier'; name: string }
  | { kind: 'unary'; operator: string; argument: ExprNode }
  | { kind: 'binary'; operator: string; left: ExprNode; right: ExprNode }
  | {
      kind: 'conditional'
      condition: ExprNode
      whenTrue: ExprNode
      whenFalse: ExprNode
    }
  | { kind: 'call'; name: string; args: ExprNode[] }

type EvalValue = number | string | boolean | null

const EVAL_ERROR_UNSUPPORTED = 'unsupported_expression'
const EVAL_ERROR_NEGATIVE = 'negative_result'
const NUMERIC_TOKEN_REGEX = /^(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?/
const IDENTIFIER_TOKEN_REGEX = /^[A-Za-z_][A-Za-z0-9_]*/

function createEvalResult(
  error: string | null,
  values: Partial<Omit<EvalResult, 'error'>> = {}
): EvalResult {
  return {
    cost: values.cost ?? 0,
    matchedTier: values.matchedTier ?? '',
    error,
    billablePromptTokens: values.billablePromptTokens ?? 0,
    billableCompletionTokens: values.billableCompletionTokens ?? 0,
    inputLength: values.inputLength ?? 0,
    expressionOutput: values.expressionOutput ?? 0,
    quotaBeforeGroup: values.quotaBeforeGroup ?? 0,
    quotaAfterGroup: values.quotaAfterGroup ?? 0,
  }
}

function tokenizeExpression(input: string): ExprToken[] {
  const tokens: ExprToken[] = []
  let index = 0
  while (index < input.length) {
    const rest = input.slice(index)
    const char = input[index]
    if (/\s/.test(char)) {
      index += 1
      continue
    }
    if (char === '"') {
      let end = index + 1
      let escaped = false
      while (end < input.length) {
        const current = input[end]
        if (current === '"' && !escaped) break
        escaped = current === '\\' && !escaped
        if (current !== '\\') escaped = false
        end += 1
      }
      if (end >= input.length) throw new Error(EVAL_ERROR_UNSUPPORTED)
      const raw = input.slice(index, end + 1)
      try {
        tokens.push({ type: 'string', value: JSON.parse(raw) as string })
      } catch {
        throw new Error(EVAL_ERROR_UNSUPPORTED)
      }
      index = end + 1
      continue
    }

    const numberMatch = rest.match(NUMERIC_TOKEN_REGEX)
    if (numberMatch) {
      const value = Number(numberMatch[0])
      if (!Number.isFinite(value)) throw new Error(EVAL_ERROR_UNSUPPORTED)
      tokens.push({ type: 'number', value })
      index += numberMatch[0].length
      continue
    }

    const identifierMatch = rest.match(IDENTIFIER_TOKEN_REGEX)
    if (identifierMatch) {
      tokens.push({ type: 'identifier', value: identifierMatch[0] })
      index += identifierMatch[0].length
      continue
    }

    const operator = [
      '&&',
      '||',
      '<=',
      '>=',
      '==',
      '!=',
      '+',
      '-',
      '*',
      '/',
      '%',
      '<',
      '>',
      '?',
      ':',
      '(',
      ')',
      ',',
      '!',
    ].find((candidate) => rest.startsWith(candidate))
    if (!operator) throw new Error(EVAL_ERROR_UNSUPPORTED)
    tokens.push({ type: 'operator', value: operator })
    index += operator.length
  }
  tokens.push({ type: 'eof' })
  return tokens
}

class ExpressionParser {
  private position = 0

  constructor(private readonly tokens: ExprToken[]) {}

  parse(): ExprNode {
    const expr = this.parseConditional()
    if (this.peek().type !== 'eof') throw new Error(EVAL_ERROR_UNSUPPORTED)
    return expr
  }

  private peek(): ExprToken {
    return this.tokens[this.position]
  }

  private consumeOperator(operator: string): boolean {
    const token = this.peek()
    if (token.type === 'operator' && token.value === operator) {
      this.position += 1
      return true
    }
    return false
  }

  private expectOperator(operator: string): void {
    if (!this.consumeOperator(operator)) throw new Error(EVAL_ERROR_UNSUPPORTED)
  }

  private parseConditional(): ExprNode {
    const condition = this.parseLogicalOr()
    if (!this.consumeOperator('?')) return condition
    const whenTrue = this.parseConditional()
    this.expectOperator(':')
    const whenFalse = this.parseConditional()
    return { kind: 'conditional', condition, whenTrue, whenFalse }
  }

  private parseLogicalOr(): ExprNode {
    let expr = this.parseLogicalAnd()
    while (this.consumeOperator('||')) {
      expr = {
        kind: 'binary',
        operator: '||',
        left: expr,
        right: this.parseLogicalAnd(),
      }
    }
    return expr
  }

  private parseLogicalAnd(): ExprNode {
    let expr = this.parseEquality()
    while (this.consumeOperator('&&')) {
      expr = {
        kind: 'binary',
        operator: '&&',
        left: expr,
        right: this.parseEquality(),
      }
    }
    return expr
  }

  private parseEquality(): ExprNode {
    let expr = this.parseComparison()
    while (true) {
      if (this.consumeOperator('==')) {
        expr = {
          kind: 'binary',
          operator: '==',
          left: expr,
          right: this.parseComparison(),
        }
        continue
      }
      if (this.consumeOperator('!=')) {
        expr = {
          kind: 'binary',
          operator: '!=',
          left: expr,
          right: this.parseComparison(),
        }
        continue
      }
      return expr
    }
  }

  private parseComparison(): ExprNode {
    let expr = this.parseAdditive()
    while (true) {
      const token = this.peek()
      if (
        token.type !== 'operator' ||
        !['<', '<=', '>', '>='].includes(token.value)
      ) {
        return expr
      }
      this.position += 1
      expr = {
        kind: 'binary',
        operator: token.value,
        left: expr,
        right: this.parseAdditive(),
      }
    }
  }

  private parseAdditive(): ExprNode {
    let expr = this.parseMultiplicative()
    while (true) {
      if (this.consumeOperator('+')) {
        expr = {
          kind: 'binary',
          operator: '+',
          left: expr,
          right: this.parseMultiplicative(),
        }
        continue
      }
      if (this.consumeOperator('-')) {
        expr = {
          kind: 'binary',
          operator: '-',
          left: expr,
          right: this.parseMultiplicative(),
        }
        continue
      }
      return expr
    }
  }

  private parseMultiplicative(): ExprNode {
    let expr = this.parseUnary()
    while (true) {
      if (this.consumeOperator('*')) {
        expr = {
          kind: 'binary',
          operator: '*',
          left: expr,
          right: this.parseUnary(),
        }
        continue
      }
      if (this.consumeOperator('/')) {
        expr = {
          kind: 'binary',
          operator: '/',
          left: expr,
          right: this.parseUnary(),
        }
        continue
      }
      if (this.consumeOperator('%')) {
        expr = {
          kind: 'binary',
          operator: '%',
          left: expr,
          right: this.parseUnary(),
        }
        continue
      }
      return expr
    }
  }

  private parseUnary(): ExprNode {
    for (const operator of ['+', '-', '!']) {
      if (this.consumeOperator(operator)) {
        return { kind: 'unary', operator, argument: this.parseUnary() }
      }
    }
    return this.parsePrimary()
  }

  private parsePrimary(): ExprNode {
    const token = this.peek()
    if (token.type === 'number') {
      this.position += 1
      return { kind: 'number', value: token.value }
    }
    if (token.type === 'string') {
      this.position += 1
      return { kind: 'string', value: token.value }
    }
    if (token.type === 'identifier') {
      this.position += 1
      if (!this.consumeOperator('(')) {
        return { kind: 'identifier', name: token.value }
      }
      const args: ExprNode[] = []
      if (!this.consumeOperator(')')) {
        do {
          args.push(this.parseConditional())
        } while (this.consumeOperator(','))
        this.expectOperator(')')
      }
      return { kind: 'call', name: token.value, args }
    }
    if (this.consumeOperator('(')) {
      const expr = this.parseConditional()
      this.expectOperator(')')
      return expr
    }
    throw new Error(EVAL_ERROR_UNSUPPORTED)
  }
}

function collectIdentifiers(node: ExprNode, output = new Set<string>()) {
  switch (node.kind) {
    case 'identifier':
      output.add(node.name)
      break
    case 'unary':
      collectIdentifiers(node.argument, output)
      break
    case 'binary':
      collectIdentifiers(node.left, output)
      collectIdentifiers(node.right, output)
      break
    case 'conditional':
      collectIdentifiers(node.condition, output)
      collectIdentifiers(node.whenTrue, output)
      collectIdentifiers(node.whenFalse, output)
      break
    case 'call':
      node.args.forEach((arg) => collectIdentifiers(arg, output))
      break
  }
  return output
}

function safeTokenValue(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0
}

function toNumber(value: EvalValue): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'boolean') return value ? 1 : 0
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  throw new Error(EVAL_ERROR_UNSUPPORTED)
}

function toBoolean(value: EvalValue): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return Number.isFinite(value) && value !== 0
  if (typeof value === 'string') return value.length > 0
  return false
}

function normalizeNumericOutput(value: number): number {
  if (!Number.isFinite(value)) throw new Error(EVAL_ERROR_UNSUPPORTED)
  return value
}

function roundHalfAwayFromZero(value: number): number {
  if (!Number.isFinite(value)) return 0
  if (value < 0) return Math.ceil(value - 0.5)
  return Math.floor(value + 0.5)
}

function stripSupportedVersion(exprStr: string): string {
  const versionMatch = exprStr.match(/^v(\d+):([\s\S]*)$/)
  if (!versionMatch) return exprStr
  if (versionMatch[1] !== '1') throw new Error(EVAL_ERROR_UNSUPPORTED)
  return versionMatch[2]
}

function evalNode(
  node: ExprNode,
  env: Record<string, EvalValue>,
  state: { matchedTier: string }
): EvalValue {
  switch (node.kind) {
    case 'number':
      return node.value
    case 'string':
      return node.value
    case 'identifier':
      if (node.name === 'true') return true
      if (node.name === 'false') return false
      if (node.name === 'nil') return null
      if (Object.hasOwn(env, node.name)) return env[node.name]
      throw new Error(EVAL_ERROR_UNSUPPORTED)
    case 'unary': {
      const value = evalNode(node.argument, env, state)
      if (node.operator === '+') return toNumber(value)
      if (node.operator === '-') return -toNumber(value)
      if (node.operator === '!') return !toBoolean(value)
      throw new Error(EVAL_ERROR_UNSUPPORTED)
    }
    case 'binary': {
      if (node.operator === '&&') {
        return (
          toBoolean(evalNode(node.left, env, state)) &&
          toBoolean(evalNode(node.right, env, state))
        )
      }
      if (node.operator === '||') {
        return (
          toBoolean(evalNode(node.left, env, state)) ||
          toBoolean(evalNode(node.right, env, state))
        )
      }

      const left = evalNode(node.left, env, state)
      const right = evalNode(node.right, env, state)
      if (node.operator === '==') return left === right
      if (node.operator === '!=') return left !== right

      const leftNumber = toNumber(left)
      const rightNumber = toNumber(right)
      switch (node.operator) {
        case '+':
          return normalizeNumericOutput(leftNumber + rightNumber)
        case '-':
          return normalizeNumericOutput(leftNumber - rightNumber)
        case '*':
          return normalizeNumericOutput(leftNumber * rightNumber)
        case '/':
          return normalizeNumericOutput(leftNumber / rightNumber)
        case '%':
          return normalizeNumericOutput(leftNumber % rightNumber)
        case '<':
          return leftNumber < rightNumber
        case '<=':
          return leftNumber <= rightNumber
        case '>':
          return leftNumber > rightNumber
        case '>=':
          return leftNumber >= rightNumber
      }
      throw new Error(EVAL_ERROR_UNSUPPORTED)
    }
    case 'conditional':
      return evalNode(
        toBoolean(evalNode(node.condition, env, state))
          ? node.whenTrue
          : node.whenFalse,
        env,
        state
      )
    case 'call': {
      if (node.name === 'tier') {
        if (node.args.length !== 2) throw new Error(EVAL_ERROR_UNSUPPORTED)
        const label = evalNode(node.args[0], env, state)
        if (typeof label !== 'string') throw new Error(EVAL_ERROR_UNSUPPORTED)
        const value = toNumber(evalNode(node.args[1], env, state))
        state.matchedTier = label
        return normalizeNumericOutput(value)
      }

      const values = node.args.map((arg) => toNumber(evalNode(arg, env, state)))
      if (node.name === 'max' && values.length === 2) {
        return normalizeNumericOutput(Math.max(values[0], values[1]))
      }
      if (node.name === 'min' && values.length === 2) {
        return normalizeNumericOutput(Math.min(values[0], values[1]))
      }
      if (node.name === 'abs' && values.length === 1) {
        return normalizeNumericOutput(Math.abs(values[0]))
      }
      if (node.name === 'ceil' && values.length === 1) {
        return normalizeNumericOutput(Math.ceil(values[0]))
      }
      if (node.name === 'floor' && values.length === 1) {
        return normalizeNumericOutput(Math.floor(values[0]))
      }
      throw new Error(EVAL_ERROR_UNSUPPORTED)
    }
  }
}

export function evalExprLocally(
  exprStr: string,
  promptTokens: number,
  completionTokens: number,
  extraTokenValues: ExtraTokenValues,
  options: EstimatorOptions = {}
): EvalResult {
  try {
    if (!exprStr || !exprStr.trim()) {
      return createEvalResult(null)
    }

    const body = stripSupportedVersion(exprStr.trim())
    const ast = new ExpressionParser(tokenizeExpression(body)).parse()
    const usedIdentifiers = collectIdentifiers(ast)
    const rawPromptTokens = safeTokenValue(promptTokens)
    const rawCompletionTokens = safeTokenValue(completionTokens)
    const cacheReadTokens = safeTokenValue(extraTokenValues.cacheReadTokens)
    const cacheCreateTokens = safeTokenValue(extraTokenValues.cacheCreateTokens)
    const cacheCreate1hTokens = safeTokenValue(
      extraTokenValues.cacheCreate1hTokens
    )
    const imageTokens = safeTokenValue(extraTokenValues.imageTokens)
    const imageOutputTokens = safeTokenValue(extraTokenValues.imageOutputTokens)
    const audioInputTokens = safeTokenValue(extraTokenValues.audioInputTokens)
    const audioOutputTokens = safeTokenValue(extraTokenValues.audioOutputTokens)

    const usageSemantics = options.usageSemantics ?? 'openai'
    const len =
      usageSemantics === 'anthropic'
        ? rawPromptTokens +
          cacheReadTokens +
          cacheCreateTokens +
          cacheCreate1hTokens
        : rawPromptTokens

    let billablePromptTokens = rawPromptTokens
    let billableCompletionTokens = rawCompletionTokens
    if (usageSemantics === 'openai') {
      if (usedIdentifiers.has('cr')) billablePromptTokens -= cacheReadTokens
      if (usedIdentifiers.has('cc')) billablePromptTokens -= cacheCreateTokens
      if (usedIdentifiers.has('cc1h')) {
        billablePromptTokens -= cacheCreate1hTokens
      }
      if (usedIdentifiers.has('img')) billablePromptTokens -= imageTokens
      if (usedIdentifiers.has('ai')) billablePromptTokens -= audioInputTokens
      if (usedIdentifiers.has('img_o')) {
        billableCompletionTokens -= imageOutputTokens
      }
      if (usedIdentifiers.has('ao')) billableCompletionTokens -= audioOutputTokens
    }
    billablePromptTokens = Math.max(0, billablePromptTokens)
    billableCompletionTokens = Math.max(0, billableCompletionTokens)

    const env: Record<string, EvalValue> = {
      p: billablePromptTokens,
      c: billableCompletionTokens,
      len,
      cr: cacheReadTokens,
      cc: cacheCreateTokens,
      cc1h: cacheCreate1hTokens,
      img: imageTokens,
      img_o: imageOutputTokens,
      ai: audioInputTokens,
      ao: audioOutputTokens,
    }

    const state = { matchedTier: '' }
    const expressionOutput = toNumber(evalNode(ast, env, state))
    if (!Number.isFinite(expressionOutput)) {
      return createEvalResult(EVAL_ERROR_UNSUPPORTED, {
        matchedTier: state.matchedTier,
        billablePromptTokens,
        billableCompletionTokens,
        inputLength: len,
      })
    }
    if (expressionOutput < 0) {
      return createEvalResult(EVAL_ERROR_NEGATIVE, {
        matchedTier: state.matchedTier,
        billablePromptTokens,
        billableCompletionTokens,
        inputLength: len,
      })
    }

    const quotaPerUnit = Number.isFinite(options.quotaPerUnit)
      ? Number(options.quotaPerUnit)
      : 1_000_000
    const groupRatio = Number.isFinite(options.groupRatio)
      ? Number(options.groupRatio)
      : 1
    const quotaBeforeGroup = (expressionOutput / 1_000_000) * quotaPerUnit
    const quotaAfterGroup = roundHalfAwayFromZero(
      quotaBeforeGroup * groupRatio
    )
    return createEvalResult(null, {
      cost: expressionOutput,
      matchedTier: state.matchedTier,
      billablePromptTokens,
      billableCompletionTokens,
      inputLength: len,
      expressionOutput,
      quotaBeforeGroup,
      quotaAfterGroup,
    })
  } catch {
    return createEvalResult(EVAL_ERROR_UNSUPPORTED)
  }
}

export function exprUsesExtraVars(exprStr: string): boolean {
  if (!exprStr) return false
  const varNames = ESTIMATOR_VARS.map((f) => f.var).join('|')
  return new RegExp(`\\b(${varNames})\\b`).test(exprStr)
}

export const ESTIMATOR_EXTRA_FIELDS = ESTIMATOR_VARS
