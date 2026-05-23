export interface ActiveRequestSnapshot {
  request_id: string
  user_id: number
  token_id: number
  token_name: string
  model: string
  channel_id: number
  channel_type: number
  start_time: number
  is_stream: boolean
  client_ip: string
  input_tokens: number
  output_chunks: number
  elapsed_seconds: number
  stale_for_seconds: number
}
