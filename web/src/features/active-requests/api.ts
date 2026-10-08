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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { ActiveRequestsResponse } from './types'

type ActiveRequestsApiResponse = ActiveRequestsResponse & {
  success: boolean
}

export async function getActiveRequests(): Promise<ActiveRequestsResponse> {
  const res = await api.get<ActiveRequestsApiResponse>('/api/active-requests')
  const response = requireServerSuccess(res.data)
  return {
    data: response.data ?? [],
    completed_retention_seconds: response.completed_retention_seconds ?? 10,
  }
}

export async function terminateActiveRequest(requestId: string): Promise<void> {
  const res = await api.delete(`/api/active-requests/${requestId}`)
  requireServerSuccess(res.data)
}
