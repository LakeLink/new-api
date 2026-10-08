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
export interface ActiveRequestSnapshot {
  request_id: string
  user_id: number
  username: string
  token_id: number
  token_name: string
  model: string
  channel_name: string
  channel_id: number
  channel_type: number
  start_time: number
  end_time?: number
  status?: 'active' | 'completed'
  is_stream: boolean
  client_ip: string
  input_tokens: number
  output_chunks: number
  elapsed_seconds: number
  stale_for_seconds: number
  ended_seconds_ago?: number
  can_terminate?: boolean
}

export interface ActiveRequestsResponse {
  data: ActiveRequestSnapshot[]
  completed_retention_seconds: number
}
