import { useSearchParams } from "react-router-dom"
import { WebviewFrame } from "@/components/plugins/PluginSlot"
import { EmptyState } from "@/components/ui"
import { pluginStore, useStore } from "@/lib/store"

// Full-page plugin webviews (slot "screen").
export default function WebviewPage() {
    const [params] = useSearchParams()
    const plugins = useStore(pluginStore)
    const pluginId = params.get("plugin") ?? ""
    const id = params.get("id") ?? ""
    const view = plugins[pluginId]?.state?.webviews?.find(w => w.id === id)
    if (!view) return <div className="p-10"><EmptyState title="This plugin page is not available" /></div>
    return (
        <div className="h-full p-6">
            <WebviewFrame pluginId={pluginId} id={id} html={view.html} options={{ ...view.options, height: "calc(100vh - 3rem)" }} className="h-full" />
        </div>
    )
}
