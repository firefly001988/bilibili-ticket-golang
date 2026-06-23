# Employer–Worker 集群

桌面应用是雇主控制面，`ticket-worker` 是执行面。会员购的循环下单只在 Worker 中运行；BWS 仍由桌面应用执行。

雇主与 Worker 之间通过 **gRPC + mTLS** 通信，不再使用 HTTP Bearer Token。每个 Worker 拥有自签名 CA 签发的 TLS 证书，雇主持有客户端证书完成双向认证。

## 进程与文件

- `bilibili-ticket-golang`：Wails 雇主桌面应用、SQLite 持久化、规划器（Planner）、调度器（Dispatcher）。
- `ticket-worker serve --config worker.json`：gRPC 驻守服务，接收并执行不可变订单。
- `ticket-worker run --task task.json`：（遗留模式）执行单个任务并向 stdout 输出 JSON 结果。
- `data/employer.db`：权限 `0600` 的雇主数据库（SQLite + WAL）。
- Worker 的 `success-orders.jsonl`：先 fsync 再报告成功，不写入任何凭据。
- Worker 的 `worker.log`：权限 `0600`，脱敏并按 5 MiB 轮转。
- `data/local-worker/`：本机 Worker 的 TLS 证书、配置和运行数据。

发布包同时包含雇主和 Worker 二进制文件。本机 Worker（ID=`local`）由雇主自动启动并管理，协议与远程 Worker 完全相同。

## Worker 配置

```json
{
  "listen": "127.0.0.1:18080",
  "dataDir": "./worker-data",
  "pollIntervalSec": 15,
  "leaseDurationSec": 180,
  "workerId": "worker-01",
  "version": "build-version",
  "pluginDir": "./plugins",
  "captchaPlugin": "captcha-plugin",
  "calibrateClock": true,
  "pluginVersion": "captcha-plugin-version",
  "algorithmVersion": "ticket-algorithm-version",
  "caCertPEM": "-----BEGIN CERTIFICATE-----\n...",
  "serverCertPEM": "-----BEGIN CERTIFICATE-----\n...",
  "serverKeyPEM": "-----BEGIN EC PRIVATE KEY-----\n..."
}
```

TLS 证书字段（`caCertPEM`、`serverCertPEM`、`serverKeyPEM`）如果为空，Worker 启动时会在 `dataDir` 下自动生成 CA 和服务端证书，并持久化到磁盘。手动提供这些字段可实现配置完全自包含（用于远程部署）。

轮询间隔必须为 10–60 秒。有效租约至少为 `max(180 秒, 3 × 轮询间隔)`；Status 调用会续租。租约或任务 deadline 到期时，Worker 自行取消任务。

`serve` 模式会启用 Bilibili 时钟校准。配置 `captchaPlugin` 后，Worker 从 `pluginDir` 加载与桌面端相同协议的验证码插件，并在 Health 接口报告插件版本；插件不可用时 Worker 拒绝启动，避免运行到验证码阶段才静默失效。

## gRPC API

Worker 暴露以下 gRPC 服务（定义见 `cluster/worker/proto/worker.proto`）。所有 RPC 均通过 mTLS 认证——客户端必须持有由同一 CA 签发的有效证书。

| RPC | 用途 |
| --- | --- |
| `Health` | 查询 Worker 版本、插件能力、时钟校准状态和当前活动 Attempt。 |
| `Submit` | 下发不可变 `ExecutionSpec`。幂等：同 `attemptId` 同 `specHash` 返回已有任务；同 ID 不同内容返回 `ALREADY_EXISTS`。Worker 忙碌时返回 `RESOURCE_EXHAUSTED`。 |
| `Status` | 查询任务状态与完整结果，同时续租。任务不存在返回 `NOT_FOUND`。 |
| `Logs` | 返回任务的全部日志条目（阶段、消息、错误码、是否可重试）。 |
| `Stop` | 请求取消运行中任务，进入 `stopping` 状态。 |
| `Ack` | 确认已收到终态结果，Worker 清理资源。仅终态任务可 Ack，否则返回 `FAILED_PRECONDITION`。 |
| `Heartbeat` | 双向流：Worker 定期发送心跳，雇主回显确认。任一端超时未收到消息则视为断连，触发自动重连。心跳消息携带当前活动 Attempt ID。 |

`attemptId + specHash` 是幂等键。每个 Worker 最多同时运行一个非终态 Attempt。

## 任务规范（ExecutionSpec）

```json
{
  "attemptId": "attempt-123",
  "intentId": "intent-456",
  "projectId": 100,
  "screenId": 200,
  "skuId": 300,
  "buyers": [{ "logicalId": "alice", "buyerId": 9001, "name": "Alice", "type": 2 }],
  "startMode": "scheduled",
  "startAt": "2026-07-01T12:00:00+08:00",
  "deadline": "2026-07-01T12:20:00+08:00",
  "intervalMs": 500,
  "credentials": {
    "cookies": { "SESSDATA": "..." },
    "cookieJar": [],
    "refreshToken": "...",
    "version": 1,
    "deviceProfile": {}
  }
}
```

Attempt 始终保留完整购票人列表，Worker 不会拆分或修改订单形状。调度时 Dispatcher 通过 `buyerResolver` 自动用数据库中存储的完整实名信息（未脱敏的身份证号、手机号）填充每个购票人。

## TLS 与 Worker 管理

### 本机 Worker

雇主启动时自动检测 `data/local-worker/` 下的 TLS 证书；不存在则生成自签名 CA 和服务端/客户端证书（ECDSA P-256）。本机 Worker 的 ID 固定为 `local`，监听 `127.0.0.1:18080`，不可手动删除或断开。

### 远程 Worker 部署

**方式一：生成自包含配置（推荐）**

雇主通过 `GenerateRemoteWorkerConfig` 生成一份完整的 Worker 配置（含 CA 证书、服务端证书、客户端证书），以 Base4096 编码为一串可复制粘贴的字符串。将这段字符串在目标机器上解码后保存为 `worker.json`，即可用 `ticket-worker serve --config worker.json` 启动。

雇主端通过 `AddWorkerFromEncodedConfig` 解码同一配置字符串，提取雇主端 TLS 凭据后自动建立 mTLS 连接。

**方式二：手动配置**

手动提供 CA 证书、客户端证书和客户端私钥（PEM 格式），调用 `AddWorker` 添加 Worker。Worker 端需要相同的 CA 和服务端证书。

### Worker 连接管理

| 操作 | 说明 |
| --- | --- |
| `AddWorker` | 添加远程 Worker（需提供地址、TLS 凭据）。添加后同步拨号验证连通性。 |
| `UpdateWorker` | 更新 Worker 的地址、TLS 配置或角色。要求 Worker 无活跃 Attempt。不可编辑本机 Worker。 |
| `DeleteWorker` | 删除 Worker 及其 TLS 配置。要求 Worker 无活跃 Attempt。不可删除本机 Worker。 |
| `DisconnectWorker` | 断开 gRPC 连接但保留 TLS 配置，阻止调度器的自动重连。 |
| `ReconnectWorker` | 重新建立 gRPC 连接，最多重试 5 次（每次间隔 5 秒）。 |

### 心跳与健康检查

Employer 与每个已连接 Worker 维护一条 gRPC 双向 Heartbeat 流。心跳间隔 5 秒，超时阈值 15 秒。调度器每 15 秒执行一次 `Reconcile`，刷新资源状态并处理 Attempt 生命周期。失联 Worker 的活跃 Attempt 会被故障转移到其他健康的 Worker。

## 规划与冲突

- 宏任务（MacroTask）属于一个 SKU，活动日期（EventDay）必须人工确认后（`eventDayConfirmed=true`）才可调度。
- 单订单人数上限按 `API 返回值 → 用户覆盖值（CapacityOverride）→ 默认 4` 解析。
- 准点阶段（Punctual）智能合并使用确定性的 best-fit-decreasing 算法，原始购票组不拆分。
- 回流阶段（Reflow）不跨组合并；仅当购票组的 `allowSplit=true` 时拆成单人 Intent。
- 同一 Intent 可用不同账号和 Worker 创建多个副本（由 `desiredReplicas` 控制）；第一个确认成功的订单获胜，兄弟副本立即被 Dispatcher 停止。
- 不同 Intent 共享 `BuyerDayKey`（购票人 × 活动日期）时按宏任务优先级串行执行，落实"同一人同一天已有任意票后不能再参与另一订单"的约束。
- `SwitchToReflow` 由用户手动触发：雇主等待所有准点 Attempt 停止后才生成回流 Intent。

## 账号管理

### 扫码登录

支持独立扫码添加账号：调用 `BeginAccountLogin` 获取二维码，`PollAccountLogin` 轮询扫码状态。登录会话 5 分钟过期。登录成功后自动导入该账号的已有购票人列表，账号以 UID 派生的 `bili-{uid}` 作为唯一 ID。

### 凭据导入

支持标准凭据 JSON 导入（`ImportAccount`），格式包含 cookies、cookieJar、refreshToken、deviceProfile。账号以 UID 派生的 `bili-{uid}` 作为唯一 ID，与扫码登录的账号自动去重。

### 主账号同步

雇主 UI 的登录态通过 `SyncMainAccount` 自动同步到账号池。以 UID 为 ID，每次同步递增凭据版本号（`credentialVersion`）。同步后自动清理遗留的匿名迁移账号。

### 购票人同步

| 操作 | 说明 |
| --- | --- |
| `SyncAccountBuyers(accountID)` | 导入指定账号的购票人列表（含完整实名信息——身份证号、手机号）。手机号或身份证号仍为掩码状态（含 `*`）的购票人不会被持久化。 |
| `SyncAllAccountBuyers()` | 同步所有启用账号的购票人，跨账号按姓名+身份证号+证件类型自动去重。 |
| `SyncBuyerToAccount(logicalBuyerID, accountID)` | 将逻辑购票人同步到指定 Bilibili 账号（在远端创建购票人）。 |
| `SyncBuyerToAllAccounts(logicalBuyerID)` | 将逻辑购票人同步到所有已启用的 Bilibili 账号。 |

调度时，如果购票人尚未在目标账号上配置，`buyerResolver` 会自动使用数据库中的完整实名数据在 Bilibili 端创建购票人（`confirmed=true`），无需用户手动干预。

## 故障转移

每个账号有独立 Cookie Jar 和稳定设备指纹（`deviceProfile`）。主资源（`role=primary`）正常负载均衡，后备资源（`role=standby`）平时完全待机。

| 故障类型 | 处理策略 |
| --- | --- |
| Cookie 失效 | 换账号（同角色下选择其他可用账号）。 |
| HTTP 412、验证码失败 | 换 Worker（标记当前 Worker 为失败，冷却期内不分配新任务）。 |
| 心跳丢失（Worker 失联） | 该 Worker 使用的账号隔离到旧租约 × 安全余量结束；Dispatcher 自动将 Attempt 故障转移到其他 Worker。 |
| 账号被风控 | 冷却至少 5 分钟，在正常及后备账号全部耗尽后才重新候选。 |

## 迁移

首次启动将旧单账号和会员购任务事务性迁入 `data/employer.db`。旧任务按 `projectId/screenId/skuId/start/expire` 合并，每条旧 Buyers 列表成为不可拆购票组；默认一个副本、关闭智能合并，并标记 `needsReview`。

迁移幂等且保留原 `data/store.bin`。BWS、通知、语言和旧设置继续由原 MessagePack 存储管理。

## 安全边界

- Worker 间通过 mTLS 认证，不共享证书私钥。CA 私钥（`ca-key.pem`）仅存储在雇主本机，不随配置传输到远程 Worker。
- 不把 TLS 私钥或账号凭据提交到版本控制系统。
- Worker 不恢复活动任务或失败结果，只加载去凭据化的成功索引（`success-orders.jsonl`）。
- SQLite 数据库按既定方案明文保存凭据，依赖数据库文件的 `0600` 权限和雇主本机文件系统保护。
- 远程 Worker 应部署在受控网络内，通过防火墙或 VPN 限制 gRPC 端口的访问来源。
