package com.wails.app;

import android.Manifest;
import android.app.Activity;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.os.PowerManager;
import android.provider.MediaStore;
import android.provider.Settings;
import android.util.Log;

import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;

import org.json.JSONObject;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
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
            // Photo / GIF / video preview, like Telegram (collapsed: a small
            // picture on the right; expanded: the whole picture).
            String image = o.optString("image", "");
            if (!image.isEmpty()) {
                android.graphics.BitmapFactory.Options opt = new android.graphics.BitmapFactory.Options();
                opt.inJustDecodeBounds = true;
                android.graphics.BitmapFactory.decodeFile(image, opt);
                int sample = 1;
                while (Math.max(opt.outWidth, opt.outHeight) / sample > 1024) sample *= 2;
                opt = new android.graphics.BitmapFactory.Options();
                opt.inSampleSize = sample;
                android.graphics.Bitmap bmp = android.graphics.BitmapFactory.decodeFile(image, opt);
                if (bmp != null) {
                    b.setLargeIcon(bmp).setStyle(new NotificationCompat.BigPictureStyle()
                            .bigPicture(bmp).bigLargeIcon((android.graphics.Bitmap) null)
                            .setSummaryText(o.optString("body", "")));
                }
            }
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

    /**
     * Saves a decrypted file to the phone's gallery / downloads, like
     * Telegram's "Сохранить в галерею": {"path","name","mime"} → "" when
     * saved, else an error text. Photos go to Pictures/Kantra, videos to
     * Movies/Kantra, everything else to Download/Kantra.
     */
    public static String saveMedia(String json) {
        File src = null;
        try {
            JSONObject o = new JSONObject(json);
            src = new File(o.getString("path"));
            String name = o.optString("name", src.getName());
            String mime = o.optString("mime", "application/octet-stream");
            boolean image = mime.startsWith("image/"), video = mime.startsWith("video/");
            String dir = image ? Environment.DIRECTORY_PICTURES : video ? Environment.DIRECTORY_MOVIES : Environment.DIRECTORY_DOWNLOADS;
            if (Build.VERSION.SDK_INT >= 29) {
                ContentResolver cr = app.getContentResolver();
                Uri collection = image ? MediaStore.Images.Media.EXTERNAL_CONTENT_URI
                        : video ? MediaStore.Video.Media.EXTERNAL_CONTENT_URI
                        : MediaStore.Downloads.EXTERNAL_CONTENT_URI;
                ContentValues v = new ContentValues();
                v.put(MediaStore.MediaColumns.DISPLAY_NAME, name);
                v.put(MediaStore.MediaColumns.MIME_TYPE, mime);
                v.put(MediaStore.MediaColumns.RELATIVE_PATH, dir + "/Kantra");
                v.put(MediaStore.MediaColumns.IS_PENDING, 1);
                Uri uri = cr.insert(collection, v);
                if (uri == null) return "Не удалось сохранить файл";
                try (InputStream in = new FileInputStream(src); OutputStream out = cr.openOutputStream(uri)) {
                    copy(in, out);
                }
                v.clear();
                v.put(MediaStore.MediaColumns.IS_PENDING, 0);
                cr.update(uri, v, null, null);
                return "";
            }
            if (ContextCompat.checkSelfPermission(app, Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
                Activity a = activity.get();
                if (a != null) a.runOnUiThread(() -> a.requestPermissions(new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, 4203));
                return "Разрешите доступ к памяти и сохраните ещё раз";
            }
            File outDir = new File(Environment.getExternalStoragePublicDirectory(dir), "Kantra");
            if (!outDir.isDirectory() && !outDir.mkdirs()) return "Не удалось создать папку";
            File dst = new File(outDir, name);
            for (int i = 1; dst.exists(); i++) {
                int dot = name.lastIndexOf('.');
                dst = new File(outDir, dot > 0 ? name.substring(0, dot) + " (" + i + ")" + name.substring(dot) : name + " (" + i + ")");
            }
            try (InputStream in = new FileInputStream(src); OutputStream out = new FileOutputStream(dst)) {
                copy(in, out);
            }
            android.media.MediaScannerConnection.scanFile(app, new String[]{dst.getAbsolutePath()}, new String[]{mime}, null);
            return "";
        } catch (Exception e) {
            Log.w(TAG, "save media", e);
            return "Не удалось сохранить: " + e.getMessage();
        } finally {
            if (src != null) //noinspection ResultOfMethodCallIgnored
                src.delete();
        }
    }

    private static void copy(InputStream in, OutputStream out) throws java.io.IOException {
        byte[] buf = new byte[64 * 1024];
        for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
    }

    /**
     * Installs a downloaded, signature-checked update (update.go) with the
     * system installer. Returns "" when the install started, else what to
     * do. Android asks the user to confirm; from Android 12 on, once Kantra
     * itself installed the running version, it updates without asking.
     */
    public static String installUpdate(String path) {
        try {
            if (Build.VERSION.SDK_INT >= 26 && !app.getPackageManager().canRequestPackageInstalls()) {
                Intent i = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + app.getPackageName()));
                i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                app.startActivity(i);
                return "Разрешите Kantra устанавливать приложения и нажмите «Установить» ещё раз";
            }
            android.content.pm.PackageInstaller pi = app.getPackageManager().getPackageInstaller();
            android.content.pm.PackageInstaller.SessionParams params =
                    new android.content.pm.PackageInstaller.SessionParams(android.content.pm.PackageInstaller.SessionParams.MODE_FULL_INSTALL);
            params.setAppPackageName(app.getPackageName());
            if (Build.VERSION.SDK_INT >= 31) {
                params.setRequireUserAction(android.content.pm.PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED);
            }
            int id = pi.createSession(params);
            try (android.content.pm.PackageInstaller.Session session = pi.openSession(id)) {
                File apk = new File(path);
                try (InputStream in = new FileInputStream(apk);
                     OutputStream out = session.openWrite("kantra.apk", 0, apk.length())) {
                    copy(in, out);
                    session.fsync(out);
                }
                Intent done = new Intent(app, KantraUpdateReceiver.class);
                int flags = PendingIntent.FLAG_UPDATE_CURRENT | (Build.VERSION.SDK_INT >= 31 ? PendingIntent.FLAG_MUTABLE : 0);
                PendingIntent pending = PendingIntent.getBroadcast(app, id, done, flags);
                session.commit(pending.getIntentSender());
            }
            return "";
        } catch (Exception e) {
            Log.w(TAG, "install update", e);
            return "Не удалось установить обновление: " + e.getMessage();
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
