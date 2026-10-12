package io.github.xattribution.burrow.work

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import io.github.xattribution.burrow.R
import io.github.xattribution.burrow.ui.MainActivity

class Notifications(private val context: Context) {
    private val nm = context.getSystemService(NotificationManager::class.java)

    fun createChannels() {
        nm.createNotificationChannels(
            listOf(
                NotificationChannel(SYNC, "Sync and backup", NotificationManager.IMPORTANCE_LOW).apply { description = "Progress while folders sync with Burrow" },
                NotificationChannel(TRANSFERS, "Uploads and downloads", NotificationManager.IMPORTANCE_LOW).apply { description = "Files you send to or save from Burrow" },
                NotificationChannel(ALERTS, "Problems", NotificationManager.IMPORTANCE_DEFAULT).apply { description = "When something needs you, like a phone that was removed from Burrow" },
            ),
        )
    }

    private fun open() = PendingIntent.getActivity(context, 0, Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP), PendingIntent.FLAG_IMMUTABLE)

    fun progress(channel: String, title: String, text: String, done: Long, total: Long) =
        NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setOnlyAlertOnce(true)
            .setOngoing(true)
            .setSilent(true)
            .setContentIntent(open())
            .setProgress(if (total > 0) 1000 else 0, if (total > 0) (done * 1000 / total).toInt() else 0, total <= 0)
            .build()

    fun alert(id: Int, title: String, text: String) {
        if (context.checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != android.content.pm.PackageManager.PERMISSION_GRANTED) return
        nm.notify(
            id,
            NotificationCompat.Builder(context, ALERTS)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(title)
                .setContentText(text)
                .setStyle(NotificationCompat.BigTextStyle().bigText(text))
                .setAutoCancel(true)
                .setContentIntent(open())
                .build(),
        )
    }

    companion object {
        const val SYNC = "sync"
        const val TRANSFERS = "transfers"
        const val ALERTS = "alerts"
        const val ID_SYNC = 1
        const val ID_TRANSFER = 2
        const val ID_REMOVED = 3
        const val ID_SYNC_PROBLEM = 4
    }
}
