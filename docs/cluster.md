# Employer–Worker 集群

桌面应用是雇主控制面，`ticket-worker` 是执行面。会员购的循环下单只在 Worker 中运行；BWS 仍由桌面应用执行。

## 进程与文件

- `bilibili-ticket-golang`：Wails 雇主、SQLite、规划器和 Dispatcher。
- `ticket-worker run --task task.json`：执行一个不可变订单并向 stdout 输出 JSON 结果。
- `ticket-worker serve --config worker.json`：单活动任务 HTTP 驻守服务。
- `data/employer.db`：权限 `0600` 的雇主数据库。
- Worker 的 `success-orders.jsonl`：先 fsync 再报告成功，且不写入任何凭据。
- Worker 的 `worker.log`：权限 `0600`，脱敏并按 5 MiB 轮转。

发布包同时包含雇主和 Worker。本机 Worker 由雇主通过 loopback 自动启动，协议与远程 Worker 完全相同。

## Worker 配置

```json
{
  "listen": "127.0.0.1:18080",
  "bearerKey": "每台机器独立的随机密钥",
  "dataDir": "./worker-data",
  "pollIntervalSec": 15,
  "leaseDurationSec": 180,
  "workerId": "worker-01",
  "version": "build-version",
  "pluginVersion": "captcha-plugin-version",
  "algorithmVersion": "ticket-algorithm-version"
}
```

轮询间隔必须为 10–60 秒。有效租约至少为 `max(180 秒, 3 × 轮询间隔)`；状态查询会续租。租约或任务 deadline 到期时，Worker 自行取消任务。

Worker 仅提供 HTTP。远程部署必须用可信反向代理终止 HTTPS，并通过防火墙或私网限制监听地址。有控制密钥即能完全控制对应 Worker。

## HTTP API

所有请求都要求 `Authorization: Bearer <worker-key>`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/v1/tasks` | 下发不可变 Attempt。 |
| `GET` | `/v1/tasks/{attemptId}` | 查询状态和完整结果，同时续租。 |
| `POST` | `/v1/tasks/{attemptId}/stop` | 进入 `stopping` 并取消 Executor。 |
| `POST` | `/v1/tasks/{attemptId}/ack` | 确认已收到终态结果。 |
| `GET` | `/v1/health` | 查询版本、插件能力和活动 Attempt。 |

`attemptId + specHash` 是幂等键。同 ID 不同不可变内容返回 `409 Conflict`，成功后重启仍然如此。每个 Worker 最多有一个非终态 Attempt。

## 任务示例

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

Attempt 始终保留完整购票人列表，Worker 不会拆分或修改订单形状。

## 规划与冲突

- 宏任务属于一个 SKU，活动日期必须人工确认。
- 单订单人数上限按 `API → 用户覆盖 → 默认 4` 解析。
- 准点智能合并使用确定性的 best-fit-decreasing，原始购票组不拆分。
- 回流不跨组合并；仅当 `allowSplit=true` 时拆成单人 Intent。
- 同一 Intent 可用不同账号和 Worker 创建多个副本；第一个确认成功的订单获胜，兄弟副本立即停止。
- 不同形状共享 `BuyerDayKey` 时按宏任务优先级串行，落实“同一人同一天已有任意票后不能再参与另一订单”的约束。
- 准点到回流由用户手动切换。雇主等待准点 Attempt 停止或租约失效后才生成回流 Intent。

## 账号和故障转移

每个账号有独立 Cookie Jar 和稳定设备配置，可通过独立扫码或标准凭据 JSON 加入。缺失账号购票人映射时禁止调度；调用 Bilibili 创建购票人必须由用户显式确认。

主资源正常均衡，后备资源平时完全待机。Cookie 失效换账号；HTTP 412、验证码失败、心跳丢失换机器。失联 Worker 使用过的账号会隔离到旧租约加安全余量结束。风控账号至少冷却五分钟，并在正常及后备账号耗尽后才重新候选。

## 迁移

首次启动将旧单账号和会员购任务事务性迁入 `data/employer.db`。旧任务按 `projectId/screenId/skuId/start/expire` 合并，每条旧 Buyers 列表成为不可拆购票组；默认一个副本、关闭智能合并，并标记 `needsReview`。

迁移幂等且保留 `data/store.bin`。BWS、通知、语言和旧设置继续由原存储管理。

## 安全边界

- 不共享 Worker 密钥，不把密钥或凭据提交到版本库。
- 不把 Worker HTTP 直接暴露到公网。
- Worker 不恢复活动任务或失败结果，只加载无凭据的成功索引。
- SQLite 第一版按既定方案明文保存凭据，依赖数据库文件的 `0600` 权限保护。
