# cc-relayer

高性能 Claude Code API 中转服务，Go 语言实现。在客户端与 Anthropic API 之间提供多账号调度、身份隔离和深度防检测能力。

> 基于 [claude-relay-service](https://github.com/Wei-Shaw/claude-relay-service) 核心思路，使用 Go 完全重写并大幅增强安全层。

## 为什么选 cc-relayer

我们对比了 9 个主流开源中转项目（claude-code-relay、sub2api、CCGate、antigravity-claude-proxy、ClaudeCodeProxy、cli_proxy、LLM-API-Key-Proxy、claude-proxy、CLIProxyAPI），cc-relayer 在**请求伪装和身份隔离**层面是最全面的：

| 防检测能力 | cc-relayer | 其他项目中的最佳实现 |
|-----------|:---:|:---:|
| TLS 指纹伪装（utls Chrome Auto） | ✅ | 部分项目有（硬编码 Chrome 124 或 Firefox） |
| Stainless SDK 指纹绑定 | ✅ | 仅 1 个项目有类似（透传但不绑定） |
| Billing Header 剥离 | ✅ | 无 |
| Session 污染检测 | ✅ | 无 |
| Warmup 请求本地拦截 | ✅ | 无 |
| User ID 重写 | ✅ | 无 |
| cache_control 合规（4 块 + strip TTL） | ✅ | 无 |
| 5 小时窗口精细管理 | ✅ | 部分项目有基础实现 |
| 429 Retry-After 智能冷却 | ✅ | 3 个项目有 |
| 请求头白名单过滤 | ✅ | 1 个项目有黑名单 |
| 错误响应脱敏 | ✅ | 1 个项目有（Fuzzy 模式） |
| Ban 信号正则检测 | ✅ | 3 个项目有 |
| Per-account 独立代理 | ✅ | 1 个项目有 |

## 安全架构详解

### 1. TLS 指纹伪装

使用 [utls](https://github.com/refraction-networking/utls) 的 `HelloChrome_Auto`，自动匹配最新 Chrome 版本的 TLS ClientHello 指纹（cipher suites、extensions 顺序、GREASE 值、ALPN、HTTP/2 SETTINGS）。相比硬编码特定 Chrome 版本的方案，无需手动更新。

每个账号通过独立的 `http.Transport` 发起请求，支持 SOCKS5/HTTP CONNECT 代理，实现 IP 级别隔离。

### 2. Stainless SDK 指纹绑定

Claude Code 的底层 SDK（Stainless）会注入 `x-stainless-os`、`x-stainless-runtime`、`x-stainless-package-version` 等标识头。cc-relayer 在账号首次被使用时**捕获**这些头并存入 Redis，后续该账号的所有请求都**重放**同一套指纹，确保 Anthropic 看到的是一个行为一致的客户端。

使用 Redis `SETNX` 保证并发安全——多个请求同时到达时只有第一个写入成功，其余读取已存储值。

### 3. Billing Header 剥离

Claude Code 会在 system prompt 中注入 `x-anthropic-billing-header`，包含账单追踪信息。cc-relayer 自动检测并移除这些条目，防止上游通过 billing 信息关联到原始订阅。

### 4. Session 污染检测

当一个多轮对话的绑定账号变为不可用时，cc-relayer **不会**静默切换到另一个账号继续对话（这会暴露中转模式——同一个 session 突然换了账号身份）。而是返回错误，引导客户端开启新会话。

检测逻辑：多条消息、多部分内容、或缺少工具定义（Claude Code 新 session 必带）的请求被视为"旧 session"，强制绑定原账号。

### 5. Warmup 请求拦截

Claude Code 客户端在启动时发送 warmup 请求（内容为 `"Warmup"` 或标题生成/话题分析提示）。cc-relayer 在本地合成 SSE 响应（20ms 间隔模拟网络延迟），不消耗上游 token，不触发账号调度。

### 6. User ID 重写

每个请求的 `metadata.user_id` 被重写为与目标账号匹配的格式：
- 账号哈希部分：从真实账号 UUID 派生
- Session 部分：从 `accountID + sessionUUID` 确定性派生

确保 Anthropic 看到的 user_id 与账号身份一致，同时保持 session 内的稳定性。

### 7. cache_control 合规

强制执行 Anthropic 的缓存限制：最多 4 个 `cache_control` 块，自动剥离 `ttl` 字段。超出限制时优先移除 messages 中的缓存标记，保留 system prompt 的缓存。

### 8. 请求头白名单

只放行 8 个已知安全的头（`content-type`、`user-agent`、`anthropic-version` 等）和 `x-stainless-*` 系列。所有代理追踪头（`x-forwarded-for`、`cf-connecting-ip`、`x-real-ip` 等）被白名单机制天然过滤。

### 9. 错误响应脱敏

上游的原始错误响应会被映射为标准化错误码（E001-E015），剥离内部路由标签（如 `[relay/claude]`），防止错误信息泄露内部架构。

## 调度与容错

### 多级账号选择

```
API Key 绑定账号 → Session 粘性绑定 → 账号池最优选择
                                     (priority DESC, lastUsedAt ASC)
```

### 分级错误处理

| 错误码 | 处理策略 | 默认冷却 |
|--------|---------|---------|
| **429** | 解析 `Retry-After` / `anthropic-ratelimit-unified-reset` 头，设精确冷却 | 60s（兜底） |
| **529** | 标记 overloaded | 5 分钟 |
| **403** (ban 信号) | 标记 blocked + 不可调度 | 30 分钟 |
| **403** (其他) | 临时冷却，同账号先重试 2 次 | 10 分钟 |
| **401** | 标记 error + 异步刷新 token | 30 分钟 |

429 冷却优先级：`Retry-After` 头（秒数/HTTP日期）> `anthropic-ratelimit-unified-reset`（RFC3339）> `ERROR_PAUSE_429` 配置值。

### 5 小时窗口管理

实时捕获 `anthropic-ratelimit-unified-5h-status` 响应头：
- `allowed` — 正常
- `allowed_warning` — 可配置自动停止（`autoStopOnWarning`）
- `rejected` — 立即停止调度

窗口到期后自动恢复。Opus 模型有独立的 per-model 限速追踪。

### Sticky Session

- **Session Binding**：基于 `metadata.user_id` 中的 session UUID，将多轮对话绑定到同一账号（Redis TTL 24h）
- **Sticky Session**：基于请求内容哈希（system prompt + 首条消息），实现 prompt cache 亲和性（Redis TTL 1h）

### 重试机制

每个请求最多尝试 `MaxRetryAccounts + 1` 个不同账号（默认 3 个）。403 错误在同账号重试 2 次后再切换，避免将偶发的瞬时 403 误判为封号。

## 快速开始

```bash
# 环境变量
export ENCRYPTION_KEY="your-32-char-encryption-key-here"
export API_TOKEN="your-api-token"

# 编译运行
make build && ./cc-relayer

# 或直接
go run ./cmd/relay
```

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|------|:---:|-------|------|
| `ENCRYPTION_KEY` | 是 | - | AES 加密密钥（32 字符） |
| `API_TOKEN` | 是 | - | 下游客户端认证 token |
| `HOST` | 否 | `0.0.0.0` | 监听地址 |
| `PORT` | 否 | `3000` | 监听端口 |
| `REDIS_ADDR` | 否 | `127.0.0.1:6379` | Redis 地址 |
| `REDIS_PASSWORD` | 否 | - | Redis 密码 |
| `REDIS_DB` | 否 | `0` | Redis 数据库 |
| `LOG_LEVEL` | 否 | `info` | 日志级别（debug/info/warn/error） |
| `CLAUDE_API_URL` | 否 | `https://api.anthropic.com/v1/messages` | 上游 API 地址 |
| `CLAUDE_API_VERSION` | 否 | `2023-06-01` | API 版本 |
| `CLAUDE_BETA_HEADER` | 否 | `claude-code-20250219,...` | Beta 功能标识 |
| `REQUEST_TIMEOUT` | 否 | `300000` | 请求超时（ms） |
| `MAX_RETRY_ACCOUNTS` | 否 | `2` | 单请求最大换号次数 |
| `MAX_CACHE_CONTROLS` | 否 | `4` | 最大 cache_control 块数 |
| `STICKY_SESSION_TTL` | 否 | `3600000` | Sticky session TTL（ms） |
| `SESSION_BINDING_TTL` | 否 | `86400000` | Session binding TTL（ms） |
| `ERROR_PAUSE_401` | 否 | `1800000` | 401 冷却时长（ms） |
| `ERROR_PAUSE_403` | 否 | `600000` | 403 冷却时长（ms） |
| `ERROR_PAUSE_429` | 否 | `60000` | 429 冷却时长（ms，兜底值） |
| `ERROR_PAUSE_529` | 否 | `300000` | 529 冷却时长（ms） |

## API 端点

### 中转

```
POST /v1/messages              # Claude API 中转（需 API Key）
POST /api/event_logging/batch  # 遥测接收（返回 200）
GET  /health                   # 健康检查
```

### 管理

```
POST   /admin/login                  # JWT 登录
GET    /admin/accounts               # 账号列表
POST   /admin/accounts               # 创建账号
PUT    /admin/accounts/{id}          # 更新账号
DELETE /admin/accounts/{id}          # 删除账号
POST   /admin/accounts/{id}/refresh  # 强制刷新 token
POST   /admin/accounts/{id}/toggle   # 切换调度状态
GET    /admin/keys                   # API Key 列表
POST   /admin/keys                   # 创建 API Key
DELETE /admin/keys/{id}              # 删除 API Key
GET    /admin/status                 # 系统状态
```

## 项目结构

```
cmd/relay/              入口
internal/
  account/              账号模型、AES 加密、OAuth token 刷新
  auth/                 API Key 认证、并发控制
  config/               环境变量配置
  identity/             防检测：请求头白名单、Stainless 绑定、
                        User ID 重写、Billing 剥离、Warmup 拦截
  ratelimit/            5h 窗口追踪、Opus 限速、自动恢复
  relay/                请求管线、错误脱敏、SSE 流转发
  scheduler/            账号选择、Sticky session、Session binding
  server/               HTTP 服务器、Admin API
  store/                Redis 操作
  transport/            utls TLS 指纹、代理拨号、连接池
```

## 请求流程

```
客户端 → 认证中间件 → Warmup 拦截检查
  → Session binding 查找 → Scheduler 选择账号
  → Token 刷新（如需） → 身份变换（User ID / Headers / Billing / Cache）
  → 上游请求（utls + proxy） → 流式/JSON 响应转发
  → 速率限制头捕获 → 429/529/403/401 分级处理
```

## Redis 兼容性

所有 Redis key 格式与 Node.js 版本兼容，支持零停机迁移：

```
claude:account:{id}              # 账号数据
claude:account:index             # 账号索引
apikey:{id}                      # API Key
apikey:hash_map                  # Key 哈希映射
sticky_session:{hash}            # Sticky session
original_session_binding:{uuid}  # Session binding
stainless_headers:{accountId}    # Stainless 指纹
concurrency:{keyId}              # 并发计数
token_refresh_lock:claude:{id}   # Token 刷新锁
```

## 开发

```bash
make build    # 编译
make run      # 编译并运行
make test     # 运行测试
make lint     # go vet 检查
make deps     # 依赖整理
```

## License

MIT
