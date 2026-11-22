# Buzzit

A standalone private messenger with two things the others don't have:

- **Buzz.** Friends you trust can make your phone ring **even when it's on silent**. There are three levels (Nudge, Buzz, Emergency). Each one is gated by your consent per friend (Buzz Passes), plus quiet hours and rate limits.
- **Buzzit Platform.** A free public API. Apps and bots can message and nudge users who opt in, with signed webhooks.

It runs as **one server binary** (with its own embedded database) plus the **Android app**. No Firebase, cloud database or other third-party service is required.

## What works today
| Area | Features |
|---|---|
| Accounts | Register and sign in, multiple devices, remote sign-out, password change (signs out other devices) |
| Social | Username search, friend requests, blocking (silent, kills every buzz right), privacy toggles for read receipts and last seen |
| Messaging | 1:1 and group chats (up to 256 members, owner/admin roles), text, photos, videos, files and voice notes, replies, reactions, edit (15 min), delete for everyone, read receipts, typing, presence, mute, unread counts |
| Reliability | Offline outbox with retries, idempotent sends, an ordered per-user event log with gap replay (`/v1/sync`), reconnect with backoff |
| Buzz | Per-friend levels and quiet hours. Rings on the alarm stream through silent and Do Not Disturb, full-screen over the lock screen. "I'm OK" / Reply / Snooze 5 min. The sender sees delivered → ringing → answered. Answering on one device stops all your devices |
| Platform | Apps with bot accounts, consent scopes, free-tier quotas, Bot API (REST, polling, WebSocket), HMAC-signed webhooks with retries and SSRF protection |
| Security | bcrypt passwords, hashed bearer tokens, message bodies and event logs encrypted at rest (AES-256-GCM), private media access checks, rate limits on auth |

Not built yet (see the [roadmap](docs/PRODUCT_AND_ARCHITECTURE.md)): end-to-end encryption, voice/video calls, the iOS app (only the buzz snippets exist in `apps/ios`), a web client, the developer portal UI, translations.

## Run the server
```bash
cd services
go run ./cmd/buzzit-server --addr :8080 --data ./data
```
Or with Docker:
```bash
docker compose -f infra/docker-compose.yml up -d
```
Everything lives in the data directory: `buzzit.db` (SQLite), `data.key` (the at-rest encryption key, **back it up**) and `media/`. In production, run it behind HTTPS: a TLS proxy, or `--tls-cert`/`--tls-key`.

| Flag / env | Default | |
|---|---|---|
| `--addr` / `BUZZIT_ADDR` | `:8080` | listen address |
| `--data` / `BUZZIT_DATA` | `./data` | data directory |
| `--max-upload-mb` / `BUZZIT_MAX_UPLOAD_MB` | `50` | upload limit |
| `--fcm-service-account` / `BUZZIT_FCM_SERVICE_ACCOUNT` | none | optional Firebase key for push wake-ups |
| `BUZZIT_DATA_KEY` | none | 64-hex key; overrides `data.key` |

## Build the Android app
Requirements: JDK 17+ and the Android SDK (platform 35).
```bash
cd apps/android
./gradlew assembleDebug
```
The APK is written to `app/build/outputs/apk/debug/app-debug.apk`. On the sign-in screen, tap **Server** to enter your server's address. The emulator default is `http://10.0.2.2:8080`. Debug builds allow `http://`; release builds require `https://`.

Optional Gradle properties (in `~/.gradle/gradle.properties`):
- `buzzit.serverUrl`: the default server address
- `buzzit.fcm.projectId`, `buzzit.fcm.appId`, `buzzit.fcm.apiKey`, `buzzit.fcm.senderId`: turn on Firebase push. Without them, the app keeps its own background connection open, shown as a small ongoing notification.
- `buzzit.keystore`, `buzzit.keystorePassword`, `buzzit.keyAlias`, `buzzit.keyPassword`: release signing

To get buzzes through silent mode, open **Settings → Who can buzz me** and allow the three items there: notifications, Do Not Disturb access and full-screen alerts.

## Tests
```bash
cd services && go test -race ./...
cd apps/android && ./gradlew testDebugUnitTest lintDebug
```
- The Go suite includes an end-to-end test. It runs real HTTP and WebSockets through accounts, friends, chats, media, receipts, buzz, groups, blocks and the full Platform flow, including signed webhooks.
- `ServerContractTest` runs the Android API layer against a real server. Start one and set `BUZZIT_TEST_SERVER=http://127.0.0.1:8095` (CI does this).

## Repository layout
| Path | What |
|---|---|
| `services/` | Go server. `internal/{auth,social,chat,media,realtime,buzz,platform,push}` are modules; `internal/server` wires them; `cmd/buzzit-server` is the binary |
| `apps/android/` | Kotlin + Jetpack Compose app (Room cache and outbox, OkHttp WebSocket, alarm-stream buzz alerts) |
| `apps/ios/` | Swift buzz snippets (Critical / Time Sensitive). The full app is not built yet |
| `api/openapi.yaml` | Public Platform API |
| `docs/CLIENT_API.md` | First-party client API and realtime events |
| `docs/` | Product and architecture blueprint, ADRs |
