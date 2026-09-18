import { useEffect, useMemo, useRef, useState } from "react"
import { Code2, Copy, Download, ExternalLink, MoreHorizontal, RotateCw, X } from "lucide-react"
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
	const [menu, setMenu] = useState(false)
	const menuRef = useRef<HTMLDivElement>(null)
	const file = artifactFile(artifact)
	const doc = useMemo(() => previewHtml(artifact), [artifact])

	// При смене артефакта всегда начинаем с браузера для сайтов и картинок.
	useEffect(() => {
		setTab(artifact.kind === "web" ? "preview" : "code")
		setNonce((n) => n + 1)
	}, [artifact.id, artifact.kind])

	useEffect(() => {
		if (!menu) return
		function onDown(event: MouseEvent) {
			if (!menuRef.current?.contains(event.target as Node)) setMenu(false)
		}
		document.addEventListener("mousedown", onDown)
		return () => document.removeEventListener("mousedown", onDown)
	}, [menu])

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

				{/* Три точки: скачать файл, посмотреть исходник, открыть снаружи. */}
				<div className="relative" ref={menuRef}>
					<Button variant="subtle" size="icon-sm" onClick={() => setMenu((v) => !v)}>
						<MoreHorizontal className="size-3.5" />
					</Button>
					{menu ? (
						<div className="bg-popover absolute end-0 top-8 z-30 w-56 overflow-hidden rounded-lg border p-1 shadow-lg">
							{[
								{
									label: "Скачать файл",
									icon: <Download className="size-3.5" />,
									run: download,
								},
								{
									label: tab === "code" ? "Открыть браузер" : "Показать исходный код",
									icon: <Code2 className="size-3.5" />,
									run: () => setTab(tab === "code" ? "preview" : "code"),
								},
								{
									label: "Копировать код",
									icon: <Copy className="size-3.5" />,
									run: () => void navigator.clipboard.writeText(artifact.code),
								},
								{
									label: "Открыть в новом окне",
									icon: <ExternalLink className="size-3.5" />,
									run: openExternally,
								},
							].map((item) => (
								<button
									key={item.label}
									type="button"
									onClick={() => {
										setMenu(false)
										item.run()
									}}
									className="hover:bg-accent flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-start text-[12.5px]"
								>
									{item.icon}
									{item.label}
								</button>
							))}
						</div>
					) : null}
				</div>
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
