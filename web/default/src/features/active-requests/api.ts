import { api } from '@/lib/api'
import type { ActiveRequestsResponse } from './types'

type ActiveRequestsApiResponse = ActiveRequestsResponse & {
  success: boolean
}

export async function getActiveRequests(): Promise<ActiveRequestsResponse> {
  const res = await api.get<ActiveRequestsApiResponse>('/api/active-requests')
  return {
    data: res.data.data ?? [],
    completed_retention_seconds: res.data.completed_retention_seconds ?? 10,
  }
}

export async function terminateActiveRequest(requestId: string): Promise<void> {
  await api.delete(`/api/active-requests/${requestId}`)
}
