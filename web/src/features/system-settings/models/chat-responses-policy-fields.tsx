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
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { CHANNEL_TYPE_OPTIONS } from '@/features/channels/constants'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import type { GlobalModelSettingsFormInput } from './global-settings-form'

export function ChatResponsesPolicyFields({
  disabled,
}: {
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<GlobalModelSettingsFormInput>()
  const allChannels = form.watch(
    'global.chat_completions_to_responses_policy.all_channels'
  )
  return (
    <div className='space-y-4'>
      <FormField
        control={form.control}
        name='global.chat_completions_to_responses_policy.enabled'
        render={({ field }) => (
          <SettingsSwitchItem>
            <SettingsSwitchContent>
              <FormLabel>
                {t('Enable ChatCompletions -> Responses conversion')}
              </FormLabel>
              <FormDescription>
                {t(
                  'Convert matching requests to the Responses API. Request passthrough takes precedence.'
                )}
              </FormDescription>
            </SettingsSwitchContent>
            <FormControl>
              <Switch
                checked={field.value}
                onCheckedChange={field.onChange}
                onBlur={field.onBlur}
                ref={field.ref}
                disabled={disabled}
              />
            </FormControl>
          </SettingsSwitchItem>
        )}
      />
      <FormField
        control={form.control}
        name='global.chat_completions_to_responses_policy.all_channels'
        render={({ field }) => (
          <SettingsSwitchItem>
            <SettingsSwitchContent>
              <FormLabel>{t('Apply to all channels')}</FormLabel>
              <FormDescription>
                {t(
                  'Channel overrides take precedence. Otherwise, use all channels or the selected channel types.'
                )}
              </FormDescription>
            </SettingsSwitchContent>
            <FormControl>
              <Switch
                checked={field.value}
                onCheckedChange={field.onChange}
                onBlur={field.onBlur}
                ref={field.ref}
                disabled={disabled}
              />
            </FormControl>
          </SettingsSwitchItem>
        )}
      />
      <FormField
        control={form.control}
        name='global.chat_completions_to_responses_policy.channel_types'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Channel types')}</FormLabel>
            <FormControl>
              <MultiSelect
                options={CHANNEL_TYPE_OPTIONS.map((option) => ({
                  value: String(option.value),
                  label: t(option.label),
                }))}
                selected={field.value.map(String)}
                onChange={(selected) => field.onChange(selected.map(Number))}
                disabled={disabled || allChannels}
                placeholder={t('Channel types')}
              />
            </FormControl>
            <FormDescription>
              {t(
                'Manage individual channels in Channels -> Request. Existing channel selections remain active until overridden.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='global.chat_completions_to_responses_policy.model_patterns'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Model patterns')}</FormLabel>
            <FormControl>
              <Textarea
                {...field}
                rows={4}
                placeholder={'^gpt-5.*$\n^gpt-6.*$'}
                disabled={disabled}
                className='font-mono'
              />
            </FormControl>
            <FormDescription>
              {t(
                'One Go regular expression per line. Only matching model names are converted; an empty list matches no models.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </div>
  )
}
