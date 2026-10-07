// Kumo desktop shell.
//
// Starts the Kumo server (bound to 127.0.0.1) or attaches to one that is
// already running, then shows the UI in a native window. Every request the
// window makes carries the per-run shell token, which is how the server
// recognises its own desktop window when the browser Web UI is turned off.

const { app, BrowserWindow, ipcMain, shell, session, Menu, dialog, nativeImage } = require("electron")
const { spawn } = require("node:child_process")
const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")
const http = require("node:http")

const isWindows = process.platform === "win32"

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
if (process.platform === "linux") {
    // Smooth video and scrolling on Linux.
    app.commandLine.appendSwitch("enable-features", "VaapiVideoDecodeLinuxGL,VaapiVideoDecoder")
    app.commandLine.appendSwitch("ignore-gpu-blocklist")
}
if (isWindows && process.env.LOCALAPPDATA) {
    // Electron would keep the window's browser data (caches, the AniList
    // login window's cookies) in %APPDATA%\Kumo, which is the server's data
    // folder on Windows: keep it apart, with Kumo's other local files.
    app.setPath("userData", path.join(process.env.LOCALAPPDATA, "Kumo", "electron"))
}

const runtimeDir = serverRuntimeDir()
let serverProc = null
let mainWindow = null
let baseUrl = null

// Where the server writes server.json and the shell token: the same folder
// as its config.RuntimeDir().
function serverRuntimeDir() {
    if (isWindows) {
        const local = process.env.LOCALAPPDATA
        return local ? path.join(local, "Kumo", "run") : path.join(os.tmpdir(), "kumo")
    }
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
            // replacing Kumo, and starts it when it's done.
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
                dialog.showErrorBox("Kumo", "The Kumo server stopped unexpectedly. Restart Kumo to continue.")
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

function createWindow() {
    const iconPath = [path.join(__dirname, "icon.png"), "/usr/share/icons/hicolor/512x512/apps/kumo-anime.png"].find(p => fs.existsSync(p))
    mainWindow = new BrowserWindow({
        width: 1600,
        height: 1000,
        minWidth: 960,
        minHeight: 620,
        title: "Kumo",
        backgroundColor: "#0a0a0b",
        autoHideMenuBar: true,
        show: false,
        icon: iconPath ? nativeImage.createFromPath(iconPath) : undefined,
        webPreferences: {
            preload: path.join(__dirname, "preload.js"),
            contextIsolation: true,
            sandbox: true,
            nodeIntegration: false,
            spellcheck: false,
        },
    })
    Menu.setApplicationMenu(null)
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
    mainWindow.on("closed", () => (mainWindow = null))
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
    createWindow()
}

if (!app.requestSingleInstanceLock()) {
    app.quit()
} else {
    app.on("second-instance", () => {
        if (mainWindow) {
            if (mainWindow.isMinimized()) mainWindow.restore()
            mainWindow.focus()
        }
    })
    app.whenReady().then(boot)
    app.on("window-all-closed", () => app.quit())
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
