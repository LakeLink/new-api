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
import * as z from 'zod'

const jsonString = z.string().refine((value) => {
  if (!value.trim()) return true
  try {
    JSON.parse(value)
    return true
  } catch {
    return false
  }
}, 'Invalid JSON format')

export const globalSettingsSchema = z.object({
  global: z.object({
    pass_through_request_enabled: z.boolean(),
    thinking_model_blacklist: jsonString,
    chat_completions_to_responses_policy: z
      .object({
        enabled: z.boolean(),
        all_channels: z.boolean(),
        channel_types: z.array(z.number().int().positive()),
        model_patterns: z.string(),
      })
      .passthrough(),
  }),
  general_setting: z.object({
    ping_interval_enabled: z.boolean(),
    ping_interval_seconds: z.coerce.number().min(1),
  }),
})

export type GlobalModelSettingsFormValues = z.output<
  typeof globalSettingsSchema
>
export type GlobalModelSettingsFormInput = z.input<typeof globalSettingsSchema>
export type GlobalSettingsDefaults = Omit<
  GlobalModelSettingsFormValues,
  'global'
> & {
  global: Omit<
    GlobalModelSettingsFormValues['global'],
    'chat_completions_to_responses_policy'
  > & {
    chat_completions_to_responses_policy: string
  }
}

export function toGlobalSettingsForm(
  values: GlobalSettingsDefaults
): GlobalModelSettingsFormValues {
  let policy: Record<string, unknown> = {}
  try {
    const parsed: unknown = JSON.parse(
      values.global.chat_completions_to_responses_policy.trim() || '{}'
    )
    if (
      parsed !== null &&
      typeof parsed === 'object' &&
      !Array.isArray(parsed)
    ) {
      policy = parsed as Record<string, unknown>
    }
  } catch {
    // Older settings accepted arbitrary JSON; keep the editor usable for correction.
  }
  return {
    ...values,
    global: {
      ...values.global,
      chat_completions_to_responses_policy: {
        ...policy,
        enabled: policy.enabled === true,
        all_channels: policy.all_channels === true,
        channel_types: Array.isArray(policy.channel_types)
          ? policy.channel_types.filter(
              (value): value is number => Number.isInteger(value) && value > 0
            )
          : [],
        model_patterns: Array.isArray(policy.model_patterns)
          ? policy.model_patterns
              .filter((value): value is string => typeof value === 'string')
              .join('\n')
          : '',
      },
    },
  }
}

export function flattenGlobalValues(values: GlobalModelSettingsFormValues) {
  const policy = values.global.chat_completions_to_responses_policy
  return {
    'global.pass_through_request_enabled':
      values.global.pass_through_request_enabled,
    'global.thinking_model_blacklist':
      values.global.thinking_model_blacklist.trim() || '[]',
    'global.chat_completions_to_responses_policy': JSON.stringify(
      Object.fromEntries(
        Object.entries({
          ...policy,
          model_patterns: policy.model_patterns
            .split('\n')
            .map((pattern) => pattern.trim())
            .filter(Boolean),
        }).sort(([a], [b]) => a.localeCompare(b))
      )
    ),
    'general_setting.ping_interval_enabled':
      values.general_setting.ping_interval_enabled,
    'general_setting.ping_interval_seconds':
      values.general_setting.ping_interval_seconds,
  }
}
