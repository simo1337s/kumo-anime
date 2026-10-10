package app.kumo;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.UiModeManager;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.Color;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

/**
 * Kumo for Android, made for TVs (a Fire TV Stick) and the libraries the
 * computers at home share: it runs Kumo's server (KumoServer) and shows its
 * web UI, which on a TV is driven by the remote (TV mode, web/src/lib/tv.ts).
 */
public class MainActivity extends Activity {
    private final Handler main = new Handler(Looper.getMainLooper());
    private FrameLayout root;
    private WebView web;
    private TextView message;
    private ScrollView messageScroll;
    private View fullscreen;
    private WebChromeClient.CustomViewCallback fullscreenDone;
    private WifiManager.MulticastLock multicast;
    private boolean loaded;
    private boolean starting;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        root = new FrameLayout(this);
        root.setBackgroundColor(Color.rgb(11, 11, 15));
        web = new WebView(this);
        web.setBackgroundColor(Color.rgb(11, 11, 15));
        web.setVisibility(View.INVISIBLE);
        setUpWebView();
        root.addView(web, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        message = new TextView(this);
        message.setTextColor(Color.rgb(230, 230, 235));
        message.setTextSize(TypedValue.COMPLEX_UNIT_SP, 20);
        message.setGravity(Gravity.CENTER);
        int pad = (int) (32 * getResources().getDisplayMetrics().density);
        message.setPadding(pad, pad, pad, pad);
        message.setText("Starting Kumo…");
        messageScroll = new ScrollView(this);
        messageScroll.setFillViewport(true);
        messageScroll.addView(message, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(messageScroll, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        setContentView(root);

        // The other Kumo apps announce themselves by multicast, which Wi-Fi
        // drops for apps without this.
        WifiManager wifi = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
        if (wifi != null) {
            multicast = wifi.createMulticastLock("kumo");
            multicast.setReferenceCounted(false);
            multicast.acquire();
        }
        startServer();
        onNewIntent(getIntent());
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void setUpWebView() {
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setUserAgentString(s.getUserAgentString() + " KumoAndroid/" + versionName() + (isTV() ? " KumoTV" : ""));
        web.addJavascriptInterface(new Bridge(), "KumoAndroid");
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
                return leave(req.getUrl());
            }

            @SuppressWarnings("deprecation")
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, String url) {
                return leave(Uri.parse(url));
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                if (!loaded && url.startsWith(KumoServer.URL)) {
                    loaded = true;
                    web.setVisibility(View.VISIBLE);
                    messageScroll.setVisibility(View.GONE);
                    web.requestFocus();
                }
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest req, WebResourceError err) {
                if (req.isForMainFrame() && !KumoServer.get(MainActivity.this).running()) {
                    loaded = false;
                    web.setVisibility(View.INVISIBLE);
                    messageScroll.setVisibility(View.VISIBLE);
                    startServer();
                }
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            // The player's fullscreen (on a phone; a TV is all screen).
            @Override
            public void onShowCustomView(View view, CustomViewCallback done) {
                exitFullscreen();
                fullscreen = view;
                fullscreenDone = done;
                root.addView(view, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
                web.setVisibility(View.INVISIBLE);
            }

            @Override
            public void onHideCustomView() {
                exitFullscreen();
            }
        });
    }

    /** Only Kumo's pages show here: anything else (AniList, GitHub…) opens in a browser, if there's one, so the bridge stays Kumo's own. */
    private boolean leave(Uri u) {
        if ("127.0.0.1".equals(u.getHost()) && u.getPort() == KumoServer.PORT) return false;
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, u));
        } catch (Exception e) {
            Toast.makeText(this, "No app here opens " + u.getHost(), Toast.LENGTH_LONG).show();
        }
        return true;
    }

    private void exitFullscreen() {
        if (fullscreen == null) return;
        root.removeView(fullscreen);
        fullscreen = null;
        if (fullscreenDone != null) fullscreenDone.onCustomViewHidden();
        fullscreenDone = null;
        web.setVisibility(View.VISIBLE);
    }

    /** Starts the server if needed, and shows Kumo once it answers. */
    private void startServer() {
        if (starting) return;
        starting = true;
        final KumoServer server = KumoServer.get(this);
        new Thread(() -> {
            String failure = null;
            try {
                server.start();
                long until = System.currentTimeMillis() + 40_000;
                while (!server.ready()) {
                    if (!server.running()) {
                        failure = "Kumo's server stopped.\n\n" + server.whyStopped();
                        break;
                    }
                    if (System.currentTimeMillis() > until) {
                        String lines = server.lastLines();
                        failure = "Kumo's server doesn't answer." + (lines.isEmpty() ? "" : "\n\n" + lines);
                        break;
                    }
                    Thread.sleep(250);
                }
            } catch (Exception e) {
                failure = "Kumo's server couldn't start: " + e.getMessage();
            }
            final String error = failure;
            main.post(() -> {
                starting = false;
                if (error != null) {
                    Log.e("kumo", error);
                    showMessage(error + "\n\nPress OK to try again.", true);
                    return;
                }
                showMessage("Starting Kumo…", false);
                web.loadUrl(KumoServer.URL);
            });
        }, "kumo-start").start();
    }

    /** The screen before Kumo shows: starting, or why it didn't. */
    private void showMessage(String text, boolean error) {
        message.setText(text);
        // An error has the details (the server's last lines): smaller, from the top, scrolling.
        message.setTextSize(TypedValue.COMPLEX_UNIT_SP, error ? 14 : 20);
        message.setGravity(error ? Gravity.START | Gravity.TOP : Gravity.CENTER);
        messageScroll.scrollTo(0, 0);
        // The remote's Up and Down scroll it.
        if (error) messageScroll.requestFocus();
    }

    @Override
    protected void onResume() {
        super.onResume();
        // Android may have stopped it while Kumo was in the background.
        if (!starting && !KumoServer.get(this).running()) {
            loaded = false;
            web.setVisibility(View.INVISIBLE);
            messageScroll.setVisibility(View.VISIBLE);
            showMessage("Starting Kumo…", false);
            startServer();
        }
    }

    @Override
    protected void onDestroy() {
        if (multicast != null && multicast.isHeld()) multicast.release();
        if (isFinishing()) KumoServer.get(this).stop();
        web.destroy();
        super.onDestroy();
    }

    // ------------------------------------------------------------------ keys

    @Override
    public boolean dispatchKeyEvent(KeyEvent e) {
        int code = e.getKeyCode();
        boolean down = e.getAction() == KeyEvent.ACTION_DOWN;
        switch (code) {
            case KeyEvent.KEYCODE_BACK:
                // On release, once: a long press doesn't go back twice.
                if (!down && !e.isCanceled()) back();
                return true;
            case KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE:
            case KeyEvent.KEYCODE_MEDIA_PLAY:
            case KeyEvent.KEYCODE_MEDIA_PAUSE:
                if (down && e.getRepeatCount() == 0) js("window.kumoKey&&window.kumoKey('playpause')");
                return true;
            case KeyEvent.KEYCODE_MEDIA_REWIND:
                if (down) js("window.kumoKey&&window.kumoKey('rewind')");
                return true;
            case KeyEvent.KEYCODE_MEDIA_FAST_FORWARD:
                if (down) js("window.kumoKey&&window.kumoKey('forward')");
                return true;
            case KeyEvent.KEYCODE_DPAD_CENTER:
            case KeyEvent.KEYCODE_ENTER:
                // Kumo didn't start: OK tries again.
                if (!loaded && !starting && down) {
                    showMessage("Starting Kumo…", false);
                    startServer();
                    return true;
                }
                break;
            default:
                break;
        }
        return super.dispatchKeyEvent(e);
    }

    /** Back: the page closes what's open or goes back; at Home, Kumo closes. */
    private void back() {
        if (fullscreen != null) {
            exitFullscreen();
            return;
        }
        if (!loaded) {
            finish();
            return;
        }
        web.evaluateJavascript("(window.kumoBack&&window.kumoBack())?1:0", v -> {
            if (!"1".equals(v)) finish();
        });
    }

    private void js(String code) {
        if (loaded) web.evaluateJavascript(code, null);
    }

    // ------------------------------------------------------------ the bridge

    /** What the page asks of the app (window.KumoAndroid). */
    private final class Bridge {
        /** Keeps the screen on while a video plays. */
        @JavascriptInterface
        public void keepAwake(final boolean on) {
            main.post(() -> {
                if (on) getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
                else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
            });
        }

        /** Installs an update the server downloaded (Android asks first). */
        @JavascriptInterface
        public void installApk(final String path) {
            main.post(() -> Installer.install(MainActivity.this, path));
        }
    }

    // Android answers an install here (Installer).
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        if (intent == null || !Installer.ACTION.equals(intent.getAction())) return;
        int status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE);
        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            Intent confirm = intent.getParcelableExtra(Intent.EXTRA_INTENT);
            if (confirm != null) startActivity(confirm);
        } else if (status != PackageInstaller.STATUS_SUCCESS) {
            String msg = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE);
            Toast.makeText(this, "The update wasn't installed" + (msg == null ? "" : ": " + msg), Toast.LENGTH_LONG).show();
        }
    }

    private boolean isTV() {
        UiModeManager ui = (UiModeManager) getSystemService(Context.UI_MODE_SERVICE);
        PackageManager pm = getPackageManager();
        return (ui != null && ui.getCurrentModeType() == Configuration.UI_MODE_TYPE_TELEVISION)
                || pm.hasSystemFeature(PackageManager.FEATURE_LEANBACK)
                || pm.hasSystemFeature("amazon.hardware.fire_tv");
    }

    private String versionName() {
        try {
            PackageInfo info = getPackageManager().getPackageInfo(getPackageName(), 0);
            return info.versionName;
        } catch (PackageManager.NameNotFoundException e) {
            return "0";
        }
    }
}
