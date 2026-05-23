import { api } from '@/lib/api'
import type { ActiveRequestSnapshot } from './types'

export async function getActiveRequests(): Promise<ActiveRequestSnapshot[]> {
  const res = await api.get<{ data: ActiveRequestSnapshot[] }>(
    '/api/active-requests'
  )
  return res.data.data
}

export async function terminateActiveRequest(
  requestId: string
): Promise<void> {
  await api.delete(`/api/active-requests/${requestId}`)
}
