package com.wails.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

import androidx.annotation.Nullable;
import androidx.core.app.NotificationCompat;

/**
 * Keeps the app's process alive so the Go client can hold its single
 * WebSocket to the Kantra server while the app is in the background: this is
 * the "push" channel, without Google's FCM.
 *
 * It does no work itself — no timers, no wake locks. The CPU sleeps; the
 * device wakes only when a packet arrives (a message, or the server's
 * keep-alive every few minutes). Restarted by the system if killed
 * (START_STICKY) and after boot ({@link KantraBootReceiver}).
 */
public class KantraPushService extends Service {
    private static final String CHANNEL = "kantra_connection";
    private static final int ID = 0x4B41; // "KA"

    @Override
    public void onCreate() {
        super.onCreate();
        KantraNative.attach(this);
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (!KantraNative.backgroundWanted(this)) {
            stopSelf();
            return START_NOT_STICKY;
        }
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            // Lowest importance: no sound, no status-bar icon, collapsed.
            NotificationChannel ch = new NotificationChannel(CHANNEL, "Фоновое соединение",
                    NotificationManager.IMPORTANCE_MIN);
            ch.setDescription("Kantra остаётся на связи, чтобы сразу показывать новые сообщения");
            ch.setShowBadge(false);
            nm.createNotificationChannel(ch);
        }
        Intent launch = new Intent(this, MainActivity.class);
        PendingIntent pi = PendingIntent.getActivity(this, 0, launch, PendingIntent.FLAG_IMMUTABLE);
        Notification n = new NotificationCompat.Builder(this, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_kantra)
                .setContentTitle("Kantra на связи")
                .setContentText("Ожидание новых сообщений")
                .setOngoing(true)
                .setPriority(NotificationCompat.PRIORITY_MIN)
                .setCategory(NotificationCompat.CATEGORY_SERVICE)
                .setContentIntent(pi)
                .build();
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(ID, n);
        }
        // Start the Go client if the process was (re)started without the UI.
        KantraNative.startBackgroundClient();
        return START_STICKY;
    }

    @Nullable
    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
