package app.taskiem.taskiem

import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

// A FlutterFragmentActivity: the phone's own unlock (local_auth, the app
// lock) shows its prompt as a fragment.
class MainActivity : FlutterFragmentActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        // lib/app/notify.dart: the server's Firebase values, kept for when the
        // phone starts the app for a notification (TaskiemApplication).
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "taskiem/notify").setMethodCallHandler { call, result ->
            when (call.method) {
                "keep" -> {
                    @Suppress("UNCHECKED_CAST")
                    TaskiemApplication.keep(applicationContext, call.arguments as? Map<String, String>)
                    result.success(null)
                }
                "clear" -> {
                    // Signing out: what this person was shown leaves the tray.
                    (getSystemService(NOTIFICATION_SERVICE) as android.app.NotificationManager).cancelAll()
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        }
        // lib/app/lock.dart: with the app lock on, the app switcher shows no
        // picture of the screens (Android 13 and later), as WhatsApp's lock
        // does, without stopping screenshots or covering the app while it is
        // in front.
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "taskiem/window").setMethodCallHandler { call, result ->
            when (call.method) {
                "recents" -> {
                    val hide = call.arguments as? Boolean ?: false
                    if (android.os.Build.VERSION.SDK_INT >= 33) setRecentsScreenshotEnabled(!hide)
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        }
    }
}
