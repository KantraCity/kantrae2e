package com.wails.app;

import android.Manifest;
import android.app.Activity;
import android.content.ContentUris;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.media.ThumbnailUtils;
import android.net.Uri;
import android.os.Build;
import android.provider.MediaStore;
import android.util.Size;
import android.webkit.JavascriptInterface;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;

/**
 * The photo picker of the attach sheet (like Telegram's): recent photos and
 * videos of the phone's gallery for the WebView. JS calls window.KantraGallery
 * (state / request / list); thumbnails and files are served at
 * https://wails.localhost/__gallery/thumb/<id> and /__gallery/file/<id>.
 * Nothing leaves the phone unless the user sends it.
 */
public class KantraGallery {
    static final int PERMISSION_REQUEST = 4310;
    static final String PREFIX = "/__gallery/";
    private final Activity activity;
    private final WebView webView;

    KantraGallery(Activity activity, WebView webView) {
        this.activity = activity;
        this.webView = webView;
    }

    private static String[] permissions() {
        if (Build.VERSION.SDK_INT >= 34) {
            return new String[]{Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO,
                    "android.permission.READ_MEDIA_VISUAL_USER_SELECTED"};
        }
        if (Build.VERSION.SDK_INT >= 33) {
            return new String[]{Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO};
        }
        return new String[]{Manifest.permission.READ_EXTERNAL_STORAGE};
    }

    private boolean granted(String p) {
        return Build.VERSION.SDK_INT < 23 || activity.checkSelfPermission(p) == PackageManager.PERMISSION_GRANTED;
    }

    /** "granted", "partial" (Android 14: only selected photos) or "denied". */
    @JavascriptInterface
    public String state() {
        if (Build.VERSION.SDK_INT >= 33) {
            if (granted(Manifest.permission.READ_MEDIA_IMAGES)) return "granted";
            if (Build.VERSION.SDK_INT >= 34 && granted("android.permission.READ_MEDIA_VISUAL_USER_SELECTED")) return "partial";
            return "denied";
        }
        return granted(Manifest.permission.READ_EXTERNAL_STORAGE) ? "granted" : "denied";
    }

    @JavascriptInterface
    public void request() {
        activity.runOnUiThread(() -> {
            if (Build.VERSION.SDK_INT >= 23) activity.requestPermissions(permissions(), PERMISSION_REQUEST);
        });
    }

    /** Called by MainActivity when the permission dialog is answered. */
    void onPermissionResult() {
        webView.post(() -> webView.evaluateJavascript("window.kantraGalleryChanged && window.kantraGalleryChanged()", null));
    }

    /** Newest photos and videos: [{id, video, mime, w, h, dur, size, name}]. */
    @JavascriptInterface
    public String list(int offset, int limit) {
        JSONArray out = new JSONArray();
        if ("denied".equals(state())) return out.toString();
        Uri uri = MediaStore.Files.getContentUri("external");
        String[] cols = {MediaStore.Files.FileColumns._ID, MediaStore.Files.FileColumns.MEDIA_TYPE,
                MediaStore.MediaColumns.MIME_TYPE, MediaStore.MediaColumns.WIDTH, MediaStore.MediaColumns.HEIGHT,
                MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DISPLAY_NAME, "duration"};
        String sel = MediaStore.Files.FileColumns.MEDIA_TYPE + " IN (" + MediaStore.Files.FileColumns.MEDIA_TYPE_IMAGE + ","
                + MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO + ")";
        String order = MediaStore.MediaColumns.DATE_ADDED + " DESC";
        try (Cursor c = activity.getContentResolver().query(uri, cols, sel, null, order)) {
            if (c == null) return out.toString();
            if (offset > 0 && !c.moveToPosition(offset - 1)) return out.toString();
            int n = 0;
            while (n < limit && c.moveToNext()) {
                JSONObject o = new JSONObject();
                boolean video = c.getInt(1) == MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO;
                o.put("id", c.getLong(0));
                o.put("video", video);
                o.put("mime", c.getString(2));
                o.put("w", c.getInt(3));
                o.put("h", c.getInt(4));
                o.put("size", c.getLong(5));
                o.put("name", c.getString(6));
                o.put("dur", video ? c.getLong(7) / 1000.0 : 0);
                out.put(o);
                n++;
            }
        } catch (Exception e) {
            android.util.Log.w("KantraGallery", "list", e);
        }
        return out.toString();
    }

    private Uri contentUri(long id, boolean video) {
        return ContentUris.withAppendedId(video ? MediaStore.Video.Media.EXTERNAL_CONTENT_URI
                : MediaStore.Images.Media.EXTERNAL_CONTENT_URI, id);
    }

    private boolean isVideo(long id) {
        Uri uri = MediaStore.Files.getContentUri("external");
        try (Cursor c = activity.getContentResolver().query(uri, new String[]{MediaStore.Files.FileColumns.MEDIA_TYPE},
                MediaStore.Files.FileColumns._ID + "=?", new String[]{String.valueOf(id)}, null)) {
            return c != null && c.moveToFirst() && c.getInt(0) == MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO;
        } catch (Exception e) {
            return false;
        }
    }

    /** Serves /__gallery/thumb/<id> and /__gallery/file/<id> (null: not ours). */
    WebResourceResponse serve(String path) {
        if (path == null || !path.startsWith(PREFIX)) return null;
        String[] parts = path.substring(PREFIX.length()).split("/");
        if (parts.length != 2 || "denied".equals(state())) return notFound();
        long id;
        try {
            id = Long.parseLong(parts[1]);
        } catch (NumberFormatException e) {
            return notFound();
        }
        boolean video = isVideo(id);
        Uri uri = contentUri(id, video);
        try {
            if ("thumb".equals(parts[0])) {
                Bitmap bmp;
                if (Build.VERSION.SDK_INT >= 29) {
                    bmp = activity.getContentResolver().loadThumbnail(uri, new Size(320, 320), null);
                } else if (video) {
                    bmp = MediaStore.Video.Thumbnails.getThumbnail(activity.getContentResolver(), id,
                            MediaStore.Video.Thumbnails.MINI_KIND, null);
                } else {
                    BitmapFactory.Options o = new BitmapFactory.Options();
                    o.inSampleSize = 8;
                    try (InputStream in = activity.getContentResolver().openInputStream(uri)) {
                        bmp = BitmapFactory.decodeStream(in, null, o);
                    }
                    if (bmp != null) bmp = ThumbnailUtils.extractThumbnail(bmp, 320, 320);
                }
                if (bmp == null) return notFound();
                ByteArrayOutputStream buf = new ByteArrayOutputStream();
                bmp.compress(Bitmap.CompressFormat.JPEG, 80, buf);
                return response("image/jpeg", new ByteArrayInputStream(buf.toByteArray()), "private, max-age=3600");
            }
            if ("file".equals(parts[0])) {
                String mime = activity.getContentResolver().getType(uri);
                InputStream in = activity.getContentResolver().openInputStream(uri);
                if (in == null) return notFound();
                return response(mime != null ? mime : "application/octet-stream", in, "no-store");
            }
        } catch (Exception e) {
            android.util.Log.w("KantraGallery", "serve " + path, e);
        }
        return notFound();
    }

    private static WebResourceResponse response(String mime, InputStream data, String cache) {
        Map<String, String> h = new HashMap<>();
        h.put("Cache-Control", cache);
        return new WebResourceResponse(mime, null, 200, "OK", h, data);
    }

    private static WebResourceResponse notFound() {
        return new WebResourceResponse("text/plain", null, 404, "Not Found", new HashMap<>(), new ByteArrayInputStream(new byte[0]));
    }
}
