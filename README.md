# cc-relayer

High-performance Claude Code API relay written in Go. Provides multi-account scheduling, identity isolation, and deep anti-detection between Claude Code clients and the Anthropic API.

## Installation

### Download binary

Download the latest binary for your platform from [GitHub Releases](https://github.com/yansircc/cc-relayer/releases), then:

```bash
chmod +x cc-relayer-*
mv cc-relayer-* /usr/local/bin/cc-relayer
```

Available platforms: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.

### Build from source

```bash
git clone https://github.com/yansircc/cc-relayer.git
cd cc-relayer
make build
```

Requires Go 1.24+ and a running Redis instance.

## Quick Start

### 1. Start the server

You need two secrets — pick any strong strings you like:

- **`ENCRYPTION_KEY`** — used to encrypt tokens stored in Redis (any length; longer is better)
- **`API_TOKEN`** — the password your Claude Code clients will use to authenticate with the relay

```bash
# Generate secrets (or just make up your own)
export ENCRYPTION_KEY=$(openssl rand -hex 16)
export API_TOKEN=$(openssl rand -hex 16)

# Print them so you can save them
echo "ENCRYPTION_KEY=$ENCRYPTION_KEY"
echo "API_TOKEN=$API_TOKEN"

./cc-relayer
```

The server listens on `0.0.0.0:3000` by default. Redis must be available at `127.0.0.1:6379` (configurable).

### 2. Add an account

Open [claude.ai](https://claude.ai) in your browser, then run this in the browser console (F12 → Console):

```js
fetch("https://YOUR_SERVER/admin/accounts/oauth", {
  method: "POST",
  headers: { "Content-Type": "application/json", "x-api-key": "YOUR_API_TOKEN" },
  body: JSON.stringify({ sessionKey: document.cookie.match(/sessionKey=([^;]+)/)?.[1] })
}).then(r => r.json()).then(console.log)
```

Replace `YOUR_SERVER` and `YOUR_API_TOKEN`. On success you'll see:

```json
{ "id": "uuid", "name": "user@gmail.com", "email": "user@gmail.com", "status": "active" }
```

Submitting the same account again updates its tokens instead of creating a duplicate.

### 3. Configure Claude Code

Point your Claude Code client to the relay:

```bash
export ANTHROPIC_BASE_URL=http://YOUR_SERVER
export ANTHROPIC_API_KEY=YOUR_API_TOKEN

claude
```

That's it. The relay handles account selection, token refresh, and all identity details automatically.

## Admin API

All admin endpoints require the same `API_TOKEN` via `x-api-key` header or `Authorization: Bearer` header.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/admin/accounts/oauth` | Add/update account via Cookie OAuth |
| `GET` | `/admin/accounts` | List all accounts (without tokens) |
| `DELETE` | `/admin/accounts/{id}` | Delete an account |

## Security

cc-relayer implements multiple layers of protection to keep your accounts safe:

- **TLS fingerprinting** — Uses [utls](https://github.com/refraction-networking/utls) with `HelloChrome_Auto` to match genuine Chrome TLS handshakes, auto-updating with new versions.
- **Per-account isolation** — Each account uses its own HTTP transport with optional SOCKS5/HTTP proxy, providing IP-level separation.
- **SDK fingerprint binding** — Captures and replays Stainless SDK headers per account for consistent client identity.
- **Billing header stripping** — Automatically removes billing tracking injected by Claude Code.
- **Session integrity** — Refuses to silently switch accounts mid-conversation; detects stale sessions and returns clear errors.
- **Warmup interception** — Responds to warmup requests locally without touching the upstream API.
- **User ID rewriting** — Deterministically rewrites `metadata.user_id` to match the target account.
- **Header allowlisting** — Only forwards known-safe headers; proxy-tracking headers are filtered by design.
- **Error sanitization** — Maps upstream errors to standardized codes, stripping internal details.

## Scheduling & Fault Tolerance

### Account Selection

```
Bound account → Sticky session → Pool selection (priority DESC, lastUsedAt ASC)
```

### Error Handling

| Code | Strategy | Default Cooldown |
|------|----------|-----------------|
| **429** | Parse `Retry-After` / `anthropic-ratelimit-unified-reset` | 60s (fallback) |
| **529** | Mark overloaded | 5 min |
| **403** (ban signal) | Mark blocked | 30 min |
| **401** | Mark error + async token refresh | 30 min |

### 5-Hour Window

Tracks `anthropic-ratelimit-unified-5h-status` headers in real-time. Accounts in `rejected` state are removed from scheduling and automatically recover when the window expires.

### Sticky Sessions

- **Session binding** — Binds multi-turn conversations to the same account via session UUID (TTL 24h)
- **Sticky session** — Routes similar prompts to the same account for prompt cache affinity (TTL 1h)

## Environment Variables

| Variable | Required | Default | Description |
|----------|:--------:|---------|-------------|
| `ENCRYPTION_KEY` | Yes | - | Secret for encrypting tokens in Redis |
| `API_TOKEN` | Yes | - | Password for client and admin authentication |
| `HOST` | No | `0.0.0.0` | Listen address |
| `PORT` | No | `3000` | Listen port |
| `REDIS_ADDR` | No | `127.0.0.1:6379` | Redis address |
| `REDIS_PASSWORD` | No | - | Redis password |
| `REDIS_DB` | No | `0` | Redis database |
| `LOG_LEVEL` | No | `info` | Log level (debug/info/warn/error) |
| `REQUEST_TIMEOUT` | No | `300000` | Request timeout in ms |
| `MAX_RETRY_ACCOUNTS` | No | `2` | Max account switches per request |

## Project Structure

```
cmd/relay/              Entry point
internal/
  account/              Account model, AES crypto, OAuth token refresh
  auth/                 API token authentication
  config/               Environment variable config
  identity/             Anti-detection: header allowlist, SDK binding,
                        user ID rewriting, billing stripping, warmup interception
  ratelimit/            5h window tracking, Opus rate limits, auto-recovery
  relay/                Request pipeline, error sanitization, SSE streaming
  scheduler/            Account selection, sticky sessions, session binding
  server/               HTTP server, admin API
  store/                Redis operations
  transport/            utls TLS fingerprinting, proxy dialing, connection pool
```

## License

MIT
