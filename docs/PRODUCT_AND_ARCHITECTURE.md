# Buzzit — Production Plan

## Context
Greenfield project (`/home/user` is empty). Goal: a production-grade private messaging app ("like Messenger") named **Buzzit** with two differentiators:
1. **Buzz** — a friend can trigger a loud, attention-grabbing alert on your phone *even when it is on silent*.
2. **Buzzit Platform** — a free public API/SDK so third-party apps can send messages and buzzes through Buzzit (like Twilio/Telegram Bot API, but free tier-first).

This plan is a full product + engineering blueprint, not an MVP.

---

## 1. Product Definition

### 1.1 Core messaging (parity with Messenger/WhatsApp)
- 1:1 chats, group chats (up to 1,024 members), channels (broadcast, unlimited followers)
- Text, emoji, reactions, replies/threads, edits (15 min), delete-for-everyone, forwarding, mentions
- Media: photos, video, voice notes (with waveform + transcription), files up to 2 GB, GIF/sticker search, location (one-off + live)
- Read receipts, typing indicators, online/last-seen (all individually togglable)
- Voice & video calls (1:1 and group up to 32), screen share
- Disappearing messages (24h / 7d / 90d), view-once media
- Message search (on-device for E2EE chats), pinned chats/messages, folders, archive, mute
- Multi-device: up to 4 linked devices + web + desktop (Electron/Tauri)
- Stories (24h) — phase 2 of GA
- Contact discovery via phone (hashed, private set intersection) or @username

### 1.2 Buzz (signature feature)
| Concept | Detail |
|---|---|
| **Buzz levels** | `Nudge` (normal notification + haptic), `Buzz` (loud sound + strong vibration pattern, breaks Focus/DND where allowed), `Emergency Buzz` (bypasses silent/DND, repeats until acknowledged) |
| **Consent ("Buzz Pass")** | Recipient must explicitly grant each friend a max level. Default for new friends = `Nudge`. Emergency requires per-person approval and can be revoked anytime |
| **Acknowledge** | Full-screen alert with "I'm OK / Call back / Snooze 5 min". Sender sees delivery → rang → acknowledged state |
| **Anti-abuse** | Rate limits (e.g. 3 Buzz / 10 min per pair, Emergency 3/day), auto-downgrade on repeated spam reports, quiet-hours with Emergency-only override, block instantly kills all buzz rights |
| **Customization** | Custom buzz sounds/vibration patterns per friend, "Buzz with message" (short text on full-screen), location attach for Emergency |
| **Family/Safety mode** | Guardian ↔ dependent pairing with pre-approved Emergency, "Buzz me if no reply in X min" auto-escalation |

**Platform reality (must design around):**
- **Android**: achievable. Play audio on `AudioAttributes.USAGE_ALARM` (ignores ringer mode), notification channel with DND bypass (requires user granting *Notification Policy Access*), full-screen intent (Android 14+: `USE_FULL_SCREEN_INTENT` is restricted; Buzzit qualifies under "alarm-like" justification only for Emergency — prepare Play Console declaration; fallback is heads-up notification + alarm-stream sound). Delivered via FCM high-priority data messages.
- **iOS**: bypassing the silent switch is only possible with Apple's **Critical Alerts entitlement** (manual approval, intended for health/safety/security). Strategy:
  1. Apply for Critical Alerts positioned around the Safety/Family/Emergency use case (with consent + rate limits as evidence).
  2. Until/if approved: use **Time Sensitive** interruption level (breaks Focus, not silent switch) + strong custom haptics + repeated notifications + **Live Activity / Dynamic Island** alert + optional Apple Watch haptic (Watch taps the wrist even when phone is silent — a strong fallback).
  3. Be transparent in-app about what each OS allows.

### 1.3 Buzzit Platform (free API)
- **Who uses it**: apps wanting to message/buzz their users (delivery apps, alarms, home security, games, support desks), bots, integrations.
- **Products**:
  - **Bot API** — bot accounts users can chat with (REST + webhooks + WebSocket)
  - **Notify API** — app sends message/buzz to a user who *opted in* to that app
  - **Login with Buzzit** — OAuth 2.1 / OIDC identity
  - **Embed SDK** — chat widget for web/mobile
- **Free tier** (generous, abuse-limited): e.g. 10k messages/day, 500 buzzes/day (Nudge/Buzz only; Emergency never available to third parties), 100 req/s burst. Paid "Pro" tiers later for higher limits/SLA — this funds the free tier.
- **Rules**: third parties never get message content of user↔user chats; users grant scopes per app; every app is reviewable/revocable in Settings → Connected Apps; verified-developer badge.
- Developer portal: app registration, API keys, OAuth clients, webhook config + signing secrets, logs, usage dashboard, sandbox environment, OpenAPI docs, SDKs (JS/TS, Python, Go, Kotlin, Swift).

### 1.4 Monetization (keeps API free)
Buzzit Plus subscription (custom buzz sounds, larger uploads, more devices, themes), Platform Pro tiers, business verified accounts. No ads in chats.

---

## 2. Architecture

### 2.1 Tech stack (recommended)
| Layer | Choice | Why |
|---|---|---|
| Mobile | **Native: Kotlin (Jetpack Compose) + Swift (SwiftUI)** sharing logic via **Kotlin Multiplatform** (networking, crypto wrappers, DB, sync) | Buzz needs deep OS integration (alarm streams, Critical Alerts, notification extensions, CallKit) — native is the safe choice for production |
| Web/Desktop | React + TypeScript (Vite), desktop via Tauri | |
| Real-time gateway | **Elixir/Phoenix** (WebSocket, presence) | Millions of concurrent connections, proven for chat |
| Core services | **Go** microservices (gRPC internally) | Performance, simple ops |
| Message store | **ScyllaDB** (Cassandra-compatible) partitioned by `conversation_id` + time bucket | Write-heavy, linear scale |
| Relational | **PostgreSQL** (users, social graph, apps, billing) via Citus when sharding needed | |
| Cache / rate limit / presence | **Redis Cluster** | |
| Event bus | **Kafka** (or Redpanda) | Fan-out, delivery, analytics, webhooks |
| Media | S3-compatible object storage + CDN (CloudFront/Cloudflare), client-side encrypted | |
| Search | On-device SQLite FTS5 for E2EE; OpenSearch for public channels/directory | |
| Calls | WebRTC with **LiveKit** SFU + TURN (coturn) | |
| Push | FCM, APNs (incl. VoIP/PushKit for calls), Web Push | |
| Infra | Kubernetes (EKS/GKE), Terraform, ArgoCD, multi-region active-active | |
| Observability | OpenTelemetry → Prometheus/Grafana, Loki, Tempo; Sentry for clients | |

### 2.2 Services
```
clients ──► API Gateway (Envoy) ──► auth-svc        (signup, OTP, sessions, OAuth/OIDC provider)
        │                     ├──► user-svc        (profiles, contacts, privacy settings)
        │                     ├──► graph-svc       (friends, blocks, buzz passes)
        │                     ├──► conversation-svc(groups, membership, roles)
        │                     ├──► media-svc       (upload URLs, transcoding, thumbnails)
        │                     ├──► keys-svc        (E2EE prekey bundles, device lists)
        │                     └──► platform-svc    (developer apps, API keys, scopes, quotas)
        └──► Realtime Gateway (Phoenix WS) ◄──► message-svc (accept, persist ciphertext, sequence)
                                               │
                                     Kafka ────┼──► delivery-svc (online fan-out / offline push)
                                               ├──► buzz-svc     (consent check, rate limit, escalation, ack tracking)
                                               ├──► webhook-svc  (signed deliveries to third-party apps, retries/DLQ)
                                               ├──► trust-safety-svc (spam/abuse scoring, reports)
                                               └──► analytics (privacy-preserving, aggregated)
call-svc + LiveKit SFU / TURN
```

### 2.3 Security & privacy
- **End-to-end encryption by default** for 1:1 and groups: Signal Protocol (X3DH/PQXDH + Double Ratchet) via `libsignal`; groups via **MLS (RFC 9420)** for large groups. Server stores only ciphertext + minimal metadata.
- Sealed sender to minimize metadata; safety-number verification; encrypted backups (user passphrase / hardware-backed key).
- Bot/API conversations are explicitly labeled **not E2EE with Buzzit** (encrypted in transit + at rest), shown clearly in UI.
- Auth: phone OTP or email + passkeys (WebAuthn), 2FA PIN, device attestation (Play Integrity / App Attest) to fight bot farms.
- Compliance: GDPR/CCPA (data export, delete), age gating (13+/16+ per region), DSA/Online Safety Act reporting flows, App Store/Play policies, SOC 2 for the Platform.
- Regular external pentests, bug bounty, threat model per service.

### 2.4 Key data models (Postgres/Scylla)
- `users(id, username, phone_hash, display_name, avatar, created_at)`
- `devices(id, user_id, platform, push_token, identity_key, last_seen)`
- `friendships(user_a, user_b, state, created_at)`
- `buzz_passes(grantor_id, grantee_id, max_level, quiet_hours, expires_at)`
- `buzz_events(id, sender, recipient, level, status[sent|delivered|rang|acked|expired], created_at)`
- `conversations(id, type, settings)`, `members(conv_id, user_id, role)`
- `messages` (Scylla): `(conv_id, bucket, seq) → sender_device, ciphertext, type, ts`
- `apps(id, owner_id, name, verified, tier)`, `app_credentials`, `app_user_grants(app_id, user_id, scopes)`, `webhooks`, `usage_counters`

### 2.5 Buzz delivery flow
1. Sender taps Buzz → `POST /v1/buzz {recipient, level, note?}`
2. buzz-svc: check friendship, block, Buzz Pass level, quiet hours, rate limits (Redis token bucket), trust score.
3. Emit to Kafka → delivery-svc sends: Android FCM high-priority data msg; iOS APNs with `interruption-level: critical` (if entitled) or `time-sensitive` + Notification Service Extension; Watch; also via WS if app is foregrounded.
4. Client plays alarm-stream sound/vibration, shows full-screen UI, reports `rang` → `acked`.
5. No ack within N sec → re-alert (Emergency only, up to 3×), then notify sender "not acknowledged" + suggest call.

---

## 3. Public API (v1 sketch)
- Base: `https://api.buzzit.app/v1`, auth via OAuth 2.1 bearer tokens (user-context) or bot tokens.
- `POST /messages` — send to chat/user the app is authorized for
- `POST /buzz` — Nudge/Buzz to opted-in user (scope `buzz:send`)
- `GET /chats/{id}/messages`, `GET /me`, `POST /bots/{id}/commands`
- Webhooks: `message.created`, `buzz.acknowledged`, `user.authorized`, `user.revoked` — HMAC-SHA256 signed, retries with exponential backoff, idempotency keys.
- Realtime: WebSocket `wss://rt.buzzit.app/v1/bot`
- Versioning (`/v1`, deprecation policy 12 months), OpenAPI 3.1 spec as source of truth → generated SDKs and docs.
- Quotas enforced at gateway + per-app; abuse → automatic suspension with appeal.

---

## 4. Repository layout (monorepo)
```
buzzit/
  apps/
    android/          Kotlin + Compose
    ios/              Swift + SwiftUI (+ NotificationServiceExtension, WatchApp, Widgets)
    web/              React + TS
    desktop/          Tauri shell around web
    developer-portal/ Next.js
  shared/kmp/         Kotlin Multiplatform: sync engine, models, API client, crypto bindings
  services/
    gateway-rt/       Elixir/Phoenix
    auth/ user/ graph/ conversation/ message/ delivery/ buzz/ media/ keys/ platform/ webhook/ trust-safety/ call/   (Go)
  api/                OpenAPI + protobuf definitions
  sdks/               js, python, go, kotlin, swift (generated + hand-written helpers)
  infra/              terraform/, k8s/ (helm charts), argocd/
  docs/               ADRs, threat models, runbooks
  .github/workflows/  CI/CD
```

---

## 5. Delivery roadmap (production path)
| Phase | Duration | Scope |
|---|---|---|
| 0. Foundations | 4 wks | Monorepo, CI/CD, infra (dev/staging/prod), auth, design system, ADRs, threat model; **submit iOS Critical Alerts request early** |
| 1. Messaging core | 10 wks | E2EE 1:1 + groups, media, receipts, push, multi-device sync, offline queue |
| 2. Buzz | 6 wks | Buzz Passes, 3 levels, Android alarm path, iOS time-sensitive/critical path, Watch, ack flow, abuse limits |
| 3. Calls | 6 wks | 1:1 + group voice/video (LiveKit), CallKit/ConnectionService |
| 4. Platform | 8 wks | Developer portal, OAuth/OIDC, Bot + Notify APIs, webhooks, SDKs, quotas, docs |
| 5. Hardening | 6 wks | Pentest, load tests (target 1M concurrent WS, p99 send→deliver < 300 ms), chaos tests, accessibility (WCAG 2.2 AA), localization (EN, FR, AR w/ RTL, ES), store review |
| 6. Launch | — | Staged rollout (closed beta → 10% → GA), status page, support tooling |
| Post-GA | | Stories, channels, Plus subscription, Platform Pro, Family mode |

Team (suggested): 2 iOS, 2 Android, 1 KMP, 2 web, 5 backend, 1 crypto/security, 2 SRE, 1 QA automation, 1 designer, 1 PM, 1 trust & safety, 1 DevRel.

---

## 6. Quality & operations
- Tests: unit + contract tests (from OpenAPI/protobuf), integration tests with Testcontainers (Postgres/Scylla/Kafka/Redis), E2E mobile (Maestro), web (Playwright), crypto test vectors, load (k6), push-on-silent device lab (physical Android/iOS matrix).
- SLOs: 99.95% message send availability; buzz delivery p95 < 3 s on-device.
- Feature flags (Unleash), remote config, crash-free sessions > 99.8%.
- Runbooks + on-call; incident postmortems.

---

## 7. Verification (how we'll know it works)
1. `docker compose up` local stack → run integration suite; send E2EE message between two emulators, verify server only holds ciphertext.
2. Buzz device lab: Android phone in silent + DND → Buzz/Emergency plays on alarm stream and shows full-screen; iOS silent → Time Sensitive (and Critical if entitled) behaves as designed; Watch taps.
3. Rate-limit/consent tests: buzz without pass is rejected (403), spam hits 429.
4. Platform: register sandbox app, OAuth consent, send Notify message + buzz via SDK, receive signed webhook, verify signature.
5. k6 load test against staging meets latency SLOs.

---

## 8. Immediate next step after approval
Scaffold the monorepo (Phase 0): directory structure, OpenAPI v1 spec draft, auth + buzz-svc skeletons in Go, Phoenix gateway skeleton, Android/iOS app shells with the Buzz alert prototype (the highest technical risk), docker-compose for local dev, and CI workflows. Also produce a polished product/architecture document (shareable page) from this plan if desired.
