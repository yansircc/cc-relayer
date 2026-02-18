# cc-relayer

Claude Code API relay service written in Go. Acts as middleware between clients and upstream Claude API, providing multi-account management, scheduling, and anti-detection capabilities.

## Features

- **Multi-account scheduling** — round-robin with priority, sticky sessions, and session binding
- **TLS fingerprinting** — utls Chrome fingerprint to match Claude Code's TLS profile
- **Per-account proxy** — SOCKS5 and HTTP CONNECT with connection pooling
- **Identity transformation** — user_id rewrite, stainless header binding, system prompt injection
- **Rate limit handling** — 5-hour window tracking, Opus weekly cost, atomic concurrency control
- **Error sanitization** — standardized error codes, internal tag stripping
- **OAuth token refresh** — distributed locking, auto-refresh before expiry
- **AES-256-CBC encryption** — Node.js compatible, scrypt key derivation

## Quick Start

```bash
# Set required environment variables
export ENCRYPTION_KEY="your-32-char-encryption-key-here"
export JWT_SECRET="your-jwt-secret-here"
export ADMIN_PASSWORD="your-admin-password"

# Build and run
make build
./cc-relayer

# Or with go run
go run ./cmd/relay
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ENCRYPTION_KEY` | Yes | - | AES encryption key (32 chars) |
| `JWT_SECRET` | Yes | - | JWT signing secret |
| `ADMIN_PASSWORD` | Yes | - | Admin login password |
| `HOST` | No | `0.0.0.0` | Listen host |
| `PORT` | No | `3000` | Listen port |
| `REDIS_ADDR` | No | `127.0.0.1:6379` | Redis address |
| `REDIS_PASSWORD` | No | - | Redis password |
| `REDIS_DB` | No | `0` | Redis database |
| `API_KEY_PREFIX` | No | `cr_` | API key prefix |
| `LOG_LEVEL` | No | `info` | Log level (debug/info/warn/error) |
| `REQUEST_TIMEOUT` | No | `300000` | Request timeout in ms |
| `MAX_RETRY_ACCOUNTS` | No | `2` | Max account switches per request |
| `STICKY_SESSION_TTL` | No | `3600000` | Sticky session TTL in ms |

## API Endpoints

### Relay

```
POST /v1/messages          # Claude API relay (requires API key)
POST /api/event_logging/batch  # Telemetry sink (returns 200)
GET  /health               # Health check
```

### Admin

```
POST   /admin/login                  # JWT login
GET    /admin/accounts               # List accounts
POST   /admin/accounts               # Create account
PUT    /admin/accounts/{id}          # Update account
DELETE /admin/accounts/{id}          # Delete account
POST   /admin/accounts/{id}/refresh  # Force token refresh
POST   /admin/accounts/{id}/toggle   # Toggle schedulable
GET    /admin/keys                   # List API keys
POST   /admin/keys                   # Create API key
DELETE /admin/keys/{id}              # Delete API key
GET    /admin/status                 # System status
```

## Architecture

```
cmd/relay/          Entry point
internal/
  account/          Account model, encryption, token refresh
  auth/             API key validation, concurrency control
  config/           Environment config
  identity/         Anti-detection: headers, prompts, signatures, stainless, user-agent
  ratelimit/        5h window, Opus limits, cost tracking
  relay/            Request pipeline, usage parsing, error sanitization
  scheduler/        Account selection, sticky sessions
  server/           HTTP server, admin endpoints
  store/            Redis operations
  transport/        utls TLS, proxy dialing, connection pool
```

## Request Flow

```
Client (cr_ key) -> Auth middleware -> Warmup check -> Session binding
  -> Scheduler (select account) -> Token refresh (if needed)
  -> Identity transform -> Upstream request (via utls + proxy)
  -> Stream/JSON response -> Usage capture -> Cost accumulation
```

## Redis Compatibility

All Redis key patterns are compatible with the Node.js version for zero-downtime migration:

- `claude:account:{id}` / `claude:account:index`
- `apikey:{id}` / `apikey:hash_map`
- `sticky_session:{hash}` / `original_session_binding:{uuid}`
- `concurrency:{keyId}` / `usage:opus:weekly:{keyId}:{week}`
- `token_refresh_lock:claude:{accountId}`

## Development

```bash
make build    # Build binary
make run      # Build and run
make test     # Run tests
make lint     # Run go vet
make deps     # Tidy dependencies
```

## License

MIT
