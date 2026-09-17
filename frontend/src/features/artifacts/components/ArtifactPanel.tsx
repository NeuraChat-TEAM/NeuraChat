import { useMemo, useState } from "react"
import { Copy, Download, ExternalLink, RotateCw, X } from "lucide-react"
import type { Artifact } from "../model/artifacts"
import { artifactFile, previewHtml } from "../model/artifacts"
import { cn } from "../../../shared/lib/utils"
import { Button, ScrollArea, Tooltip } from "../../../shared/ui"

/**
 * Правая панель-браузер для артефактов: слева чат, справа «окно» со
 * адресной строкой, перезагрузкой и переключателем Превью / Код.
 */
export default function ArtifactPanel({
	artifact,
	onClose,
	onToast,
}: {
	artifact: Artifact
	onClose: () => void
	onToast?: (msg: string, error?: boolean) => void
}) {
	const [tab, setTab] = useState<"preview" | "code">(
		artifact.kind === "web" ? "preview" : "code",
	)
	const [nonce, setNonce] = useState(0)
	const file = artifactFile(artifact)
	const doc = useMemo(() => previewHtml(artifact), [artifact])

	function download() {
		try {
			const bytes = artifact.dataBase64 ? Uint8Array.from(atob(artifact.dataBase64), (c) => c.charCodeAt(0)) : artifact.code
			const blob = new Blob([bytes], { type: artifact.mime || "text/plain;charset=utf-8" })
			const url = URL.createObjectURL(blob)
			const a = document.createElement("a")
			a.href = url
			a.download = file
			a.click()
			setTimeout(() => URL.revokeObjectURL(url), 1000)
		} catch {
			onToast?.("Не удалось скачать файл", true)
		}
	}

	function openExternally() {
		try {
			const blob = new Blob([doc], { type: "text/html;charset=utf-8" })
			window.open(URL.createObjectURL(blob), "_blank")
		} catch {
			onToast?.("Не удалось открыть во внешнем окне", true)
		}
	}

	return (
		<aside className="artifact-panel bg-background flex w-[min(52%,620px)] min-w-[360px] shrink-0 flex-col border-s">
			{/* Адресная строка */}
			<div className="flex items-center gap-1.5 border-b px-2 py-1.5">
				<Tooltip label="Перезагрузить">
					<Button variant="subtle" size="icon-sm" onClick={() => setNonce((n) => n + 1)}>
						<RotateCw className="size-3.5" />
					</Button>
				</Tooltip>

				<div className="bg-muted text-muted-foreground flex h-7 min-w-0 flex-1 items-center rounded-full px-3 text-[12px]">
					<span className="truncate">artifact://{file}</span>
				</div>

				<div className="bg-muted flex items-center rounded-md p-0.5">
					{(["preview", "code"] as const).map((t) => (
						<button
							key={t}
							type="button"
							onClick={() => setTab(t)}
							className={cn(
								"rounded-[5px] px-2 py-1 text-[12px] font-medium transition-colors",
								tab === t
									? "bg-background text-foreground shadow-xs"
									: "text-muted-foreground hover:text-foreground",
							)}
						>
							{t === "preview" ? "Превью" : "Код"}
						</button>
					))}
				</div>

				<Tooltip label="Копировать код">
					<Button
						variant="subtle"
						size="icon-sm"
						onClick={() => void navigator.clipboard.writeText(artifact.code)}
					>
						<Copy className="size-3.5" />
					</Button>
				</Tooltip>
				<Tooltip label="Скачать">
					<Button variant="subtle" size="icon-sm" onClick={download}>
						<Download className="size-3.5" />
					</Button>
				</Tooltip>
				<Tooltip label="Открыть в новом окне">
					<Button variant="subtle" size="icon-sm" onClick={openExternally}>
						<ExternalLink className="size-3.5" />
					</Button>
				</Tooltip>
				<Tooltip label="Закрыть">
					<Button variant="subtle" size="icon-sm" onClick={onClose}>
						<X className="size-3.5" />
					</Button>
				</Tooltip>
			</div>

			{tab === "preview" ? (
				<iframe
					key={nonce}
					title={file}
					srcDoc={doc}
					sandbox="allow-scripts allow-forms allow-modals allow-popups"
					className="min-h-0 flex-1 bg-white"
				/>
			) : (
				<ScrollArea className="min-h-0 flex-1">
					<pre className="px-4 py-3 font-mono text-[12.5px] leading-5 whitespace-pre">
						{artifact.code}
					</pre>
				</ScrollArea>
			)}
		</aside>
	)
}
