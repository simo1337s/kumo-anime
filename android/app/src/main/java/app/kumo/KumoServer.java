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
import java.util.regex.Matcher;
import java.util.regex.Pattern;

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
    private Thread logs;
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

    private File binary() {
        return new File(ctx.getApplicationInfo().nativeLibraryDir, "libkumo.so");
    }

    /** Starts the server, unless it's running. */
    synchronized void start() throws IOException {
        if (running()) return;
        File bin = binary();
        if (!bin.isFile()) {
            throw new IOException("Android didn't install its program (" + bin + " is missing). " + device());
        }
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
        environment(pb.environment());
        synchronized (last) {
            last.clear();
        }
        final Process p = pb.start();
        process = p;
        logs = new Thread(() -> {
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

    private void environment(Map<String, String> env) {
        File files = ctx.getFilesDir();
        File cache = ctx.getCacheDir();
        env.put("HOME", files.getAbsolutePath());
        env.put("TMPDIR", cache.getAbsolutePath());
        env.put("XDG_RUNTIME_DIR", new File(cache, "run").getAbsolutePath());
        env.put("XDG_CACHE_HOME", cache.getAbsolutePath());
        env.put("XDG_CONFIG_HOME", new File(files, "config").getAbsolutePath());
        env.put("KUMO_DATA_DIR", new File(files, "kumo").getAbsolutePath());
        env.put("TZ", TimeZone.getDefault().getID());
        env.put("KUMO_DEVICE_NAME", deviceName());
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

    /**
     * Why the server stopped, as much as can be found out: its last lines
     * (once all of them are read), how it ended, what Android logged about
     * it, and whether the program runs at all.
     */
    String whyStopped() {
        Process p;
        Thread t;
        synchronized (this) {
            p = process;
            t = logs;
        }
        // Its last lines may still be on their way.
        if (t != null) {
            try {
                t.join(2000);
            } catch (InterruptedException ignored) {
            }
        }
        StringBuilder b = new StringBuilder();
        String lines = lastLines();
        if (!lines.isEmpty()) b.append(lines).append("\n\n");
        if (p != null) {
            try {
                b.append("It ended with ").append(ending(p.exitValue())).append(".\n");
            } catch (IllegalThreadStateException ignored) {
            }
            if (lines.isEmpty()) {
                String logged = androidLog(pid(p));
                if (!logged.isEmpty()) b.append(logged).append("\n");
                b.append(trial()).append("\n");
            }
        }
        b.append(device());
        return b.toString().trim();
    }

    /** An exit code, or the signal that ended the program. */
    private static String ending(int code) {
        // Android gives 128 + the signal for a killed program.
        if (code > 128 && code < 128 + 65) {
            int sig = code - 128;
            String name;
            switch (sig) {
                case 4: name = "SIGILL"; break;
                case 6: name = "SIGABRT"; break;
                case 7: name = "SIGBUS"; break;
                case 9: name = "SIGKILL"; break;
                case 11: name = "SIGSEGV"; break;
                case 15: name = "SIGTERM"; break;
                case 31: name = "SIGSYS"; break;
                default: name = "signal " + sig;
            }
            return "code " + code + " (" + name + ")";
        }
        return "code " + code;
    }

    /** What Android logged about the server (a crash, the linker's error). */
    private static String androidLog(int pid) {
        Ran lc = Ran.run(new ProcessBuilder("logcat", "-d", "-v", "brief", "-t", "600"), 5000, 4000);
        List<String> found = new ArrayList<>();
        String mark = pid > 0 ? "(" + pad(pid) + ")" : null;
        for (String line : lc.lines) {
            boolean ours = (mark != null && line.contains(mark)) || line.contains("libkumo");
            // The server's own lines are shown already.
            if (!ours || line.startsWith("I/" + TAG)) continue;
            found.add(line);
            if (found.size() > 8) found.remove(0);
        }
        return TextUtils.join("\n", found);
    }

    // logcat -v brief writes the pid as "( 123)", at least 5 wide.
    private static String pad(int pid) {
        String s = String.valueOf(pid);
        StringBuilder b = new StringBuilder();
        for (int i = s.length(); i < 5; i++) b.append(' ');
        return b.append(s).toString();
    }

    private static int pid(Process p) {
        Matcher m = Pattern.compile("pid=(\\d+)").matcher(p.toString());
        if (m.find()) return Integer.parseInt(m.group(1));
        for (Class<?> c = p.getClass(); c != null; c = c.getSuperclass()) {
            try {
                java.lang.reflect.Field f = c.getDeclaredField("pid");
                f.setAccessible(true);
                return f.getInt(p);
            } catch (Exception ignored) {
            }
        }
        return -1;
    }

    /** Runs the program alone (--version): does it run here at all? */
    private String trial() {
        ProcessBuilder pb = new ProcessBuilder(binary().getAbsolutePath(), "--version");
        environment(pb.environment());
        Ran r = Ran.run(pb, 5000, 20);
        if (r.error != null) return "Alone, the program doesn't start: " + r.error;
        if (r.code == null) return "Alone, the program doesn't end.";
        String said = TextUtils.join(" ", r.lines).trim();
        return "Alone, the program " + (said.isEmpty() ? "says nothing" : "says \"" + said + "\"") + " and ends with " + ending(r.code) + ".";
    }

    /** A program run to its end (or until it's taken too long): what it said, how it ended. */
    private static final class Ran {
        final List<String> lines = new ArrayList<>();
        Integer code;
        String error;

        static Ran run(ProcessBuilder pb, long ms, final int max) {
            final Ran ran = new Ran();
            final Process p;
            try {
                p = pb.redirectErrorStream(true).start();
            } catch (IOException e) {
                ran.error = e.getMessage();
                return ran;
            }
            Thread reader = new Thread(() -> {
                try (BufferedReader r = new BufferedReader(new InputStreamReader(p.getInputStream()))) {
                    String line;
                    while ((line = r.readLine()) != null) {
                        synchronized (ran.lines) {
                            ran.lines.add(line);
                            if (ran.lines.size() > max) ran.lines.remove(0);
                        }
                    }
                } catch (IOException ignored) {
                }
            }, "kumo-run");
            reader.setDaemon(true);
            reader.start();
            long until = System.currentTimeMillis() + ms;
            while (ran.code == null && System.currentTimeMillis() < until) {
                try {
                    ran.code = p.exitValue();
                } catch (IllegalThreadStateException e) {
                    try {
                        Thread.sleep(50);
                    } catch (InterruptedException ie) {
                        break;
                    }
                }
            }
            if (ran.code == null) p.destroy();
            try {
                reader.join(1000);
            } catch (InterruptedException ignored) {
            }
            synchronized (ran.lines) {
                return ran;
            }
        }
    }

    /** The device, for the error screen. */
    private String device() {
        String dir = ctx.getApplicationInfo().nativeLibraryDir;
        String abi = dir == null ? "?" : new File(dir).getName();
        return "Android " + Build.VERSION.RELEASE + " (API " + Build.VERSION.SDK_INT + "), " + Build.MANUFACTURER + " " + Build.MODEL
                + ", " + TextUtils.join("/", Build.SUPPORTED_ABIS) + ", program: " + abi + ", " + (binary().length() / (1024 * 1024)) + " MB";
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
