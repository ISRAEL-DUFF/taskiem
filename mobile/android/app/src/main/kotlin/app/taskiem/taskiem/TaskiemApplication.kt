package app.taskiem.taskiem

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions

// Notifications on this phone (lib/app/notify.dart; docs/spec/mobile-app-m4.md
// N4). The app has no Firebase file: Firebase is started from the values the
// server gives (GET /v1/app/config), which the app keeps here. When the phone
// starts the app for a notification, before any of it runs, Firebase is
// started from what was kept, so the notification is shown and not lost.
class TaskiemApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        startFirebase(this)
        makeChannels(this)
    }

    companion object {
        private const val PREFS = "taskiem_notify"

        /** The channels, one per kind, named so the phone's settings can quiet one. */
        val CHANNELS = listOf(
            "business" to "Business chats",
            "customers" to "Customer chats",
            "orders" to "Orders and money",
            "approvals" to "Approvals",
            "alerts" to "Alerts",
        )

        fun makeChannels(context: Context) {
            val manager = context.getSystemService(NotificationManager::class.java) ?: return
            for ((id, name) in CHANNELS) {
                val importance = if (id == "alerts") NotificationManager.IMPORTANCE_DEFAULT else NotificationManager.IMPORTANCE_HIGH
                manager.createNotificationChannel(NotificationChannel(id, name, importance).apply { setShowBadge(true) })
            }
        }

        /** Keeps the server's Firebase values (null forgets them). */
        fun keep(context: Context, values: Map<String, String>?) {
            val edit = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            if (values == null) {
                edit.clear()
            } else {
                for (k in listOf("api_key", "app_id", "project_id", "sender_id")) edit.putString(k, values[k])
            }
            edit.apply()
        }

        fun startFirebase(context: Context) {
            try {
                if (FirebaseApp.getApps(context).isNotEmpty()) return
                val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                val apiKey = p.getString("api_key", null) ?: return
                val appId = p.getString("app_id", null) ?: return
                val options = FirebaseOptions.Builder()
                    .setApiKey(apiKey)
                    .setApplicationId(appId)
                    .setProjectId(p.getString("project_id", null))
                    .setGcmSenderId(p.getString("sender_id", null))
                    .build()
                FirebaseApp.initializeApp(context, options)
            } catch (e: Exception) {
                // Not started: the app starts it itself when opened.
            }
        }
    }
}
