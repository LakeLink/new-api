# OpenAI-Related Upstream Code Review

Range under review: `v0.13.0..upstream/main`

Endpoint reviewed: `upstream/main@583da45296eda8a9950a346055997e53cb8a7e1e`

Baseline: `v0.13.0@2e610e5fb39cc8f4c873e0f8242ae73a39d68ac2`

## Previously Confirmed Issues

### High: large JSON requests are materialized in distributor metadata extraction

`middleware/distributor.go` calls `storage.Bytes()` and validates the full JSON body just to read top-level `model` and `group`. For disk-backed request bodies this defeats the streaming decode path added in `common/gin.go`, and can cause heap pressure or GC stalls before large OpenAI-compatible requests reach their upstream.

Relevant files:
- `middleware/distributor.go`
- `common/gin.go`
- `common/body_storage.go`

### High: stream timeout cleanup waits before unblocking a blocked scanner

`relay/helper/stream_scanner.go` waits on scanner-related goroutines during deferred cleanup, but `resp.Body.Close()` runs later because it was registered as an earlier defer. If the scanner goroutine is blocked in `scanner.Scan()`, it cannot observe `stopChan`; timeout cleanup can wait up to 5 seconds before the body close finally unblocks the read.

Confirmed introducing commit:
- `5511ba3670c4385a690a62dd477f51369632d0c5`
- Commit time: `2025-06-11T00:18:16+08:00`
- Subject: `fix(stream_scanner): improve resource management and error handling in StreamScannerHandler`

Boundary check:
- Parent `ad4d3efd2e17cf5e80d8f41a97aedcc6230bcf1f`: returns promptly.
- `5511ba3670c4385a690a62dd477f51369632d0c5`: reproduces delayed timeout cleanup.

### Medium: Waffo Pancake subscription webhook can be gated by top-up product configuration

Subscription checkout can proceed with merchant/private key plus a plan product ID, but the webhook gate requires the global top-up product ID. A subscription-only setup can therefore create paid orders whose webhook is rejected before settlement.

Relevant files:
- `controller/subscription_payment_waffo_pancake.go`
- `controller/payment_webhook_availability.go`
- `controller/topup_waffo_pancake.go`

### Medium: Waffo Pancake webhook reads and logs the full request body

The webhook path reads the full body with `io.ReadAll` and logs the raw body and signature on receipt and verification failure. This can create memory pressure and leak buyer/order payloads into logs.

Relevant file:
- `controller/topup_waffo_pancake.go`

## New Findings From Extended OpenAI-Related Review

### Medium: OpenAI-compatible pass-through requests still omit known Content-Length

Commit `fddf54ccc5cf1c97c7a48c657c3cc204b3d9f68f` (`2026-05-22T19:08:38+08:00`, `perf: reduce heap residency for large base64 relay requests`) added `RelayInfo.UpstreamRequestBodySize` plus `applyUpstreamContentLength`, explicitly to prevent `ReaderOnly(BodyStorage)` requests from falling back to chunked transfer encoding.

The converted JSON paths set `info.UpstreamRequestBodySize` after building an outbound body, but the pass-through paths still only do:

- `storage, err := common.GetBodyStorage(c)`
- `requestBody = common.ReaderOnly(storage)`

They do not copy `storage.Size()` into `info.UpstreamRequestBodySize`, so `applyUpstreamContentLength` has no size to apply. OpenAI-compatible pass-through traffic can therefore still go upstream without an explicit `Content-Length`, even though the body size is already known.

Relevant pass-through paths:
- `relay/compatible_handler.go:97`
- `relay/responses_handler.go:74`
- `relay/image_handler.go:49`
- `relay/rerank_handler.go:45`

Relevant helper:
- `relay/channel/api_request.go:36`

Risk:
- Some OpenAI-compatible providers and proxy layers reject or mishandle chunked request bodies.
- This is most visible when global or per-channel pass-through is enabled for chat completions, responses, JSON image requests, or rerank requests.
- It partially preserves the behavior that the `fddf54ccc` change was trying to remove.

Suggested fix:
- In each pass-through branch, set `info.UpstreamRequestBodySize = storage.Size()` before assigning `requestBody = common.ReaderOnly(storage)`.

Validation:
- A temporary focused Go test in `relay/channel` confirmed that `http.NewRequest` leaves `ContentLength` unset for `common.ReaderOnly(storage)` unless `RelayInfo.UpstreamRequestBodySize` is populated.

### Expanded impact: scanner timeout issue covers all OpenAI-style stream handlers

The already confirmed scanner cleanup bug is not limited to Codex. In the extended range, all OpenAI-style streaming handlers still route through `helper.StreamScannerHandler`, including:

- `relay/channel/openai/relay-openai.go:128`
- `relay/channel/openai/relay_responses.go:82`
- `relay/channel/openai/chat_via_responses.go:299`
- `relay/channel/openai/audio.go:41`

Additional adapters also delegate to those OpenAI stream handlers, including Codex, xAI, Cloudflare, Vertex, Jimeng, Mistral, and submodel paths. The stuck-stream symptom can therefore appear on OpenAI-compatible upstreams broadly, not just Codex.

Validation:
- `go test ./relay/helper -run TestStreamScannerHandler -count=1` still reproduces the timeout cleanup wait on `upstream/main`.

## Review Validation

Commands run against `/tmp/new-api-review-upstream`:

- `go test ./relay/channel/openai -count=1`
- `go test ./relay/common -count=1`
- `go test ./service/openaicompat -count=1`
- `go test ./relay/helper -run TestStreamScannerHandler -count=1`
- Temporary focused `relay/channel` content-length reproduction test for `ReaderOnly(BodyStorage)`

Results:
- `relay/channel/openai`: passed / no test files.
- `relay/common`: passed.
- `service/openaicompat`: passed / no test files.
- `relay/helper`: failed on the known stream-scanner behavior.
- Temporary content-length test: passed, confirming the pass-through gap.
