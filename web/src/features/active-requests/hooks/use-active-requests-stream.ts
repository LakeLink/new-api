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
import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useState } from 'react'
import { SSE } from 'sse.js'

import { getFreshAuthHeaders } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import type { ActiveRequestsResponse } from '../types'

export function useActiveRequestsStream(enabled: boolean) {
  const queryClient = useQueryClient()
  const authenticated = useAuthStore((state) => Boolean(state.auth.accessToken))
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const [failed, setFailed] = useState(false)
  const [revision, setRevision] = useState(0)
  const reconnect = useCallback(() => setRevision((value) => value + 1), [])

  useEffect(() => {
    if (!enabled || !authenticated) return

    let disposed = false
    let source: SSE | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let reconnectDelay = 1000
    let connecting = false
    let denied = false

    const disconnect = () => {
      clearTimeout(reconnectTimer)
      reconnectTimer = undefined
      const previous = source
      source = null
      previous?.close()
    }

    const scheduleReconnect = () => {
      if (disposed || denied || document.hidden || reconnectTimer) return
      reconnectTimer = setTimeout(() => {
        reconnectTimer = undefined
        void connect()
      }, reconnectDelay)
      reconnectDelay = Math.min(reconnectDelay * 2, 30_000)
    }

    const connect = async () => {
      if (disposed || denied || document.hidden || source || connecting) return
      connecting = true
      try {
        const headers = await getFreshAuthHeaders()
        if (disposed || denied || document.hidden) return
        const stream = new SSE('/api/active-requests/stream', {
          headers: { ...headers, Accept: 'text/event-stream' },
          method: 'GET',
          withCredentials: true,
          start: false,
        })
        source = stream
        stream.addEventListener('snapshot', (event: { data: string }) => {
          if (disposed || source !== stream) return
          try {
            const snapshot = requireServerSuccess(
              JSON.parse(event.data) as ActiveRequestsResponse & {
                success: boolean
              }
            )
            if (
              !Array.isArray(snapshot.data) ||
              typeof snapshot.completed_retention_seconds !== 'number'
            ) {
              throw new Error('Invalid active requests snapshot')
            }
            // A slower manual refresh must not overwrite a newer stream snapshot.
            void queryClient.cancelQueries(
              { queryKey: ['active-requests'] },
              { silent: true, revert: false }
            )
            queryClient.setQueryData<ActiveRequestsResponse>(
              ['active-requests'],
              snapshot
            )
            reconnectDelay = 1000
            setFailed(false)
          } catch {
            setFailed(true)
            disconnect()
            scheduleReconnect()
          }
        })
        stream.addEventListener('unauthorized', () => {
          if (disposed || source !== stream) return
          denied = true
          setFailed(true)
          disconnect()
        })
        stream.addEventListener('error', (event: { responseCode?: number }) => {
          if (disposed || source !== stream) return
          denied = event.responseCode === 401 || event.responseCode === 403
          setFailed(true)
        })
        stream.addEventListener(
          'readystatechange',
          (event: { readyState?: number }) => {
            if (
              disposed ||
              source !== stream ||
              event.readyState !== SSE.CLOSED
            ) {
              return
            }
            source = null
            scheduleReconnect()
          }
        )
        stream.stream()
      } catch {
        if (!disposed) {
          setFailed(true)
          scheduleReconnect()
        }
      } finally {
        connecting = false
      }
    }

    const handleVisibilityChange = () => {
      if (document.hidden) disconnect()
      else void connect()
    }
    document.addEventListener('visibilitychange', handleVisibilityChange)
    void connect()
    return () => {
      disposed = true
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      disconnect()
    }
  }, [enabled, authenticated, userId, sessionId, queryClient, revision])

  return { failed, reconnect }
}
