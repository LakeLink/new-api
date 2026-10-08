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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { ActiveRequestsPage } from '../active-requests-page'
import { getActiveRequests, terminateActiveRequest } from '../api'

vi.mock('../api', () => ({
  getActiveRequests: vi.fn(),
  terminateActiveRequest: vi.fn(),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

afterEach(() => vi.clearAllMocks())

describe('Active request controls', () => {
  test('shows an empty response', async () => {
    vi.mocked(getActiveRequests).mockResolvedValue({
      data: [],
      completed_retention_seconds: 10,
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <ActiveRequestsPage />
      </QueryClientProvider>
    )
    expect(
      await screen.findByText('No active or recent requests')
    ).toBeInTheDocument()
    client.clear()
  })
  test('terminates the selected live request and refreshes its status', async () => {
    const request = {
      request_id: 'request-live',
      user_id: 1,
      username: 'alice',
      token_id: 2,
      token_name: 'main',
      model: 'gpt-test',
      channel_name: 'primary',
      channel_id: 3,
      channel_type: 1,
      start_time: 1000,
      is_stream: true,
      client_ip: '127.0.0.1',
      input_tokens: 1234,
      output_chunks: 4,
      elapsed_seconds: 65,
      stale_for_seconds: 2,
      status: 'active' as const,
      can_terminate: true,
    }
    vi.mocked(getActiveRequests)
      .mockResolvedValueOnce({
        data: [request],
        completed_retention_seconds: 10,
      })
      .mockResolvedValue({
        data: [{ ...request, status: 'completed', can_terminate: false }],
        completed_retention_seconds: 10,
      })
    vi.mocked(terminateActiveRequest).mockResolvedValue()
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <ActiveRequestsPage />
      </QueryClientProvider>
    )
    expect(await screen.findByText('alice (#1)')).toBeInTheDocument()
    expect(screen.getByText('1,234')).toBeInTheDocument()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Terminate' }))
    await waitFor(() =>
      expect(terminateActiveRequest).toHaveBeenCalledWith(
        'request-live',
        expect.anything()
      )
    )
    expect(await screen.findByText('Completed')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Terminate' })
    ).not.toBeInTheDocument()
    client.clear()
  })
})
