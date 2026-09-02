# Higress WASM 到 CompliK 的页面审查方案

状态：Admin/worker 核心链路已实现；Higress WASM 部署仍需集成配置

本文归档 Higress Proxy-Wasm 插件与 CompliK 之间的流量触发式页面审查
方案。内容基于当前仓库结构，并明确区分已有接口和拟新增接口。

## 摘要

Higress WASM 负责匹配实际请求路由，并提交轻量、异步的审查信号。它不应
查询 Higress 日志、不应缓存 HTML、不应调用大模型，也不应阻塞用户请求。

CompliK 或 Admin 服务旁边的 ingest 组件负责任务合并和持久化。CompliK
worker 再复用现有 Browser 采集器及 Safety/Custom 检测器。最终检测结果继续
使用现有违规上报链路。

```text
请求 -> Higress WASM 匹配路由
     -> 通过 HTTP hostcall 异步调用 Admin/ingest
     -> 路径统计 upsert，并合并审查任务
     -> CompliK worker 获取任务租约
     -> Browser 抓取页面
     -> Safety/Custom 审查内容
     -> AdminReporter 保存最终结果
```

## 目标

- 避免扫描和去重大量 Higress 原始日志。
- 避免在网关 WASM VM 中缓存响应 body。
- 在 Browser 或大模型开始工作前合并重复请求。
- 保留现有 CompliK Browser 和 Detector 链路。
- 审查服务不可用时不影响用户请求。
- 提供可重试、可观测的持久化任务状态。

## 非目标

- 在 WASM VM 内执行大模型推理。
- 让每个用户请求同步等待审查结果。
- 把访问观测直接当作违规结果。
- 取代对无流量 path 的定时全量扫描。

## 当前仓库边界

当前 `complik` 进程使用进程内 EventBus，没有对外接收 HTTP 任务的接口；应用
启动插件后主要等待退出信号。

Admin 服务已经提供 `POST /api/discovered-paths`，适合保存聚合后的访问路径
统计。它按照 route/path 唯一键 upsert，累加 `count`，并推进
`last_seen_at`。因此调用方必须提交增量 count，不能提交累计计数器。

Admin 还提供 `POST /api/complik-violations`。这是最终检测结果接口，不应该
用来提交待审查任务，因为创建违规结果可能触发后续 autoban 处理。

## WASM 到服务端的传输

Proxy-Wasm 插件不能在 VM 内使用 Go `net/http`、任意 socket，或依赖任意 URL
建立网络连接。它可以使用 Proxy-Wasm host API，通常是
`dispatch_http_call`，请求 Envoy/Higress 宿主向已配置的 upstream cluster 发起
异步 HTTP 请求。

概念代码如下：

```go
proxywasm.DispatchHttpCall(
    "sealos-complik-admin",
    headers,
    body,
    nil,
    500,
)
```

cluster 名称取决于部署方式。Admin Service 必须作为 Envoy/Higress upstream
可达；WASM 插件不应自行建立原始网络连接。HTTP 响应在插件的异步回调中处理，
用户请求不应等待该回调。

如果当前 Higress 版本或运行时没有暴露这个 hostcall，备选方案是使用平台支持
的 telemetry/log exporter，或者部署独立的本地 ingest 服务。不能假定 VM 内存在
任意网络 API。

## API 约定

### 已有路径观测接口

如果 Admin UI 或报表需要访问统计，可以使用 `POST /api/discovered-paths`。应按
固定时间窗口批量提交，并发送增量 count：

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

这个接口是观测存储，不是审查任务队列。

### 已实现的审查任务接口

Admin 服务现在提供独立接口 `POST /api/page-review-tasks`。它接收批量任务，在校验
并合并任务后返回 `202 Accepted`：

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

接口会返回 task ID 和 accepted/queued 数量，便于观测。不能不经校验就接受任意 URL；
优先提交 route ID 或路由 metadata，再由服务端映射到允许访问的 URL。

Worker 生命周期接口为：

```text
POST /api/page-review-tasks/claim
POST /api/page-review-tasks/{id}/complete
POST /api/page-review-tasks/{id}/fail
```

`claim` 接收 `worker_id`、`limit` 和 `lease_duration_second`。Worker 完成或失败任务
时必须带上 owner ID。租约过期后，下一次 claim 会把任务重新放回 `pending`。成功后
默认冷却时间为 10 分钟。

## 幂等和去重

两类问题使用两个 key：

```text
route_key = namespace + ingress_name + 规范化 host + 规范化 path
task_key  = route_key + content_version + policy_version
```

如果没有 `ETag` 或 `Last-Modified`，应使用按 route 的冷却时间，并限制同一路径同时
只能有一个 pending/running 任务。动态页面可能需要增加租户或设备维度，不能默认
忽略这个差异。

Admin 任务表保证 task key 唯一，并保存：

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

已有 `discovered_paths` 表可以继续保存访问次数。不能因为它有
`last_detected_at` 和 `last_detection_status` 字段，就把它当作任务队列；仍然
需要明确的任务创建和状态更新链路。

## WASM 处理规则

WASM 插件应当：

1. 在 request headers 阶段匹配实际生效的 Higress route。
2. 获取稳定的 route ID，或获取配置好的 namespace/Ingress metadata。
3. 使用与 Admin 服务一致的 host/path 规范化规则。
4. 按 route 在本地聚合观测，并按定时器或阈值批量 flush。
5. 通过 `dispatch_http_call` 使用较短超时时间调用 ingest。
6. 无论 ingest 成功或失败，都继续用户请求。

WASM VM 内的聚合状态只属于某个 worker 和某个网关副本，只能作为性能优化；持久化
去重必须在 Admin/ingest 服务或其数据库中完成。

这个触发链路中，插件不应读取或转发 HTML。任务只携带 URL 和路由 metadata，由
CompliK worker 使用现有 Browser 抓取页面。因此审查看到的是受控 Fetcher 会话，
不一定完全等同于用户的 Cookie 或登录会话。

## CompliK Worker

仓库现在包含 `PageReviewWorker` 插件。它以有限并发获取 pending 任务租约，发布带有
`ReviewTaskID` 的 `DiscoveryInfo`，复用现有 Browser -> CollectorTopic ->
DetectorTopic 链路。最终检测结果仍由现有 AdminReporter 上报；worker 只独立更新任务
状态，不阻塞用户请求。

Worker 配置已加入 `complik/config.yml`，主要配置如下：

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

Worker 收到携带相同 `ReviewTaskID` 的第一个 Detector 结果后完成任务；等待超时则记录
可重试失败。任务 ID 会贯穿 DiscoveryInfo、CollectorInfo 和 DetectorInfo，不会写入
待处理的违规结果。

本仓库没有新增 Higress WASM 二进制。部署时由 Higress 匹配实际生效的路由，并通过
Proxy-Wasm `dispatch_http_call` 调用入队接口；这个调用保持异步并且 fail-open。

如果部署环境暂时没有独立消息中间件，第一版可以直接使用 Admin 的 MySQL 任务表
作为持久化队列。后续可以替换为 Redis、NATS、Kafka 或平台已有队列，而不改变
WASM API 契约。

## 安全和失败处理

- ingest 接口使用服务间认证，优先 mTLS 或可轮换的 HMAC/token。内部第一版可在
  安全配置下使用 Basic Auth。
- 不要把长期有效的凭据直接编译进 WASM 二进制。
- 校验 route 身份和 URL host，防止 SSRF。
- 默认不要转发用户 Cookie 或 Authorization header。
- 限制请求大小、批量大小、队列长度、重试次数和单 route 速率。
- ingest 超时或 Admin 不可用时，对用户请求 fail-open，并暴露丢弃观测的指标。
- 不要在网关中同步阻塞页面审查。只有在需求明确且经过测试时，才允许本地 WASM
  规则拒绝请求。

## 混合运行模式

保留定时 `Complete + Browser` 作为覆盖基线，它能发现没有流量的 path。Higress
WASM 只负责已访问 path 和内容变化的近实时审查。

不应把现有 Higress 原始日志插件作为触发机制。它按 discovery event 发起查询，
当前没有真正匹配 `Path`，并且输出的 payload 也不是现有 Detector 所要求的
`CollectorInfo`。

## 剩余集成顺序

1. 在 Higress 中配置 route metadata 和 Admin upstream cluster。
2. 实现 WASM 路由匹配、本地聚合、异步 HTTP hostcall 和 fail-open 指标。
3. 增加重复请求、内容版本变化、namespace 隔离、任务重试以及 Admin/worker
   故障的端到端测试。

## 验收标准

- 同一个未变化 route 的重复请求，在冷却窗口内最多创建一个 pending/running 任务。
- 两个 namespace 中相同 host/path 的 route 必须保持隔离。
- 该触发链路中的 WASM 不缓存或转发 HTML body。
- Admin 变慢或不可用时不能延迟用户响应。
- 租约 owner 可以避免两个 worker 同时处理同一个任务；如果 worker 在发布 Detector
  结果后、完成租约前崩溃，Detector 上报仍是至少一次语义。
- 定时扫描仍能覆盖没有流量的 route。
