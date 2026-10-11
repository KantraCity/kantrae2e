package com.wails.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.util.Log;

import androidx.core.content.ContextCompat;

/** Starts the background connection after a reboot or an app update. */
public class KantraBootReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        if (!KantraNative.backgroundWanted(context)) return;
        try {
            ContextCompat.startForegroundService(context, new Intent(context, KantraPushService.class));
        } catch (Exception e) {
            Log.w("Kantra", "start after boot", e);
        }
    }
}
