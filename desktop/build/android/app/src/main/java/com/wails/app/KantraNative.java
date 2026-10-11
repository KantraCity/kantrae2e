package com.wails.app;

import android.Manifest;
import android.app.Activity;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.Uri;
import android.os.Build;
import android.os.PowerManager;
import android.provider.Settings;
import android.util.Log;

import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;

import org.json.JSONObject;

import java.lang.ref.WeakReference;

/**
 * Kantra's own bridge between Java and the Go client, independent of the
 * Activity: the Go side keeps the realtime connection open while the app is
 * in the background (kept alive by {@link KantraPushService}) and posts
 * message notifications through here. No Google services (FCM) involved.
 *
 * Go calls the public static String-argument methods below via JNI.
 */
public final class KantraNative {
    private static final String TAG = "Kantra";
    static final String CHANNEL_MESSAGES = "kantra_messages";
    static final String EXTRA_CHAT = "kantra_chat";
    private static final String PREFS = "kantra";
    private static final String PREF_BACKGROUND = "background";

    static {
        System.loadLibrary("wails");
    }

    private static Context app;
    private static WeakReference<Activity> activity = new WeakReference<>(null);

    private KantraNative() {}

    // Implemented in Go (desktop/jni_android.go).
    private static native void nativeAttach(String filesDir);
    private static native void nativeStartBackground();
    private static native void nativeOpenChat(String chatId);
    private static native void nativeSetForeground(boolean foreground);
    private static native void nativeNetworkAvailable();

    /** Called by the activity and the service before anything else. */
    static synchronized void attach(Context ctx) {
        if (app != null) return;
        app = ctx.getApplicationContext();
        nativeAttach(app.getFilesDir().getAbsolutePath());
        createChannel();
        // Reconnect immediately when a network becomes available instead of
        // waiting for the backoff timer.
        try {
            ConnectivityManager cm = (ConnectivityManager) app.getSystemService(Context.CONNECTIVITY_SERVICE);
            cm.registerDefaultNetworkCallback(new ConnectivityManager.NetworkCallback() {
                @Override public void onAvailable(Network network) { nativeNetworkAvailable(); }
            });
        } catch (Exception e) {
            Log.w(TAG, "network callback", e);
        }
    }

    static void setActivity(Activity a) { activity = new WeakReference<>(a); }

    static void startBackgroundClient() { nativeStartBackground(); }

    static void openChat(String chatId) {
        if (chatId != null && !chatId.isEmpty()) nativeOpenChat(chatId);
    }

    static void setForeground(boolean fg) { nativeSetForeground(fg); }

    static boolean backgroundWanted(Context ctx) {
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean(PREF_BACKGROUND, true);
    }

    private static void createChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return;
        NotificationChannel ch = new NotificationChannel(CHANNEL_MESSAGES, "Сообщения",
                NotificationManager.IMPORTANCE_HIGH);
        ch.setDescription("Новые сообщения в чатах");
        ch.enableVibration(true);
        app.getSystemService(NotificationManager.class).createNotificationChannel(ch);
    }

    private static int notificationId(String chatId) { return chatId.hashCode() & 0x7fffffff; }

    // ---- called from Go ----------------------------------------------------

    /** json: {"chatId","title","body","count"}: one notification per chat. */
    public static void notify(String json) {
        try {
            JSONObject o = new JSONObject(json);
            String chatId = o.getString("chatId");
            int count = o.optInt("count", 1);
            Intent open = new Intent(app, MainActivity.class)
                    .setAction(Intent.ACTION_VIEW)
                    .setData(Uri.parse("kantra://chat/" + chatId))
                    .putExtra(EXTRA_CHAT, chatId)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
            PendingIntent pi = PendingIntent.getActivity(app, notificationId(chatId), open,
                    PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            NotificationCompat.Builder b = new NotificationCompat.Builder(app, CHANNEL_MESSAGES)
                    .setSmallIcon(R.drawable.ic_stat_kantra)
                    .setColor(0xFF5288C1)
                    .setContentTitle(o.optString("title", "Kantra"))
                    .setContentText(o.optString("body", ""))
                    .setStyle(new NotificationCompat.BigTextStyle().bigText(o.optString("body", "")))
                    .setCategory(NotificationCompat.CATEGORY_MESSAGE)
                    .setPriority(NotificationCompat.PRIORITY_HIGH)
                    .setAutoCancel(true)
                    .setContentIntent(pi);
            if (count > 1) b.setNumber(count).setSubText(count + " новых");
            if (NotificationManagerCompat.from(app).areNotificationsEnabled()) {
                NotificationManagerCompat.from(app).notify(notificationId(chatId), b.build());
            }
        } catch (SecurityException e) {
            Log.w(TAG, "notification permission missing");
        } catch (Exception e) {
            Log.e(TAG, "notify", e);
        }
    }

    /** Removes the notification of a chat (it was opened). */
    public static void cancel(String chatId) {
        NotificationManagerCompat.from(app).cancel(notificationId(chatId));
    }

    /** "1": keep the connection in the background (foreground service), "0": stop. */
    public static void setBackground(String on) {
        boolean enable = "1".equals(on);
        app.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(PREF_BACKGROUND, enable).apply();
        Intent i = new Intent(app, KantraPushService.class);
        try {
            if (enable) ContextCompat.startForegroundService(app, i);
            else app.stopService(i);
        } catch (Exception e) {
            // Starting a foreground service from the background is restricted
            // (Android 12+); it is started again when the app is opened.
            Log.w(TAG, "background service", e);
        }
    }

    /** {"notificationsAllowed","batteryUnrestricted","background"} */
    public static String status(String ignored) {
        try {
            PowerManager pm = (PowerManager) app.getSystemService(Context.POWER_SERVICE);
            JSONObject o = new JSONObject();
            o.put("notificationsAllowed", NotificationManagerCompat.from(app).areNotificationsEnabled());
            o.put("batteryUnrestricted", pm.isIgnoringBatteryOptimizations(app.getPackageName()));
            o.put("background", backgroundWanted(app));
            return o.toString();
        } catch (Exception e) {
            return "{}";
        }
    }

    /** "battery": ask to lift battery optimisation; "notifications": permission / settings. */
    public static void openSettings(String what) {
        Activity a = activity.get();
        try {
            if ("notifications".equals(what)) {
                if (Build.VERSION.SDK_INT >= 33 && a != null && ContextCompat.checkSelfPermission(app,
                        Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                    a.runOnUiThread(() -> a.requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 4201));
                    return;
                }
                Intent i = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                        .putExtra(Settings.EXTRA_APP_PACKAGE, app.getPackageName());
                start(a, i);
            } else if ("battery".equals(what)) {
                // Without this, Doze cuts the network of background apps for
                // hours; with it the idle connection costs a radio wake-up per
                // keep-alive (every few minutes) and nothing else.
                Intent i = new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS)
                        .setData(Uri.parse("package:" + app.getPackageName()));
                start(a, i);
            }
        } catch (Exception e) {
            Log.e(TAG, "openSettings " + what, e);
        }
    }

    private static void start(Activity a, Intent i) {
        if (a != null) {
            a.runOnUiThread(() -> a.startActivity(i));
        } else {
            app.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
        }
    }
}
