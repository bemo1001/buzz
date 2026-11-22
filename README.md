# Buzzit

Private, end-to-end-encrypted messaging with two signature features:

- **Buzz:** friends you trust can make your phone ring even on silent. There are three levels (Nudge, Buzz, Emergency), each gated by your consent ("Buzz Passes"), quiet hours and rate limits.
- **Buzzit Platform:** a free public API (Bot API, Notify API, Login with Buzzit) so other apps can message and buzz users who opt in.

Full product and architecture blueprint: [`docs/PRODUCT_AND_ARCHITECTURE.md`](docs/PRODUCT_AND_ARCHITECTURE.md).

## Repository layout
| Path | What |
|---|---|
| `api/openapi.yaml` | Public Platform API v1 (source of truth for docs and SDKs) |
| `services/` | Go backend. `internal/buzz` holds the consent, quiet-hours and rate-limit engine and the HTTP API. `internal/webhook` handles webhook signing. `cmd/buzz-svc` is the service binary |
| `apps/android/` | Kotlin Buzz alert path (alarm stream, DND bypass, full-screen intent) |
| `apps/ios/` | Swift Notification Service Extension (Critical / Time Sensitive) and permission flow |
| `infra/docker-compose.yml` | Local stack: Postgres, Redis, Redpanda, Scylla, MinIO, buzz-svc |
| `docs/adr/` | Architecture decision records |

## Quick start
```bash
cd services && go test ./...
go run ./cmd/buzz-svc     # seeds demo users alice & bob (alice granted bob Emergency)
curl -X POST localhost:8081/v1/buzz -H 'X-Buzzit-User: bob' \
     -d '{"recipient":"alice","level":"emergency","note":"Call me!"}'
```
In production the API gateway sets `X-Buzzit-User` after authenticating the caller. Clients never send it themselves.

## Roadmap
Phase 0 (foundations) is in progress. See section 5 of the blueprint.
