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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ActiveRequestsPage } from '../active-requests-page'
import type { ActiveRequestsResponse } from '../types'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

// Exercise the real SSE parser and auth headers at the network boundary.
class StreamXHR extends EventTarget {
  static instances: StreamXHR[] = []
  static HEADERS_RECEIVED = 2
  responseText = ''
  status = 200
  readyState = 3
  withCredentials = false
  aborted = false
  method = ''
  url = ''
  headers: Record<string, string> = {}

  open(method: string, url: string) {
    this.method = method
    this.url = url
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value
  }
  getAllResponseHeaders() {
    return 'content-type: text/event-stream'
  }
  send() {
    StreamXHR.instances.push(this)
  }
  abort() {
    this.aborted = true
    this.dispatchEvent(new Event('abort'))
  }
  emit(name: string, data: unknown) {
    this.responseText += `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`
    this.dispatchEvent(new Event('progress'))
  }
  finish(status = 200) {
    this.status = status
    this.readyState = 4
    this.dispatchEvent(new Event('load'))
  }
}

const emptySnapshot = {
  success: true,
  data: [],
  completed_retention_seconds: 10,
}
const liveRequest = {
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

beforeEach(() => {
  StreamXHR.instances = []
  vi.stubGlobal('XMLHttpRequest', StreamXHR)
  useAuthStore.setState((state) => ({
    auth: {
      ...state.auth,
      accessToken: 'test-dashboard-token',
      accessExpiresAt: Math.floor(Date.now() / 1000) + 900,
      user: { id: 1, username: 'admin', role: 100 },
    },
  }))
  vi.spyOn(api, 'get').mockResolvedValue({ data: emptySnapshot })
  vi.spyOn(api, 'delete').mockResolvedValue({ data: { success: true } })
})

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <ActiveRequestsPage />
    </QueryClientProvider>
  )
  return { ...view, client }
}

async function receiveSnapshot(
  snapshot: ActiveRequestsResponse = emptySnapshot
) {
  await waitFor(() => expect(StreamXHR.instances).toHaveLength(1))
  act(() =>
    StreamXHR.instances[0]?.emit('snapshot', { success: true, ...snapshot })
  )
}

describe('Active request live updates', () => {
  test('receives snapshots over an authenticated GET stream without polling', async () => {
    renderPage()
    await receiveSnapshot()
    expect(
      await screen.findByText('No active or recent requests')
    ).toBeInTheDocument()
    const stream = StreamXHR.instances[0]
    if (!stream) throw new Error('Expected an active requests stream')
    expect(stream.method).toBe('GET')
    expect(stream.url).toBe('/api/active-requests/stream')
    expect(stream.headers.Authorization).toBe('Bearer test-dashboard-token')
    expect(api.get).not.toHaveBeenCalled()
    act(() =>
      stream.emit('snapshot', { ...emptySnapshot, data: [liveRequest] })
    )
    expect(await screen.findByText('alice (#1)')).toBeInTheDocument()
    expect(screen.getByText('1,234')).toBeInTheDocument()
    act(() => stream.emit('snapshot', emptySnapshot))
    expect(
      await screen.findByText('No active or recent requests')
    ).toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  test('pausing closes the stream and resuming opens a fresh stream', async () => {
    renderPage()
    await receiveSnapshot()
    const user = userEvent.setup()
    await user.click(screen.getByRole('switch', { name: 'Auto Refresh' }))
    expect(StreamXHR.instances[0]?.aborted).toBe(true)
    act(() =>
      StreamXHR.instances[0]?.emit('snapshot', {
        ...emptySnapshot,
        data: [liveRequest],
      })
    )
    expect(screen.queryByText('alice (#1)')).not.toBeInTheDocument()
    await user.click(screen.getByRole('switch', { name: 'Auto Refresh' }))
    await waitFor(() => expect(StreamXHR.instances).toHaveLength(2))
  })

  test('manual refresh still fetches a snapshot while live updates are paused', async () => {
    renderPage()
    await receiveSnapshot()
    const user = userEvent.setup()
    await user.click(screen.getByRole('switch', { name: 'Auto Refresh' }))
    vi.mocked(api.get).mockResolvedValue({
      data: { ...emptySnapshot, data: [liveRequest] },
    })
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('alice (#1)')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/api/active-requests')
  })

  test('terminating a request refreshes its completed status', async () => {
    renderPage()
    await receiveSnapshot({ ...emptySnapshot, data: [liveRequest] })
    expect(await screen.findByText('alice (#1)')).toBeInTheDocument()
    vi.mocked(api.get).mockResolvedValue({
      data: {
        ...emptySnapshot,
        data: [{ ...liveRequest, status: 'completed', can_terminate: false }],
      },
    })
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Terminate' }))
    expect(api.delete).toHaveBeenCalledWith('/api/active-requests/request-live')
    expect(await screen.findByText('Completed')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Terminate' })
    ).not.toBeInTheDocument()
  })

  test('a denied stream shows the shared error state and does not retry automatically', async () => {
    vi.useFakeTimers()
    renderPage()
    await act(async () => {
      await Promise.resolve()
    })
    act(() => StreamXHR.instances[0]?.finish(403))
    expect(screen.getByText('Request failed')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(30_000))
    expect(StreamXHR.instances).toHaveLength(1)
  })

  test('reconnects a closed stream using the current authorization header', async () => {
    vi.useFakeTimers()
    const view = renderPage()
    await act(async () => {
      await Promise.resolve()
    })
    act(() => StreamXHR.instances[0]?.finish())
    useAuthStore.setState((state) => ({
      auth: { ...state.auth, accessToken: 'rotated-token' },
    }))
    await act(() => vi.advanceTimersByTimeAsync(1000))
    expect(StreamXHR.instances).toHaveLength(2)
    expect(StreamXHR.instances[1]?.headers.Authorization).toBe(
      'Bearer rotated-token'
    )
    view.unmount()
    expect(StreamXHR.instances[1]?.aborted).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(30_000))
    expect(StreamXHR.instances).toHaveLength(2)
  })

  test('server revocation closes the stream without reconnecting', async () => {
    vi.useFakeTimers()
    renderPage()
    await act(async () => {
      await Promise.resolve()
    })
    act(() => StreamXHR.instances[0]?.emit('unauthorized', {}))
    expect(StreamXHR.instances[0]?.aborted).toBe(true)
    expect(screen.getByText('Request failed')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(30_000))
    expect(StreamXHR.instances).toHaveLength(1)
  })

  test('a temporary server failure reconnects and clears the error after a snapshot', async () => {
    vi.useFakeTimers()
    renderPage()
    await act(async () => {
      await Promise.resolve()
    })
    act(() => StreamXHR.instances[0]?.finish(503))
    expect(screen.getByText('Request failed')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(1000))
    expect(StreamXHR.instances).toHaveLength(2)
    act(() => StreamXHR.instances[1]?.emit('snapshot', emptySnapshot))
    await act(() => vi.advanceTimersByTimeAsync(0))
    expect(screen.queryByText('Request failed')).not.toBeInTheDocument()
    expect(screen.getByText('No active or recent requests')).toBeInTheDocument()
  })

  test('a newer stream snapshot takes precedence over a pending manual refresh', async () => {
    renderPage()
    await receiveSnapshot()
    let finishRefresh:
      | ((value: { data: typeof emptySnapshot }) => void)
      | undefined
    vi.mocked(api.get).mockImplementation(
      () =>
        new Promise((resolve) => {
          finishRefresh = resolve
        })
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(StreamXHR.instances).toHaveLength(2))
    act(() =>
      StreamXHR.instances[1]?.emit('snapshot', {
        ...emptySnapshot,
        data: [liveRequest],
      })
    )
    expect(await screen.findByText('alice (#1)')).toBeInTheDocument()
    await act(async () => finishRefresh?.({ data: emptySnapshot }))
    expect(screen.getByText('alice (#1)')).toBeInTheDocument()
  })

  test('hiding the page disconnects and returning reconnects', async () => {
    renderPage()
    await receiveSnapshot()
    const visibility = vi.spyOn(document, 'hidden', 'get')
    visibility.mockReturnValue(true)
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    expect(StreamXHR.instances[0]?.aborted).toBe(true)
    visibility.mockReturnValue(false)
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    await waitFor(() => expect(StreamXHR.instances).toHaveLength(2))
  })

  test('logout closes the stream immediately', async () => {
    renderPage()
    await receiveSnapshot()
    act(() => useAuthStore.getState().auth.reset())
    expect(StreamXHR.instances[0]?.aborted).toBe(true)
  })
})
