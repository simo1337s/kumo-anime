// Kumo desktop shell.
//
// Starts the Kumo server (bound to 127.0.0.1) or attaches to one that is
// already running, then shows the UI in a native window. Every request the
// window makes carries the per-run shell token, which is how the server
// recognises its own desktop window when the browser Web UI is turned off.

const { app, BrowserWindow, ipcMain, shell, session, Menu, dialog, nativeImage, Tray, Notification, powerMonitor } = require("electron")
const { spawn, execFileSync } = require("node:child_process")
const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")
const http = require("node:http")

const isWindows = process.platform === "win32"
const isMac = process.platform === "darwin"

// Exit codes of the server (server/internal/lifecycle): restart the app into
// a new version, or quit for the installer that updates it.
const EXIT_RESTART = 75
const EXIT_INSTALLING = 76

// On Windows this is the AppUserModelID, which must match "appId" in
// electron-builder.yml: the installer's shortcuts carry it, and the taskbar
// groups the window with them.
const APP_ID = isWindows ? "app.kumo.desktop" : "kumo"
app.setName("Kumo")
app.setAppUserModelId?.(APP_ID)
// The window only talks to Kumo's own server (and AniList's login page): run
// Chromium's network service inside the app instead of a process of its
// own, which saves about 80 MB.
const features = ["NetworkServiceInProcess2"]
if (process.platform === "linux") {
    // Smooth video and scrolling on Linux.
    features.push("VaapiVideoDecodeLinuxGL", "VaapiVideoDecoder")
    app.commandLine.appendSwitch("ignore-gpu-blocklist")
}
app.commandLine.appendSwitch("enable-features", features.join(","))
if (isWindows && process.env.LOCALAPPDATA) {
    // Electron would keep the window's browser data (caches, the AniList
    // login window's cookies) in %APPDATA%\Kumo, which is the server's data
    // folder on Windows: keep it apart, with Kumo's other local files.
    app.setPath("userData", path.join(process.env.LOCALAPPDATA, "Kumo", "electron"))
}
if (isMac) {
    // Electron would keep them in ~/Library/Application Support/Kumo, which
    // is the server's data folder on macOS: keep them in a folder of their
    // own in it.
    app.setPath("userData", path.join(app.getPath("appData"), "Kumo", "electron"))
}

const runtimeDir = serverRuntimeDir()
let serverProc = null
let mainWindow = null
let baseUrl = null
let tray = null

// The desktop app's own preferences (Settings › App › Desktop app), kept
// next to its browser data.
//   keepRunning     closing the window leaves Kumo running (tray icon, or the
//                   Dock on a Mac): streaming to other devices, downloads and
//                   the auto downloader keep going without the window's
//                   memory. Off unless turned on (keepRunningChosen: it was
//                   on by default for a while, and that doesn't count).
//   freeWhenLocked  a few minutes after the computer is locked, the window
//                   lets go of its page, and loads it again on unlocking.
//   toldKeepRunning the notice about it was shown (the first time).
const prefs = { keepRunning: false, keepRunningChosen: false, freeWhenLocked: true, toldKeepRunning: false }
const prefsFile = () => path.join(app.getPath("userData"), "desktop.json")

function loadPrefs() {
    const saved = readJSON(prefsFile()) || {}
    for (const k of Object.keys(prefs)) if (typeof saved[k] === typeof prefs[k]) prefs[k] = saved[k]
    if (!prefs.keepRunningChosen) prefs.keepRunning = false
}

function savePrefs() {
    try {
        fs.mkdirSync(path.dirname(prefsFile()), { recursive: true })
        fs.writeFileSync(prefsFile(), JSON.stringify(prefs, null, 2))
    } catch (err) {
        console.error("desktop preferences:", err)
    }
}

// Starting with the computer (Settings › App › Desktop app), off unless
// turned on: the system's own login items on Windows and macOS, an autostart
// entry on Linux. Started that way, Kumo opens in the background (tray, or
// the Dock on a Mac) when it keeps running without its window; otherwise
// with its window, so it's never running unseen.
const BACKGROUND = "--background"
const autostartFile = () => path.join(process.env.XDG_CONFIG_HOME || path.join(os.homedir(), ".config"), "autostart", "kumo.desktop")

function startsAtLogin() {
    if (process.platform === "linux") return fs.existsSync(autostartFile())
    return app.getLoginItemSettings(isWindows ? { args: [BACKGROUND] } : undefined).openAtLogin
}

// setStartAtLogin turns starting with the computer on or off; it returns
// what went wrong, if anything.
function setStartAtLogin(on) {
    if (process.platform === "linux") {
        const file = autostartFile()
        try {
            if (!on) {
                fs.rmSync(file, { force: true })
                return null
            }
            // The package's launcher, else this Electron with this app.
            const exec = fs.existsSync("/usr/bin/kumo") ? "/usr/bin/kumo" : `"${process.execPath}" "${app.getAppPath()}"`
            fs.mkdirSync(path.dirname(file), { recursive: true })
            fs.writeFileSync(file, ["[Desktop Entry]", "Type=Application", "Name=Kumo", "Comment=Kumo in the background", `Exec=${exec} ${BACKGROUND}`, "Icon=kumo-anime", "Terminal=false", "X-GNOME-Autostart-enabled=true", ""].join("\n"))
            return null
        } catch (err) {
            return String(err?.message || err)
        }
    }
    app.setLoginItemSettings(isWindows ? { openAtLogin: on, args: [BACKGROUND] } : { openAtLogin: on })
    if (isMac && on && app.getLoginItemSettings().status === "requires-approval") {
        return "macOS asks you to allow it: System Settings › General › Login Items, turn on Kumo."
    }
    return startsAtLogin() === on ? null : "The system didn't take the change."
}

// startedAtLogin reports a launch by the system at login.
function startedAtLogin() {
    if (process.argv.includes(BACKGROUND)) return true
    return isMac && app.getLoginItemSettings().wasOpenedAtLogin
}

// Where the server writes server.json and the shell token: the same folder
// as its config.RuntimeDir().
function serverRuntimeDir() {
    if (isWindows) {
        const local = process.env.LOCALAPPDATA
        return local ? path.join(local, "Kumo", "run") : path.join(os.tmpdir(), "kumo")
    }
    // macOS: $TMPDIR, the user's own temporary folder.
    if (isMac) return path.join(os.tmpdir(), "kumo")
    return path.join(process.env.XDG_RUNTIME_DIR || os.tmpdir(), "kumo")
}

function serverCandidates() {
    if (isWindows) {
        return [
            process.env.KUMO_SERVER,
            path.join(process.resourcesPath, "kumo.exe"), // installed or portable app: resources\kumo.exe
            path.join(__dirname, "..", "dist", "windows", "kumo.exe"), // development checkout
        ]
    }
    if (isMac) {
        return [
            process.env.KUMO_SERVER,
            path.join(process.resourcesPath, "kumo"), // Kumo.app/Contents/Resources/kumo
            path.join(__dirname, "..", "dist", "kumo"), // development checkout
        ]
    }
    return [
        process.env.KUMO_SERVER,
        path.join(__dirname, "..", "kumo"), // /usr/lib/kumo/app -> /usr/lib/kumo/kumo
        path.join(__dirname, "..", "dist", "kumo"), // development checkout
        "/usr/lib/kumo/kumo",
    ]
}

function findServerBinary() {
    const candidates = serverCandidates().filter(Boolean)
    // X_OK is only an existence check on Windows.
    return candidates.find(p => {
        try {
            fs.accessSync(p, fs.constants.X_OK)
            return true
        } catch {
            return false
        }
    })
}

function readJSON(p) {
    try {
        return JSON.parse(fs.readFileSync(p, "utf8"))
    } catch {
        return null
    }
}

function ping(url) {
    return new Promise(resolve => {
        const req = http.get(url + "/api/status", { timeout: 1500, headers: { "X-Kumo-Shell": readToken() || "" } }, res => {
            res.resume()
            resolve(res.statusCode < 500)
        })
        req.on("error", () => resolve(false))
        req.on("timeout", () => {
            req.destroy()
            resolve(false)
        })
    })
}

function readToken() {
    try {
        return fs.readFileSync(path.join(runtimeDir, "shell-token"), "utf8").trim()
    } catch {
        return ""
    }
}

async function existingServer() {
    const info = readJSON(path.join(runtimeDir, "server.json"))
    if (!info?.port) return null
    try {
        process.kill(info.pid, 0)
    } catch {
        return null
    }
    const url = `http://127.0.0.1:${info.port}`
    return (await ping(url)) ? url : null
}

function startServer() {
    return new Promise((resolve, reject) => {
        const bin = findServerBinary()
        if (!bin) {
            const msg = isWindows
                ? "Could not find the Kumo server (kumo.exe). Reinstall Kumo or set KUMO_SERVER."
                : isMac
                  ? "Could not find the Kumo server in the app. Download Kumo again or set KUMO_SERVER."
                  : "Could not find the Kumo server binary (kumo). Reinstall the package or set KUMO_SERVER."
            return reject(new Error(msg))
        }
        // windowsHide: on Windows the server, and the ffmpeg, ffprobe and bash
        // processes it starts (they share its console), never pop up a console
        // window. Elsewhere it does nothing. Windows has no signals to ask the
        // server to stop: there it stops when its input closes (see
        // before-quit).
        const args = isWindows ? ["--desktop", "--exit-with-stdin"] : ["--desktop"]
        serverProc = spawn(bin, args, { stdio: [isWindows ? "pipe" : "ignore", "pipe", "pipe"], env: process.env, windowsHide: true })
        let resolved = false
        const onData = buf => {
            const text = buf.toString()
            process.stdout.write(text)
            const m = /listening on http:\/\/([\d.]+):(\d+)/.exec(text)
            if (m && !resolved) {
                resolved = true
                resolve(`http://127.0.0.1:${m[2]}`)
            }
            if (/could not start the server/.test(text) && !resolved) {
                resolved = true
                reject(new Error(text.trim()))
            }
        }
        serverProc.stdout.on("data", onData)
        serverProc.stderr.on("data", onData)
        serverProc.on("exit", code => {
            // The server stopped for an update: no error box. After an
            // update was installed, the whole app starts again, so that its
            // window loads the new main.js too. On Windows an installer is
            // replacing Kumo, and starts it when it's done; on macOS the
            // server has put the new app in place, and a helper opens it once
            // this one has quit.
            if (code === EXIT_RESTART || code === EXIT_INSTALLING) {
                serverProc = null
                app.isQuitting = true
                if (code === EXIT_RESTART) app.relaunch()
                app.exit(0)
                return
            }
            if (!resolved) reject(new Error(`The Kumo server exited (code ${code}).`))
            serverProc = null
            if (resolved && !app.isQuitting) {
                const msg = "The Kumo server stopped unexpectedly. Restart Kumo to continue."
                if (mainWindow) {
                    dialog.showErrorBox("Kumo", msg)
                    return
                }
                // Running without a window: no box that would hold up a
                // shutdown (Windows stops the server first), just a notice.
                if (Notification.isSupported()) new Notification({ title: "Kumo stopped", body: msg }).show()
                app.isQuitting = true
                app.quit()
            }
        })
        setTimeout(() => {
            if (!resolved) {
                resolved = true
                reject(new Error("The Kumo server did not start in time."))
            }
        }, 30000)
    })
}

function installShellHeader() {
    const filter = { urls: ["http://127.0.0.1/*", "http://127.0.0.1:*/*"] }
    session.defaultSession.webRequest.onBeforeSendHeaders(filter, (details, cb) => {
        const token = readToken()
        if (token) details.requestHeaders["X-Kumo-Shell"] = token
        cb({ requestHeaders: details.requestHeaders })
    })
}

function isInternal(url) {
    // Any port on 127.0.0.1 is Kumo (the port can be changed in Settings).
    return /^http:\/\/127\.0\.0\.1(:\d+)?\//.test(url)
}

// macOS keeps the app's menu in the menu bar, and the standard shortcuts
// (copy and paste, Cmd+Q, Cmd+W, full screen…) only work through its items.
// Linux and Windows show no menu.
function appMenu() {
    if (!isMac) return null
    return Menu.buildFromTemplate([{ role: "appMenu" }, { role: "editMenu" }, { role: "viewMenu" }, { role: "windowMenu" }])
}

// The window's own icon. On Linux it's invisible: KDE and other desktops draw
// it in the window's title bar, where Kumo shows none, while the taskbar and
// the app menu take Kumo's icon from its desktop entry. On Windows the
// window's icon is also the taskbar's.
function windowIcon() {
    if (process.platform === "linux") return nativeImage.createFromBitmap(Buffer.alloc(32 * 32 * 4), { width: 32, height: 32 })
    const iconPath = [path.join(__dirname, "icon.png")].find(p => fs.existsSync(p))
    return iconPath ? nativeImage.createFromPath(iconPath) : undefined
}

function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1600,
        height: 1000,
        minWidth: 960,
        minHeight: 620,
        title: "Kumo",
        backgroundColor: "#0a0a0b",
        autoHideMenuBar: true,
        show: false,
        icon: windowIcon(),
        webPreferences: {
            preload: path.join(__dirname, "preload.js"),
            contextIsolation: true,
            sandbox: true,
            nodeIntegration: false,
            spellcheck: false,
        },
    })
    Menu.setApplicationMenu(appMenu())
    mainWindow.once("ready-to-show", () => mainWindow.show())

    // External links open in the system browser; the app never navigates away.
    mainWindow.webContents.setWindowOpenHandler(({ url }) => {
        if (/^https?:\/\//.test(url) && !isInternal(url)) shell.openExternal(url)
        return { action: "deny" }
    })
    mainWindow.webContents.on("will-navigate", (e, url) => {
        if (!isInternal(url)) {
            e.preventDefault()
            if (/^https?:\/\//.test(url)) shell.openExternal(url)
        }
    })
    mainWindow.webContents.on("before-input-event", (_e, input) => {
        if (input.type === "keyDown" && input.key === "F11") mainWindow.setFullScreen(!mainWindow.isFullScreen())
        if (input.type === "keyDown" && input.control && input.shift && input.key.toLowerCase() === "i") mainWindow.webContents.toggleDevTools()
        if (input.type === "keyDown" && input.control && input.key.toLowerCase() === "r") mainWindow.webContents.reload()
    })
    mainWindow.on("closed", () => {
        mainWindow = null
        parkedUrl = null
    })
    // Windows is shutting down or signing out, which stops the server too:
    // that isn't worth an error box (the event only exists on Windows).
    mainWindow.on("query-session-end", () => (app.isQuitting = true))
    mainWindow.loadURL(baseUrl)
}

// AniList login: open the authorize page in a separate window and capture
// the access token from the redirect (#access_token=…) or the PIN page.
ipcMain.handle("kumo:anilist-login", async (_e, url) => {
    if (typeof url !== "string" || !url.startsWith("https://anilist.co/")) return null
    return new Promise(resolve => {
        const win = new BrowserWindow({
            width: 520,
            height: 760,
            parent: mainWindow ?? undefined,
            modal: false,
            title: "Log in with AniList",
            backgroundColor: "#0b1622",
            autoHideMenuBar: true,
            webPreferences: { partition: "persist:anilist", contextIsolation: true, sandbox: true },
        })
        let done = false
        const finish = token => {
            if (done) return
            done = true
            resolve(token)
            if (!win.isDestroyed()) win.close()
        }
        const check = u => {
            const m = /access_token=([^&#\s]+)/.exec(u || "")
            if (m) finish(decodeURIComponent(m[1]))
        }
        win.webContents.on("will-redirect", (_ev, u) => check(u))
        win.webContents.on("did-navigate", (_ev, u) => check(u))
        win.webContents.on("did-navigate-in-page", (_ev, u) => check(u))
        win.webContents.on("did-finish-load", async () => {
            check(win.webContents.getURL())
            // The PIN page shows the token in a text box.
            try {
                const t = await win.webContents.executeJavaScript(
                    "(document.querySelector('textarea')?.value || document.querySelector('.token, input[readonly]')?.value || '').trim()",
                )
                if (t && t.length > 100) finish(t)
            } catch {
                /* ignore */
            }
        })
        win.webContents.setWindowOpenHandler(() => ({ action: "deny" }))
        win.on("closed", () => finish(null))
        win.loadURL(url)
    })
})

ipcMain.on("kumo:open-external", (_e, url) => {
    if (typeof url === "string" && /^https?:\/\//.test(url)) shell.openExternal(url)
})

ipcMain.handle("kumo:get-prefs", () => ({ keepRunning: prefs.keepRunning, freeWhenLocked: prefs.freeWhenLocked, startAtLogin: startsAtLogin() }))
// set-pref returns true, or { error } when it couldn't be done.
ipcMain.handle("kumo:set-pref", (_e, name, value) => {
    if (typeof value !== "boolean") return { error: "not a setting" }
    if (name === "startAtLogin") {
        const error = setStartAtLogin(value)
        return error ? { error } : true
    }
    if (!["keepRunning", "freeWhenLocked"].includes(name)) return { error: "not a setting" }
    prefs[name] = value
    if (name === "keepRunning") prefs.keepRunningChosen = true
    savePrefs()
    updateTray()
    return true
})

ipcMain.on("kumo:quit", () => app.quit())

// ---------------------------------------------------------------------------
// Updated while running: a package update (makepkg) replaces Kumo's files
// but not the Kumo running in the background. Opened again, it restarts into
// the version installed: the server stops asking to be started again
// (exit code 75), and the app does, with its new files.

// installedVersion is the version of the server program on disk ("1.0.94"),
// or "" when it can't be told.
function installedVersion() {
    const bin = findServerBinary()
    if (!bin) return ""
    try {
        const out = execFileSync(bin, ["--version"], { timeout: 5000, windowsHide: true }).toString()
        return (/(\d+\.\d+\.\d+)/.exec(out) || [])[1] || ""
    } catch {
        return ""
    }
}

// serverCall makes a request to a running Kumo server as its desktop window.
function serverCall(url, method, p) {
    return new Promise(resolve => {
        const req = http.request(url + p, { method, timeout: 3000, headers: { "X-Kumo-Shell": readToken() || "", "Content-Type": "application/json" } }, res => {
            let body = ""
            res.setEncoding("utf8")
            res.on("data", c => (body += c))
            res.on("end", () => {
                try {
                    resolve(res.statusCode < 300 ? JSON.parse(body || "null") ?? true : null)
                } catch {
                    resolve(null)
                }
            })
        })
        req.on("error", () => resolve(null))
        req.on("timeout", () => {
            req.destroy()
            resolve(null)
        })
        req.end(method === "POST" ? "{}" : undefined)
    })
}

// restartIfOutdated restarts the Kumo running at url when it's another
// version than the one installed, and reports whether it did.
async function restartIfOutdated(url) {
    if (!url) return false
    const installed = installedVersion()
    const running = (await serverCall(url, "GET", "/api/status"))?.version
    if (!installed || !running || installed === running) return false
    console.log(`Kumo ${running} is running, ${installed} is installed: restarting into it`)
    return !!(await serverCall(url, "POST", "/api/update/restart"))
}

let restarting = false

// showWindow brings Kumo's window back, or opens a new one after it was
// closed while Kumo kept running.
async function showWindow() {
    if (!baseUrl || restarting) return
    // Updated since it started: the new version opens instead.
    if (await restartIfOutdated(baseUrl)) {
        restarting = true
        return
    }
    if (!mainWindow) return createWindow()
    if (mainWindow.isMinimized()) mainWindow.restore()
    mainWindow.show()
    mainWindow.focus()
}

// The tray icon, while Kumo keeps running with its window closed (Windows
// and Linux: on a Mac the Dock icon does it). Without a tray on the
// desktop (GNOME without an extension), opening Kumo again shows its window.
function updateTray() {
    if (isMac) return
    if (prefs.keepRunning && !tray) {
        const icon = nativeImage.createFromPath(path.join(__dirname, "icon.png"))
        tray = new Tray(icon.isEmpty() ? icon : icon.resize({ width: 32, height: 32, quality: "best" }))
        tray.setToolTip("Kumo")
        tray.setContextMenu(
            Menu.buildFromTemplate([
                { label: "Open Kumo", click: showWindow },
                { type: "separator" },
                { label: "Quit Kumo", click: () => app.quit() },
            ]),
        )
        tray.on("click", showWindow)
    } else if (!prefs.keepRunning && tray) {
        tray.destroy()
        tray = null
    }
}

// The window was closed and Kumo keeps running: say so, the first time.
function wentToBackground() {
    if (prefs.toldKeepRunning || !Notification.isSupported()) return
    prefs.toldKeepRunning = true
    savePrefs()
    new Notification({
        title: "Kumo is still running",
        body: isMac
            ? "Streaming to your other devices and downloads keep going. Click Kumo in the Dock to open it, or quit it with Cmd+Q."
            : "Streaming to your other devices and downloads keep going. Open or quit Kumo from its tray icon.",
    }).show()
}

// Locked computer: after a few minutes the window lets go of its page (the
// app's memory, cached images…) and loads it again when the computer is
// unlocked. Not while the player, the manga reader or Settings are open, so
// nothing is lost.
const PARK_AFTER = 5 * 60_000
const PARKED_PAGE = "data:text/html,<body style='background:%230a0a0b'></body>"
let lockTimer = null
let parkedUrl = null

async function parkPage() {
    if (!prefs.freeWhenLocked || !mainWindow || parkedUrl) return
    const wc = mainWindow.webContents
    let busy = true
    try {
        busy = await wc.executeJavaScript("!!document.querySelector('video') || /^\\/(manga\\/read|settings|webview)/.test(location.pathname)")
    } catch {
        /* keep it */
    }
    if (busy || !mainWindow || parkedUrl) return
    parkedUrl = wc.getURL()
    wc.loadURL(PARKED_PAGE)
}

function unparkPage() {
    clearTimeout(lockTimer)
    if (!parkedUrl) return
    const url = parkedUrl
    parkedUrl = null
    if (mainWindow && isInternal(url)) mainWindow.loadURL(url)
}

// Desktop shortcuts are copies of kumo.desktop: those made before the icon
// was renamed kumo-anime (with the new logo) ask for "kumo", which desktops
// keep showing as the old logo. Point them at the new icon, in place, so
// they keep their permissions and the desktop's trust.
function updateDesktopShortcuts() {
    if (process.platform !== "linux") return
    let dir, names
    try {
        dir = app.getPath("desktop")
        names = fs.readdirSync(dir)
    } catch {
        return
    }
    for (const name of names) {
        if (!name.endsWith(".desktop")) continue
        const file = path.join(dir, name)
        try {
            if (!fs.lstatSync(file).isFile()) continue // never through a link
            const text = fs.readFileSync(file, "utf8")
            const icon = /^Icon=kumo[ \t]*\r?$/m
            if (!/^Exec=(\/usr\/bin\/)?kumo(\s|$)/m.test(text) || !icon.test(text)) continue
            fs.writeFileSync(file, text.replace(icon, "Icon=kumo-anime"))
        } catch {
            // someone else's file, or one we can't write: leave it
        }
    }
}

async function boot() {
    try {
        baseUrl = (await existingServer()) || (await startServer())
    } catch (err) {
        dialog.showErrorBox("Kumo could not start", String(err?.message || err))
        app.exit(1)
        return
    }
    // Wait until the token file exists (written right after startup).
    for (let i = 0; i < 50 && !readToken(); i++) await new Promise(r => setTimeout(r, 100))
    installShellHeader()
    // Started with the computer: in the background, if Kumo runs without
    // its window (the tray icon or the Dock opens it).
    if (!(startedAtLogin() && prefs.keepRunning)) createWindow()
    updateTray()
    updateDesktopShortcuts()
    powerMonitor.on("lock-screen", () => {
        clearTimeout(lockTimer)
        lockTimer = setTimeout(parkPage, PARK_AFTER)
    })
    powerMonitor.on("unlock-screen", unparkPage)
    // Brought back some other way (the screen was never locked by the
    // system's own lock, a missed event…).
    app.on("browser-window-focus", unparkPage)
}

if (!app.requestSingleInstanceLock()) {
    // Kumo is running already and shows its window. When it's an older
    // version (updated while it ran in the background), it's asked to
    // restart into this one: versions before this check only know that.
    existingServer()
        .then(restartIfOutdated)
        .catch(() => {})
        .finally(() => app.quit())
} else {
    // Opening Kumo again shows its window, also after it was closed while
    // Kumo kept running.
    app.on("second-instance", (_e, argv) => {
        // Started with the computer while already running: nothing to show.
        if (!argv.includes(BACKGROUND)) showWindow()
    })
    app.on("activate", showWindow) // the Dock icon (macOS)
    app.whenReady().then(() => {
        loadPrefs()
        return boot()
    })
    app.on("window-all-closed", () => {
        if (!prefs.keepRunning || app.isQuitting) return app.quit()
        wentToBackground()
    })
    app.on("before-quit", event => {
        app.isQuitting = true
        if (!serverProc) return
        if (!isWindows) {
            serverProc.kill("SIGTERM")
            return
        }
        // Windows: closing its input lets the server stop what it started
        // (mpv, ffmpeg) and save; quit once it's gone, or after 5 seconds.
        // kill() would end it at once.
        event.preventDefault()
        const proc = serverProc
        const timer = setTimeout(() => proc.kill(), 5000)
        proc.once("exit", () => {
            clearTimeout(timer)
            serverProc = null
            app.quit()
        })
        proc.stdin?.end()
    })
}
