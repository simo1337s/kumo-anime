// electron-builder hook (beforePack in electron-builder.yml, Windows release
// only): stops the build when the Windows server hasn't been built. Without
// it electron-builder only warns, and makes an app that can't start.

const fs = require("node:fs")
const path = require("node:path")

exports.default = async function checkWindowsServer() {
    const server = path.join(__dirname, "..", "dist", "windows", "kumo.exe")
    if (!fs.existsSync(server)) {
        throw new Error(`${server} is missing: build the Windows server first (packaging/windows/build.ps1, or make windows on Linux)`)
    }
}
