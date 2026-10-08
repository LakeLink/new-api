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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { assert, expect, test, vi } from 'vitest'

import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  type ChannelFormValues,
  buildSettingJSON,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'
import { ChatResponsesSetting } from '../chat-responses-setting'

test.each([undefined, true, false])(
  'preserves channel conversion override %s through create, update and reload',
  (enabled) => {
    const channel = channelSchema.parse({
      id: 79,
      name: 'Codex',
      type: 57,
      key: '',
      status: 1,
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      setting: JSON.stringify({ chat_completions_to_responses: enabled }),
    })
    let expected = 'inherit'
    if (enabled !== undefined) expected = enabled ? 'enabled' : 'disabled'
    const values = transformChannelToFormDefaults(channel)
    expect(values).toHaveProperty('chat_completions_to_responses', expected)
    for (const payload of [
      transformFormDataToCreatePayload(values).channel,
      transformFormDataToUpdatePayload(values, channel.id),
    ]) {
      assert(typeof payload.setting === 'string')
      expect(JSON.parse(payload.setting).chat_completions_to_responses).toBe(
        enabled
      )
      expect(
        transformChannelToFormDefaults({ ...channel, setting: payload.setting })
      ).toHaveProperty('chat_completions_to_responses', expected)
    }
  }
)

test('new channels inherit the global policy without persisting an explicit override', () => {
  expect(
    JSON.parse(buildSettingJSON(CHANNEL_FORM_DEFAULT_VALUES))
  ).not.toHaveProperty('chat_completions_to_responses')
})

function Fixture({
  disabled,
  onSave,
}: {
  disabled?: boolean
  onSave: (value: string) => void
}) {
  const form = useForm<ChannelFormValues>({
    defaultValues: CHANNEL_FORM_DEFAULT_VALUES,
  })
  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit((values) =>
          onSave(buildSettingJSON(values))
        )}
      >
        <ChatResponsesSetting disabled={disabled} />
        <button type='submit'>Save</button>
      </form>
    </Form>
  )
}

test('channel conversion selection supports keyboard changes and persists explicit enable and disable', async () => {
  const user = userEvent.setup()
  const onSave = vi.fn()
  render(<Fixture onSave={onSave} />)
  const select = screen.getByRole('combobox', {
    name: 'ChatCompletions -> Responses Compatibility',
  })
  expect(select).toHaveTextContent('Use global policy')
  select.focus()
  await user.keyboard('{ArrowDown}')
  await user.click(screen.getByRole('option', { name: 'Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  expect(
    JSON.parse(onSave.mock.calls[0][0]).chat_completions_to_responses
  ).toBe(true)
  await user.click(select)
  await user.click(screen.getByRole('option', { name: 'Disabled' }))
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
  expect(
    JSON.parse(onSave.mock.calls[1][0]).chat_completions_to_responses
  ).toBe(false)
  await user.click(select)
  await user.click(screen.getByRole('option', { name: 'Use global policy' }))
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(3))
  expect(JSON.parse(onSave.mock.calls[2][0])).not.toHaveProperty(
    'chat_completions_to_responses'
  )
})

test('locked channel configuration disables conversion selection', async () => {
  const user = userEvent.setup()
  render(<Fixture disabled onSave={() => undefined} />)
  const select = screen.getByRole('combobox', {
    name: 'ChatCompletions -> Responses Compatibility',
  })
  expect(select).toBeDisabled()
  await user.click(select)
  expect(screen.queryByRole('option')).not.toBeInTheDocument()
})
