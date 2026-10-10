package app.kumo;

import android.app.Activity;
import android.app.PendingIntent;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import android.widget.Toast;

import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;

/**
 * Installs an update: the APK of a newer Kumo, which the server downloaded
 * (and checked) into the app's own files. Android shows what it installs and
 * asks first; the answer comes back to MainActivity (ACTION).
 */
final class Installer {
    static final String ACTION = "app.kumo.INSTALL_STATUS";

    private Installer() {
    }

    static void install(Activity a, String path) {
        File apk;
        try {
            apk = new File(path).getCanonicalFile();
            // Only an APK in Kumo's own files.
            String own = a.getApplicationInfo().dataDir;
            String ownCache = a.getCacheDir().getCanonicalPath();
            String p = apk.getPath();
            if (!p.endsWith(".apk") || !(p.startsWith(new File(own).getCanonicalPath() + File.separator) || p.startsWith(ownCache + File.separator)) || !apk.isFile()) {
                Toast.makeText(a, "That isn't a Kumo update.", Toast.LENGTH_LONG).show();
                return;
            }
        } catch (IOException e) {
            Toast.makeText(a, "Can't read the update: " + e.getMessage(), Toast.LENGTH_LONG).show();
            return;
        }
        // Android 8+: Kumo needs the user's permission to install apps.
        if (Build.VERSION.SDK_INT >= 26 && !a.getPackageManager().canRequestPackageInstalls()) {
            try {
                a.startActivity(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + a.getPackageName())));
                Toast.makeText(a, "Allow Kumo to install apps, then press Update again.", Toast.LENGTH_LONG).show();
            } catch (Exception e) {
                Toast.makeText(a, "Allow Kumo to install unknown apps (on a Fire TV: Settings › My Fire TV › Developer options › Install unknown apps), then press Update again.", Toast.LENGTH_LONG).show();
            }
            return;
        }
        PackageInstaller installer = a.getPackageManager().getPackageInstaller();
        PackageInstaller.SessionParams params = new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
        params.setAppPackageName(a.getPackageName());
        PackageInstaller.Session session = null;
        try {
            int id = installer.createSession(params);
            session = installer.openSession(id);
            try (InputStream in = new FileInputStream(apk); OutputStream out = session.openWrite("kumo.apk", 0, apk.length())) {
                byte[] buf = new byte[256 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
                session.fsync(out);
            }
            Intent back = new Intent(a, MainActivity.class).setAction(ACTION).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
            int flags = PendingIntent.FLAG_UPDATE_CURRENT;
            // The installer adds the result to it.
            if (Build.VERSION.SDK_INT >= 31) flags |= PendingIntent.FLAG_MUTABLE;
            PendingIntent result = PendingIntent.getActivity(a, id, back, flags);
            session.commit(result.getIntentSender());
            session.close();
            session = null;
        } catch (IOException | RuntimeException e) {
            Toast.makeText(a, "The update couldn't be installed: " + e.getMessage(), Toast.LENGTH_LONG).show();
            if (session != null) session.abandon();
        }
    }
}
