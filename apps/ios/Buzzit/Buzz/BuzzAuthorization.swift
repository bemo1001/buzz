import UserNotifications

/// Requests notification permissions for Buzz. `.criticalAlert` is only
/// honoured once Apple grants the Critical Alerts entitlement; until then the
/// system ignores it and buzzes fall back to Time Sensitive.
enum BuzzAuthorization {

    struct Capability {
        let notifications: Bool
        let timeSensitive: Bool
        let critical: Bool

        /// Copy shown to users so expectations match what iOS allows.
        var explanation: String {
            if critical { return "Emergency buzzes will ring even when your phone is on silent." }
            if timeSensitive { return "Buzzes break through Focus. With the silent switch on, you'll get strong haptics and an Apple Watch tap." }
            return "Turn on notifications so friends can buzz you."
        }
    }

    static func request() async throws -> Capability {
        let center = UNUserNotificationCenter.current()
        _ = try await center.requestAuthorization(options: [.alert, .sound, .badge, .criticalAlert, .timeSensitive])
        let ack = UNNotificationAction(identifier: "BUZZ_ACK", title: "I'm OK", options: [])
        let call = UNNotificationAction(identifier: "BUZZ_CALL", title: "Call back", options: [.foreground])
        let snooze = UNNotificationAction(identifier: "BUZZ_SNOOZE", title: "Snooze 5 min", options: [])
        center.setNotificationCategories([
            UNNotificationCategory(identifier: "BUZZ", actions: [ack, call, snooze], intentIdentifiers: [])
        ])
        return await current()
    }

    static func current() async -> Capability {
        let s = await UNUserNotificationCenter.current().notificationSettings()
        return Capability(
            notifications: s.authorizationStatus == .authorized,
            timeSensitive: s.timeSensitiveSetting == .enabled,
            critical: s.criticalAlertSetting == .enabled
        )
    }
}
