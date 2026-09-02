# Higress WASM to CompliK Page Review

Status: Core Admin/worker path implemented; Higress WASM deployment remains an integration step

This document records the proposed integration between a Higress Proxy-Wasm
plugin and CompliK for traffic-triggered page review. It is based on the
current repository layout and distinguishes existing APIs from proposed ones.

## Summary

The Higress WASM plugin should match the effective request route and submit a
small, asynchronous review signal. It should not query Higress logs, buffer
HTML, call a language model, or block the user request.

CompliK, or an ingestion component next to the Admin service, should perform
task coalescing and durable task management. A CompliK worker then uses the
existing Browser collector and Safety/Custom detectors. Final findings keep
using the existing violation reporting path.

```text
request -> Higress WASM route match
        -> async HTTP hostcall to Admin/ingest
        -> route observation upsert and review-task coalescing
        -> CompliK worker leases task
        -> Browser fetches page
        -> Safety/Custom reviews content
        -> AdminReporter stores the final result
```

## Goals

- Avoid scanning and deduplicating large Higress log streams.
- Avoid buffering response bodies in the gateway WASM VM.
- Coalesce repeated requests before Browser or model work is scheduled.
- Preserve the existing CompliK Browser and detector pipeline.
- Keep the user request path fail-open and independent of review availability.
- Provide durable retries and observable task state.

## Non-goals

- Performing LLM inference inside the WASM VM.
- Making every user request synchronously wait for a review result.
- Treating an access observation as a violation result.
- Replacing the scheduled full scan for routes that receive no traffic.

## Current Repository Boundaries

The `complik` process currently uses an in-process EventBus. It does not expose
an inbound HTTP task endpoint; its application startup loads plugins and then
waits for a shutdown signal.

The Admin service already exposes `POST /api/discovered-paths`. That endpoint
is suitable for aggregated traffic observations. Its upsert operation uses a
route/path uniqueness key, increments `count`, and advances `last_seen_at`.
Callers must therefore send count deltas, not cumulative counters.

The Admin service also exposes `POST /api/complik-violations`. This is the
final detector-result endpoint and must not be used for pending review tasks,
because creating a violation can trigger downstream autoban handling.

## Transport From WASM

A Proxy-Wasm plugin cannot use Go `net/http`, open arbitrary sockets, or rely on
an arbitrary URL from inside the VM. It can use the Proxy-Wasm host API,
typically `dispatch_http_call`, to ask the Envoy/Higress host to make an
asynchronous HTTP call to a configured upstream cluster.

Conceptually:

```go
proxywasm.DispatchHttpCall(
    "sealos-complik-admin",
    headers,
    body,
    nil,
    500,
)
```

The cluster name is deployment-specific. The Admin Service must be reachable
as an Envoy/Higress upstream; the WASM plugin should not construct a raw
network connection. The HTTP response is handled by the plugin's asynchronous
HTTP-call callback. The request should continue without waiting for that
callback.

If the selected Higress version or runtime does not expose this hostcall, the
fallback is a supported telemetry/log exporter or a dedicated local ingest
service. The plugin must not assume that arbitrary network APIs are available
inside the VM.

## API Contracts

### Existing route observation API

Use `POST /api/discovered-paths` for traffic statistics when those statistics
are required by the Admin UI or reporting. Submit a batch at a fixed interval
and send a delta count:

```json
{
  "items": [
    {
      "namespace": "ns-demo",
      "ingress_name": "web",
      "host": "example.com",
      "path": "/docs",
      "count": 12,
      "last_seen_at": "2026-09-02T10:00:00Z"
    }
  ]
}
```

This endpoint is an observation store, not a review queue.

### Implemented review task API

The Admin service now exposes `POST /api/page-review-tasks`. It accepts batches
and returns `202 Accepted` after validating and coalescing the tasks:

```json
{
  "items": [
    {
      "namespace": "ns-demo",
      "ingress_name": "web",
      "host": "example.com",
      "path": "/docs",
      "url": "https://example.com/docs",
      "observed_at": "2026-09-02T10:00:00Z",
      "content_version": "etag:abc123",
      "policy_version": "v1"
    }
  ]
}
```

The endpoint should return task IDs or an accepted count for observability. It
should not accept arbitrary URLs without validation. Prefer a route ID or
route metadata that the server maps to an allow-listed URL.

The worker lifecycle endpoints are:

```text
POST /api/page-review-tasks/claim
POST /api/page-review-tasks/{id}/complete
POST /api/page-review-tasks/{id}/fail
```

`claim` accepts `worker_id`, `limit`, and `lease_duration_second`. The worker
must send its owner ID when completing or failing a task. Expired leases are
returned to `pending` by the next claim operation. The default task cooldown
after success is ten minutes.

## Idempotency and Deduplication

Use two different keys for two different concerns:

```text
route_key = namespace + ingress_name + normalized host + normalized path
task_key  = route_key + content_version + policy_version
```

If `ETag` or `Last-Modified` is unavailable, use a route-level cooldown and
allow only one pending/running task per route. Dynamic pages may need an
additional tenant or device dimension; that choice must be explicit.

The Admin task table enforces a unique task key and retains at least:

```text
task_id
route_key
url
policy_version
content_version
status: pending | running | succeeded | failed
attempts
lease_until
next_run_at
last_error
created_at
updated_at
```

The existing `discovered_paths` table can continue to store counts. It should
not be treated as a queue merely because it has `last_detected_at` and
`last_detection_status` fields; a task creation and status-update path is still
needed.

## WASM Processing Rules

The WASM plugin should:

1. Match the effective Higress route during request headers processing.
2. Obtain a stable route ID or configured namespace/Ingress metadata.
3. Normalize host and path consistently with the Admin service.
4. Aggregate observations per route and flush batches on a timer or threshold.
5. Use `dispatch_http_call` with a short timeout.
6. Continue the user request regardless of ingest success or failure.

The aggregation state inside a WASM VM is local to a worker and gateway
replica. It is an optimization only. Durable deduplication must happen in the
Admin/ingest service or its database.

The plugin should not read or forward HTML for this trigger path. The task
contains the URL and route metadata; a CompliK worker fetches the page using
the existing Browser collector. This means the review sees a controlled
fetcher session rather than necessarily the exact user's cookies or session.

## CompliK Worker

The repository now includes a `PageReviewWorker` plugin. It leases pending
tasks with a bounded concurrency limit, publishes a `DiscoveryInfo` event with
`ReviewTaskID`, and reuses the existing Browser -> CollectorTopic ->
DetectorTopic flow. The existing AdminReporter still reports the final
detector result; the worker only updates task state independently of the
user-facing request.

The worker is configured in `complik/config.yml` and uses these settings:

```json
{
  "adminBaseURL": "http://sealos-complik-admin:8080",
  "adminTimeoutSecond": 10,
  "pollIntervalSecond": 5,
  "reviewTimeoutSecond": 180,
  "leaseDurationSecond": 240,
  "initialDelaySecond": 10,
  "batchSize": 10,
  "maxWorkers": 10
}
```

The worker completes a task after the first detector result carrying the same
`ReviewTaskID`. A timeout records a retryable failure. The task ID is propagated
through DiscoveryInfo, CollectorInfo, and DetectorInfo; it is not stored as a
pending violation.

The implementation does not add a Higress WASM binary to this repository.
Higress must match the effective route and call the enqueue endpoint through
Proxy-Wasm `dispatch_http_call`; the call remains asynchronous and fail-open.

For a deployment without a separate message broker, the first version can use
the Admin database as a durable queue. A later version can replace task
claiming with Redis, NATS, Kafka, or another existing platform queue without
changing the WASM contract.

## Security and Failure Handling

- Protect the ingest endpoint with service authentication, preferably mTLS or
  a rotated HMAC/token scheme. Basic Auth is acceptable for an internal first
  version when configured securely.
- Do not embed long-lived credentials directly in the WASM binary.
- Validate route identity and URL host against an allow-list to prevent SSRF.
- Do not forward user cookies or authorization headers by default.
- Apply request size, batch size, queue length, retry, and per-route rate
  limits.
- Treat ingest timeout or Admin unavailability as fail-open for the page
  request and expose metrics for dropped observations.
- Keep synchronous blocking review out of the gateway. Local WASM rules may
  reject a request only if that behavior is explicitly required and tested.

## Hybrid Operating Model

Keep the scheduled `Complete + Browser` scan as the coverage baseline. It
finds routes that have no traffic. Use the Higress WASM path only for
near-real-time review of accessed routes and content changes.

The raw-log Higress plugin should not be used as the trigger mechanism. It
creates one query per discovery event, does not currently match `Path`, and
does not produce the `CollectorInfo` payload expected by the existing
detectors.

## Remaining Integration Sequence

1. Add route metadata and a configured Admin upstream cluster in Higress.
2. Implement the WASM route matcher, local aggregation, async HTTP hostcall,
   and fail-open metrics.
3. Add end-to-end tests for duplicate requests, content-version changes,
   namespace isolation, task retries, and Admin/worker failure.

## Acceptance Criteria

- Repeated requests for one unchanged route create at most one pending/running
  task within the configured cooldown.
- The same host/path in two namespaces remains separate.
- WASM never buffers or forwards the HTML body for this trigger path.
- A slow or unavailable Admin service does not delay the user response.
- Lease ownership prevents two workers from processing the same task at the
  same time; detector reporting remains at-least-once if a worker crashes after
  publishing a detector event and before completing its lease.
- Scheduled scanning still covers routes that receive no traffic.
