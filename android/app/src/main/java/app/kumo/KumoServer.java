package app.kumo;

import android.content.Context;
import android.os.Build;
import android.provider.Settings;
import android.text.TextUtils;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.TimeZone;

/**
 * Kumo's server, the same as on a computer, built for Android and shipped as
 * the app's "native library" libkumo.so (Android extracts those somewhere it
 * may run them). The app shows the web UI it serves on 127.0.0.1.
 */
final class KumoServer {
    static final int PORT = 43211;
    static final String URL = "http://127.0.0.1:" + PORT + "/";
    private static final String TAG = "kumo";

    private static KumoServer instance;

    private final Context ctx;
    private Process process;
    // The server's last lines, to show when it doesn't start.
    private final ArrayDeque<String> last = new ArrayDeque<>();

    private KumoServer(Context ctx) {
        this.ctx = ctx.getApplicationContext();
    }

    static synchronized KumoServer get(Context ctx) {
        if (instance == null) instance = new KumoServer(ctx);
        return instance;
    }

    synchronized boolean running() {
        if (process == null) return false;
        try {
            process.exitValue();
            return false;
        } catch (IllegalThreadStateException e) {
            return true;
        }
    }

    /** Starts the server, unless it's running. */
    synchronized void start() throws IOException {
        if (running()) return;
        File bin = new File(ctx.getApplicationInfo().nativeLibraryDir, "libkumo.so");
        File files = ctx.getFilesDir();
        File data = new File(files, "kumo");
        File cache = ctx.getCacheDir();
        File run = new File(cache, "run");
        //noinspection ResultOfMethodCallIgnored
        run.mkdirs();
        List<String> cmd = new ArrayList<>();
        cmd.add(bin.getAbsolutePath());
        cmd.add("--data-dir");
        cmd.add(data.getAbsolutePath());
        cmd.add("--port");
        cmd.add(String.valueOf(PORT));
        cmd.add("--web-ui");
        ProcessBuilder pb = new ProcessBuilder(cmd).directory(files).redirectErrorStream(true);
        Map<String, String> env = pb.environment();
        env.put("HOME", files.getAbsolutePath());
        env.put("TMPDIR", cache.getAbsolutePath());
        env.put("XDG_RUNTIME_DIR", run.getAbsolutePath());
        env.put("XDG_CACHE_HOME", cache.getAbsolutePath());
        env.put("XDG_CONFIG_HOME", new File(files, "config").getAbsolutePath());
        env.put("KUMO_DATA_DIR", data.getAbsolutePath());
        env.put("TZ", TimeZone.getDefault().getID());
        env.put("KUMO_DEVICE_NAME", deviceName());
        synchronized (last) {
            last.clear();
        }
        final Process p = pb.start();
        process = p;
        Thread logs = new Thread(() -> {
            try (BufferedReader r = new BufferedReader(new InputStreamReader(p.getInputStream()))) {
                String line;
                while ((line = r.readLine()) != null) {
                    Log.i(TAG, line);
                    synchronized (last) {
                        last.addLast(line);
                        while (last.size() > 12) last.removeFirst();
                    }
                }
            } catch (IOException ignored) {
            }
        }, "kumo-log");
        logs.setDaemon(true);
        logs.start();
    }

    /** Stops the server (the app is closing). */
    synchronized void stop() {
        if (process != null) {
            process.destroy();
            process = null;
        }
    }

    /** Whether the server answers. */
    boolean ready() {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(URL + "api/status").openConnection();
            c.setConnectTimeout(800);
            c.setReadTimeout(2000);
            return c.getResponseCode() == 200;
        } catch (IOException e) {
            return false;
        } finally {
            if (c != null) c.disconnect();
        }
    }

    /** The server's last lines. */
    String lastLines() {
        synchronized (last) {
            return TextUtils.join("\n", last);
        }
    }

    /** What the other Kumo apps call this one: the device's name. */
    private String deviceName() {
        String name = null;
        if (Build.VERSION.SDK_INT >= 25) {
            name = Settings.Global.getString(ctx.getContentResolver(), Settings.Global.DEVICE_NAME);
        }
        if (name == null || name.trim().isEmpty()) {
            name = Settings.Secure.getString(ctx.getContentResolver(), "bluetooth_name");
        }
        if (name == null || name.trim().isEmpty()) name = Build.MODEL;
        return name == null ? "Kumo" : name.trim();
    }
}
