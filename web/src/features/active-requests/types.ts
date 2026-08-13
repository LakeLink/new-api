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
