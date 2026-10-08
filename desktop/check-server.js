// electron-builder hook (beforePack in electron-builder.yml): stops the
// build when the server for the app being packaged hasn't been built.
// Without it electron-builder only warns, and makes an app that can't start.

const fs = require("node:fs")
const path = require("node:path")

const dist = path.join(__dirname, "..", "dist")

// electron-builder's architecture numbers (builder-util's Arch), and the
// servers each needs: the universal app is packed for x64 and for arm64,
// then merged.
const ARCHS = { 1: ["x64"], 3: ["arm64"], 4: ["x64", "arm64"] }

exports.default = async function checkServer(context) {
    const platform = context.electronPlatformName
    if (platform === "win32") {
        const server = path.join(dist, "windows", "kumo.exe")
        if (!fs.existsSync(server)) {
            throw new Error(`${server} is missing: build the Windows server first (packaging/windows/build.ps1, or make windows on Linux)`)
        }
    } else if (platform === "darwin") {
        for (const arch of ARCHS[context.arch] ?? [String(context.arch)]) {
            const server = path.join(dist, "macos", `kumo-${arch}`)
            if (!fs.existsSync(server)) {
                throw new Error(`${server} is missing: build the macOS servers first (make macos)`)
            }
        }
    }
}
