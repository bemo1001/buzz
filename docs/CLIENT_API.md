# Buzzit client API (first-party apps)

These are the endpoints the Buzzit apps use. Every endpoint except register and login needs `Authorization: Bearer <bzu_ token>`.
The public API for third-party apps is in [`api/openapi.yaml`](../api/openapi.yaml).

Errors always look like `{"error":{"code":"…","message":"…"}}`. Timestamps are Unix milliseconds unless noted.

## Accounts & devices
| Method | Path | Notes |
|---|---|---|
| POST | `/v1/auth/register` | `{username, password, display_name, device_name, platform}` → `{token, user_id, device_id}` |
| POST | `/v1/auth/login` | same body (without display_name). Each login is a new device session |
| POST | `/v1/auth/logout` | revokes this device's token |
| POST | `/v1/auth/password` | `{current_password, new_password}`. Signs out every other device |
| GET / DELETE | `/v1/devices`, `/v1/devices/{id}` | list sessions, or sign one out remotely |
| PUT | `/v1/devices/current/push-token` | `{token}` FCM token (optional) |

## Profile, friends, blocks
| Method | Path | Notes |
|---|---|---|
| GET / PATCH | `/v1/me` | `display_name, bio, avatar_media_id, send_read_receipts, show_last_seen` |
| GET | `/v1/users/search?q=` | by username or name. Hides users who blocked you |
| GET | `/v1/users/{id}` | `{user, relationship}` where relationship is none, friends, request_sent, request_received or blocked |
| GET | `/v1/friends` | includes `can_buzz_level`: the highest level that friend lets *you* send |
| GET / POST | `/v1/friends/requests` | POST `{user_id}` or `{username}`. A reverse pending request auto-accepts |
| POST | `/v1/friends/requests/{user}/accept` | |
| DELETE | `/v1/friends/requests/{user}` | decline (incoming) or cancel (outgoing) |
| DELETE | `/v1/friends/{user}` | also removes Buzz Passes both ways |
| GET / PUT / DELETE | `/v1/blocks`, `/v1/blocks/{user}` | blocking also removes friendship, requests and passes. It is never revealed to the blocked person |

## Buzz
| Method | Path | Notes |
|---|---|---|
| GET | `/v1/buzz-passes` | passes **I granted** (who may buzz me, and how hard) |
| PUT | `/v1/buzz-passes/{friend}` | `{max_level: nudge\|buzz\|emergency, quiet_start_min?, quiet_end_min?, tz, expires_at?}` |
| DELETE | `/v1/buzz-passes/{friend}` | back to the default (nudge) |
| POST | `/v1/buzz` | `{recipient, level, note?}` → 202, 403 `level_not_granted`/`not_allowed`, 409 `quiet_hours`, 429 |
| GET | `/v1/buzz/{id}` | sender or recipient only |
| POST | `/v1/buzz/{id}/status` | recipient's device reports `delivered`, `rang` or `acked` (never goes backwards) |

Default rate limits per sender/recipient pair: nudge 10/10 min, buzz 3/10 min, emergency 3/day, plus 60 buzzes/hour per sender.

## Conversations & messages
| Method | Path | Notes |
|---|---|---|
| GET | `/v1/conversations` | each has members, last_message, unread_count, muted_until, my_role |
| POST | `/v1/conversations` | `{type:"direct", user_id}` (friends or bots; idempotent) or `{type:"group", title, member_ids}` |
| GET / PATCH | `/v1/conversations/{id}` | PATCH `{title?, avatar_media_id?}` (group admins) |
| PUT | `/v1/conversations/{id}/settings` | `{muted_until}` (-1 = forever, 0 = unmute) |
| POST | `/v1/conversations/{id}/members` | `{user_ids}` (admins; must be friends of the adder) |
| DELETE | `/v1/conversations/{id}/members/{user\|me}` | remove someone (admin) or leave |
| PATCH | `/v1/conversations/{id}/members/{user}` | `{role: admin\|member\|owner}` (owner only; `owner` transfers ownership) |
| GET | `/v1/conversations/{id}/messages?before_seq=&after_seq=&limit=` | newest first |
| POST | `/v1/conversations/{id}/messages` | `{client_id, kind, text?, media_id?, reply_to?}`. Idempotent per client_id |
| PATCH / DELETE | `/v1/conversations/{id}/messages/{mid}` | edit (sender, 15 min) / delete for everyone (sender or admin) |
| PUT / DELETE | `/v1/conversations/{id}/messages/{mid}/reaction` | `{emoji}`. One reaction per person |
| POST | `/v1/conversations/{id}/read` | `{seq}`. Read receipts go to others only if `send_read_receipts` is on |
| POST / GET | `/v1/media`, `/v1/media/{id}` | multipart `file` (default max 50 MB). Readable by the owner and by members of chats where the file was shared |

## Realtime
- `GET /v1/ws` (WebSocket). The server sends `{seq?, type, data}`. Events with a `seq` are stored and can be replayed. `typing`, `presence`, `hello` and `pong` are live-only.
- Client → server: `{"type":"typing","conversation_id":…}` and `{"type":"ping"}`.
- `GET /v1/sync?since=<seq>` replays stored events (30-day retention; `reset:true` means refetch state).
- Event types: `message.new`, `message.updated`, `conversation.updated`, `conversation.removed`, `read`, `buzz`, `buzz.status`, `buzz_pass.changed`, `friend.request`, `friend.added`, `friend.removed`.
- Clients apply events strictly in `seq` order. If they see a gap, they call `/v1/sync`.
