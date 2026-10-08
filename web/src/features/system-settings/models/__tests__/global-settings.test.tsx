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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { GlobalSettingsCard } from '../global-settings-card'

vi.mock('@/lib/api', () => ({ api: { put: vi.fn() } }))

const policy = {
  enabled: true,
  all_channels: false,
  channel_ids: [79, 103, 110, 126],
  channel_types: [1, 999],
  model_patterns: ['^gpt-5.*$'],
  extension: { retained: true },
}

function Fixture() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  )
  return (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <div ref={setActions} />
        <GlobalSettingsCard
          defaultValues={{
            global: {
              pass_through_request_enabled: false,
              thinking_model_blacklist: '[]',
              chat_completions_to_responses_policy: JSON.stringify(policy),
            },
            general_setting: {
              ping_interval_enabled: false,
              ping_interval_seconds: 10,
            },
          }}
        />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

beforeEach(() =>
  vi
    .mocked(api.put)
    .mockReset()
    .mockResolvedValue({ data: { success: true } })
)

test.each([en, zh, zhTW, fr, ja, ru, viLocale])(
  'preserves the Responses API name in compatibility headings',
  (locale) => {
    expect(
      locale.translation['ChatCompletions -> Responses Compatibility']
    ).toContain('Responses')
  }
)

test('saves structured conversion controls while retaining legacy channel IDs and extensions', async () => {
  const user = userEvent.setup()
  render(<Fixture />)
  expect(
    screen.queryByRole('textbox', { name: 'Policy JSON' })
  ).not.toBeInTheDocument()
  const patterns = screen.getByRole('textbox', { name: 'Model patterns' })
  fireEvent.change(patterns, {
    target: { value: '(?i)^gpt-[56].*$\n^custom-{1,3}$\n' },
  })
  expect(patterns).toHaveValue('(?i)^gpt-[56].*$\n^custom-{1,3}$\n')
  await user.click(
    screen.getByRole('switch', { name: 'Apply to all channels' })
  )
  await user.click(
    screen.getByRole('switch', {
      name: 'Enable ChatCompletions -> Responses conversion',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
  expect(vi.mocked(api.put).mock.calls[0][0]).toBe('/api/option/')
  const request = vi.mocked(api.put).mock.calls[0][1] as {
    key: string
    value: string
  }
  expect(request.key).toBe('global.chat_completions_to_responses_policy')
  expect(JSON.parse(request.value)).toEqual({
    ...policy,
    enabled: false,
    all_channels: true,
    model_patterns: ['(?i)^gpt-[56].*$', '^custom-{1,3}$'],
  })
})

test('unchanged structured settings do not rewrite options', async () => {
  const user = userEvent.setup()
  render(<Fixture />)
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(api.put).not.toHaveBeenCalled()
})

test('rejected saves retain the model pattern draft for correction', async () => {
  vi.mocked(api.put).mockResolvedValue({
    data: { success: false, message: 'Invalid model pattern' },
  })
  const user = userEvent.setup()
  render(<Fixture />)
  const patterns = screen.getByRole('textbox', { name: 'Model patterns' })
  fireEvent.change(patterns, { target: { value: '[' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
  expect(patterns).toHaveValue('[')
})

test('channel type selection is disabled for all channels and retained when switching back', async () => {
  const user = userEvent.setup()
  render(<Fixture />)
  const types = screen.getByRole('combobox', { name: 'Channel types' })
  expect(types).not.toBeDisabled()
  await user.click(
    screen.getByRole('switch', { name: 'Apply to all channels' })
  )
  expect(types).toBeDisabled()
  await user.click(
    screen.getByRole('switch', { name: 'Apply to all channels' })
  )
  expect(types).not.toBeDisabled()
  await user.click(types)
  await user.click(
    screen.getByRole('option', { name: 'ChatGPT Subscription (Codex)' })
  )
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
  const request = vi.mocked(api.put).mock.calls[0][1] as { value: string }
  expect(JSON.parse(request.value)).toEqual({
    ...policy,
    channel_types: [1, 999, 57],
  })
})
