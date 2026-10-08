// Exposes a tiny, safe bridge to the web UI.
const { contextBridge, ipcRenderer } = require("electron")

contextBridge.exposeInMainWorld("kumoDesktop", {
    platform: process.platform,
    anilistLogin: url => ipcRenderer.invoke("kumo:anilist-login", url),
    openExternal: url => ipcRenderer.send("kumo:open-external", url),
    // The desktop app's own settings (Settings › App › Desktop app).
    getPrefs: () => ipcRenderer.invoke("kumo:get-prefs"),
    setPref: (name, value) => ipcRenderer.invoke("kumo:set-pref", name, value),
})
