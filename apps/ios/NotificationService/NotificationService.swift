import UserNotifications

/// Notification Service Extension: upgrades an incoming Buzz push to the
/// strongest interruption level this install is allowed to use.
///
/// iOS rules:
///  - `.critical` plays sound even with the silent switch on and in Focus/DND.
///    Requires Apple's Critical Alerts entitlement
///    (com.apple.developer.usernotifications.critical-alerts) AND user consent.
///  - `.timeSensitive` breaks through Focus but NOT the silent switch.
///
/// APNs payload sent by delivery-svc:
/// { "aps": { "alert": {...}, "mutable-content": 1, "interruption-level": "time-sensitive",
///            "sound": "buzz.caf" },
///   "buzz": { "id": "bz_...", "level": "emergency", "sender": "Bob", "note": "..." } }
final class NotificationService: UNNotificationServiceExtension {

    override func didReceive(_ request: UNNotificationRequest,
                             withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        guard let content = request.content.mutableCopy() as? UNMutableNotificationContent,
              let buzz = request.content.userInfo["buzz"] as? [String: Any],
              let id = buzz["id"] as? String else {
            contentHandler(request.content)
            return
        }
        let level = buzz["level"] as? String ?? "nudge"

        UNUserNotificationCenter.current().getNotificationSettings { settings in
            let criticalAllowed = settings.criticalAlertSetting == .enabled

            switch level {
            case "emergency" where criticalAllowed:
                content.interruptionLevel = .critical
                content.sound = .criticalSoundNamed(UNNotificationSoundName("buzz-emergency.caf"), withAudioVolume: 1.0)
            case "emergency", "buzz":
                content.interruptionLevel = .timeSensitive
                content.sound = UNNotificationSound(named: UNNotificationSoundName("buzz.caf"))
            default:
                content.interruptionLevel = .active
                content.sound = .default
            }
            content.categoryIdentifier = "BUZZ"
            content.threadIdentifier = "buzz"

            BuzzStatusReporter.report(id: id, status: "delivered") {
                contentHandler(content)
            }
        }
    }

    override func serviceExtensionTimeWillExpire() {
        // Deliver whatever we have; the push already carries a time-sensitive default.
    }
}

/// Minimal status reporter usable from the extension (shares the app group
/// keychain for the device auth token).
enum BuzzStatusReporter {
    static func report(id: String, status: String, completion: @escaping () -> Void) {
        guard let token = SharedKeychain.deviceToken(),
              let url = URL(string: "https://api.buzzit.app/v1/buzz/\(id)/status") else {
            completion(); return
        }
        var req = URLRequest(url: url, timeoutInterval: 5)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONSerialization.data(withJSONObject: ["status": status])
        URLSession.shared.dataTask(with: req) { _, _, _ in completion() }.resume()
    }
}
