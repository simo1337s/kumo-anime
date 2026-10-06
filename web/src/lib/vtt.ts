// Minimal WebVTT parser so subtitle cues can be time-shifted (needed when a
// transcoded stream starts at an offset).

export type Cue = { start: number; end: number; text: string }

function ts(s: string) {
    const parts = s.trim().replace(",", ".").split(":")
    let sec = 0
    for (const p of parts) sec = sec * 60 + parseFloat(p)
    return sec
}

export function parseVTT(text: string): Cue[] {
    const cues: Cue[] = []
    const blocks = text.replace(/\r/g, "").split(/\n\n+/)
    for (const block of blocks) {
        const lines = block.split("\n")
        const idx = lines.findIndex(l => l.includes("-->"))
        if (idx === -1) continue
        const [a, b] = lines[idx].split("-->")
        const start = ts(a)
        const end = ts(b.trim().split(/\s+/)[0])
        const body = lines.slice(idx + 1).join("\n").trim()
        if (!body || !isFinite(start) || !isFinite(end)) continue
        cues.push({ start, end, text: body })
    }
    return cues.sort((x, y) => x.start - y.start)
}
