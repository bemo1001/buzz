# ADR 0001 — How Buzz reaches a phone on silent

**Status:** accepted · **Date:** 2026-11-22

## Context
Buzz must alert a user even when their phone is silenced. Each OS limits this differently, and an alert that can override silence is easy to abuse.

## Decision
1. **Consent first.** A user can only be buzzed above `nudge` by someone they granted a Buzz Pass to. Emergency needs its own grant. Third-party apps can never send Emergency. This rule lives in the server (`services/internal/buzz`), not only in the app.
2. **Android:** the buzz arrives over the app's own WebSocket (kept open by a foreground service) or, if configured, an FCM high-priority *data* message. A foreground service plays on `USAGE_ALARM` (the alarm stream ignores ringer mode) and shows a full-screen intent. The Buzz and Emergency channels use `setBypassDnd(true)` (needs Notification Policy Access). The same buzz is de-duplicated by ID across socket and push, and answering on one device stops it ringing on the others. See `apps/android/app/src/main/java/app/buzzit/buzz/`.
3. **iOS:** apply for the Critical Alerts entitlement under the Safety/Family use case. Until it is granted: Time Sensitive interruption level, custom sound and haptics, an Apple Watch tap, and repeated alerts for Emergency. The Notification Service Extension picks the strongest level allowed at runtime. See `apps/ios/NotificationService/NotificationService.swift`.
4. **Honest UX.** The app shows each user what their device will actually do (`BuzzPermissions`, `BuzzAuthorization.Capability.explanation`).

## Consequences
- Native mobile code is needed (no cross-platform-only stack).
- Without the Apple entitlement, iOS buzzes cannot beat the hardware silent switch. That limit is shown in the app rather than hidden.
- The Play Console needs a full-screen-intent declaration.
