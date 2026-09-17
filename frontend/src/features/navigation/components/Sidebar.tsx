import { useEffect, useMemo, useRef, useState } from "react"
import type { MouseEvent as ReactMouseEvent } from "react"
import {
	BellOff,
	Check,
	Link2,
	MessageSquareText,
	MoreHorizontal,
	PencilLine,
	Pin,
	PinOff,
	Plus,
	Search,
	Settings,
	Trash2,
	X,
} from "lucide-react"
import { api } from "../../../shared/api/api"
import type { Thread, WorkspaceState } from "../../../shared/model/types"
import { cn, relTime } from "../../../shared/lib/utils"
import { ScrollArea } from "../../../shared/ui"
import WorkspaceMenu from "./WorkspaceMenu"

/**
 * Сайдбар по макету Notion:
 * — шапка: кнопка воркспейса растянута на всю ширину, справа компактные
 *   кнопки поиска и «+» (они не съезжают и остаются кнопками);
 * — список чатов, сгруппированный по датам (Сегодня / Вчера / …);
 * — у каждого чата меню «три точки»: ссылка, пин, непрочитано, переименовать,
 *   удалить, а справа — время последнего изменения;
 * — внизу «Новый чат Ctrl+O» и «Настройки».
 */

/** Границы ширины сайдбара при ручном перетаскивании. */
export const SIDEBAR_MIN = 220
export const SIDEBAR_MAX = 460
export const SIDEBAR_DEFAULT = 276

/** Название всегда в одну строку и обрезается по фактической ширине. */
function cleanTitle(raw: string) {
	return (raw || "Новый чат").replace(/\s+/g, " ").trim()
}

/**
 * Страховка поверх CSS-обрезки: длинное название без пробелов
 * или с длинной ссылкой всё равно раздвигало строку и вытесняло «три точки».
 * Запас в пикселях: иконка + время + кнопка меню.
 */
function clampTitle(raw: string, width: number) {
	const text = cleanTitle(raw)
	// Справа всегда зарезервированы время и кнопка «…», поэтому название
	// заранее короче и никогда не раздвигает строку при наведении.
	const maxChars = Math.max(8, Math.floor((width - 150) / 7.2))
	return text.length > maxChars ? `${text.slice(0, maxChars - 1).trimEnd()}\u2026` : text
}

function dayBucket(ts: number) {
	const now = new Date()
	const date = new Date(ts)
	const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
	const day = 86_400_000
	if (ts >= startOfToday) return "Сегодня"
	if (ts >= startOfToday - day) return "Вчера"
	if (ts >= startOfToday - 7 * day) return "Предыдущие 7 дней"
	if (ts >= startOfToday - 30 * day) return "Предыдущие 30 дней"
	return date.toLocaleDateString("ru-RU", { month: "long", year: "numeric" })
}

/** Пины и «непрочитано» — локальные метки, живут рядом с чатами. */
function useFlags(key: string) {
	const [ids, setIds] = useState<string[]>(() => {
		try {
			const raw = localStorage.getItem(key)
			return raw ? (JSON.parse(raw) as string[]) : []
		} catch {
			return []
		}
	})
	useEffect(() => {
		try {
			localStorage.setItem(key, JSON.stringify(ids))
		} catch {
			/* приватный режим — не критично */
		}
	}, [key, ids])
	const toggle = (id: string) =>
		setIds((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
	const clear = (id: string) => setIds((prev) => prev.filter((x) => x !== id))
	return { ids, toggle, clear }
}

function RowMenu({
	thread,
	pinned,
	unread,
	onRename,
	onDelete,
	onPin,
	onUnread,
	onCopyLink,
}: {
	thread: Thread
	pinned: boolean
	unread: boolean
	onRename: () => void
	onDelete: () => void
	onPin: () => void
	onUnread: () => void
	onCopyLink: () => void
}) {
	const [open, setOpen] = useState(false)
	const wrap = useRef<HTMLDivElement>(null)

	useEffect(() => {
		if (!open) return
		const onDown = (e: MouseEvent) => {
			if (!wrap.current?.contains(e.target as Node)) setOpen(false)
		}
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false)
		document.addEventListener("mousedown", onDown)
		document.addEventListener("keydown", onKey)
		return () => {
			document.removeEventListener("mousedown", onDown)
			document.removeEventListener("keydown", onKey)
		}
	}, [open])

	const items = [
		{ icon: Link2, label: "Копировать ссылку", run: onCopyLink },
		{ icon: pinned ? PinOff : Pin, label: pinned ? "Открепить" : "Закрепить", run: onPin },
		{ icon: BellOff, label: unread ? "Пометить прочитанным" : "Пометить непрочитанным", run: onUnread },
		{ icon: PencilLine, label: "Переименовать", run: onRename },
	]

	return (
		<div ref={wrap} className="relative size-6 shrink-0">
			<button
				type="button"
				aria-label="Действия с чатом"
				onClick={(e) => {
					e.stopPropagation()
					setOpen((v) => !v)
				}}
				className={cn(
					"text-muted-foreground hover:bg-accent hover:text-foreground grid size-6 place-items-center rounded transition-[opacity,color,background-color]",
					open
						? "bg-accent text-foreground opacity-100"
						: "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100",
				)}
			>
				<MoreHorizontal className="size-3.5" />
			</button>

			{open ? (
				<div
					onClick={(e) => e.stopPropagation()}
					className="bg-popover shadow-sheet animate-in fade-in-0 zoom-in-95 absolute end-0 top-7 z-50 w-[224px] overflow-hidden rounded-xl border p-1 duration-150"
				>
					{items.map(({ icon: Icon, label, run }) => (
						<button
							key={label}
							type="button"
							onClick={() => {
								setOpen(false)
								run()
							}}
							className="hover:bg-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[13px] transition-colors"
						>
							<Icon className="text-muted-foreground size-3.5 shrink-0" />
							<span className="min-w-0 flex-1 truncate">{label}</span>
						</button>
					))}
					<div className="bg-border my-1 h-px" />
					<button
						type="button"
						onClick={() => {
							setOpen(false)
							onDelete()
						}}
						className="text-destructive hover:bg-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[13px] transition-colors"
					>
						<Trash2 className="size-3.5 shrink-0" /> Удалить
					</button>
					<p className="text-muted-foreground px-2 pt-1.5 pb-1 text-[11px]">
						Изменён {relTime(thread.updatedAt)}
					</p>
				</div>
			) : null}
		</div>
	)
}

export default function Sidebar({
	threads,
	activeId,
	collapsed,
	width,
	onWidth,
	onResizing,
	onSelect,
	onNew,
	onDelete,
	onRename,
	onOpenSettings,
	onToast,
	onWorkspaceSwitched,
}: {
	threads: Thread[]
	activeId: string | null
	collapsed: boolean
	width: number
	onWidth: (w: number) => void
	/** Сообщаем наверх, что идёт ресайз — шапка окна тоже гасит анимацию. */
	onResizing?: (active: boolean) => void
	onSelect: (id: string) => void
	onNew: () => void
	onDelete: (id: string) => void
	onRename?: (id: string, title: string) => void
	onOpenSettings: () => void
	onToast?: (msg: string, error?: boolean) => void
	onWorkspaceSwitched?: () => void
}) {
	const [query, setQuery] = useState("")
	const [searchOpen, setSearchOpen] = useState(false)
	const [workspaces, setWorkspaces] = useState<WorkspaceState | null>(null)
	const [renaming, setRenaming] = useState<{ id: string; value: string } | null>(null)
	const [, setRelativeTimeTick] = useState(0)
	const [dragging, setDragging] = useState(false)
	const pins = useFlags("neura:pinnedThreads")
	const unreads = useFlags("neura:unreadThreads")

	const toast = onToast ?? (() => {})

	// Шапка должна сразу показывать активный воркспейс.
	useEffect(() => {
		void api
			.listWorkspaces()
			.then(setWorkspaces)
			.catch(() => setWorkspaces(null))
	}, [])

	// Относительное время обновляется само, даже когда список чатов не менялся.
	useEffect(() => {
		const timer = window.setInterval(() => setRelativeTimeTick((value) => value + 1), 30_000)
		return () => window.clearInterval(timer)
	}, [])

	// Ctrl+K — поиск по чатам.
	useEffect(() => {
		function onKey(e: KeyboardEvent) {
			if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
				e.preventDefault()
				setSearchOpen(true)
			}
		}
		window.addEventListener("keydown", onKey)
		return () => window.removeEventListener("keydown", onKey)
	}, [])

	const sorted = useMemo(
		() => [...threads].sort((a, b) => b.updatedAt - a.updatedAt),
		[threads],
	)

	const pinned = useMemo(
		() => sorted.filter((t) => pins.ids.includes(t.id)),
		[sorted, pins.ids],
	)

	const groups = useMemo(() => {
		const out: { label: string; items: Thread[] }[] = []
		if (pinned.length > 0) out.push({ label: "Закреплённые", items: pinned })
		for (const thread of sorted) {
			if (pins.ids.includes(thread.id)) continue
			const label = dayBucket(thread.updatedAt)
			const last = out[out.length - 1]
			if (last && last.label === label) last.items.push(thread)
			else out.push({ label, items: [thread] })
		}
		return out
	}, [sorted, pinned, pins.ids])

	// Ширину тянем мышью за правый край; двойной клик возвращает дефолт.
	function startDrag(e: ReactMouseEvent<HTMLDivElement>) {
		e.preventDefault()
		const startX = e.clientX
		const startW = width
		setDragging(true)
		onResizing?.(true)
		const onMove = (ev: MouseEvent) => {
			const next = Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, startW + (ev.clientX - startX)))
			onWidth(next)
		}
		const onUp = () => {
			setDragging(false)
			onResizing?.(false)
			document.removeEventListener("mousemove", onMove)
			document.removeEventListener("mouseup", onUp)
			document.body.style.cursor = ""
			document.body.style.userSelect = ""
		}
		document.body.style.cursor = "col-resize"
		document.body.style.userSelect = "none"
		document.addEventListener("mousemove", onMove)
		document.addEventListener("mouseup", onUp)
	}

	function commitRename() {
		if (!renaming) return
		const title = renaming.value.trim()
		const id = renaming.id
		setRenaming(null)
		if (!title) return
		onRename?.(id, title)
	}

	function row(t: Thread) {
		const isRenaming = renaming?.id === t.id
		return (
			<li key={t.id} className="min-w-0">
				<div
					role="button"
					tabIndex={0}
					onClick={() => {
						if (isRenaming) return
						unreads.clear(t.id)
						onSelect(t.id)
					}}
					onKeyDown={(e) => e.key === "Enter" && !isRenaming && onSelect(t.id)}
					className={cn(
						"group flex h-[30px] min-w-0 max-w-full cursor-pointer items-center gap-1.5 rounded-md px-1.5 transition-colors",
						"hover:bg-sidebar-accent active:bg-sidebar-accent/70",
						t.id === activeId && "bg-sidebar-accent",
					)}
				>
					{pins.ids.includes(t.id) ? (
						<Pin className="text-muted-foreground size-3.5 shrink-0" />
					) : (
						<MessageSquareText className="text-muted-foreground size-4 shrink-0" />
					)}

					{isRenaming ? (
						<input
							autoFocus
							value={renaming.value}
							onChange={(e) => setRenaming({ id: t.id, value: e.target.value })}
							onBlur={commitRename}
							onKeyDown={(e) => {
								if (e.key === "Enter") commitRename()
								if (e.key === "Escape") setRenaming(null)
							}}
							className="bg-background min-w-0 flex-1 rounded border px-1 text-[13px] outline-none"
						/>
					) : (
						<span
							title={t.title || "Новый чат"}
							className={cn(
								"text-foreground min-w-0 flex-1 truncate text-[14px] leading-5",
								unreads.ids.includes(t.id) && "font-semibold",
							)}
						>
							{clampTitle(t.title, width)}
						</span>
					)}

					{unreads.ids.includes(t.id) ? (
						<span className="bg-brand size-1.5 shrink-0 rounded-full" />
					) : null}

					{width >= 250 ? (
						<span className="text-muted-foreground w-[68px] shrink-0 truncate text-right text-[11px]">
							{relTime(t.updatedAt)}
						</span>
					) : null}

					<RowMenu
						thread={t}
						pinned={pins.ids.includes(t.id)}
						unread={unreads.ids.includes(t.id)}
						onRename={() => setRenaming({ id: t.id, value: t.title || "" })}
						onDelete={() => onDelete(t.id)}
						onPin={() => pins.toggle(t.id)}
						onUnread={() => unreads.toggle(t.id)}
						onCopyLink={() => {
							void navigator.clipboard
								.writeText(`neura://chat/${t.id}`)
								.then(() => toast("Ссылка на чат скопирована"))
								.catch(() => toast("Не удалось скопировать", true))
						}}
					/>
				</div>
			</li>
		)
	}

	return (
		<>
			<aside
				aria-hidden={collapsed}
				style={{ width: collapsed ? 0 : width }}
				className={cn(
					"bg-sidebar text-sidebar-foreground shrink-0 overflow-hidden",
					dragging
						? ""
						: "transition-[width] duration-200 ease-out motion-reduce:transition-none",
				)}
			>
				<div className="flex h-full flex-col" style={{ width }}>
					<div className="flex min-w-0 items-center gap-1 px-2 pt-1.5 pb-1">
						<WorkspaceMenu
							state={workspaces}
							onState={setWorkspaces}
							onOpenSettings={onOpenSettings}
							onToast={toast}
							onSwitched={() => onWorkspaceSwitched?.()}
						/>
						<button
							type="button"
							aria-label="Поиск по чатам"
							title="Поиск по чатам (Ctrl+K)"
							onClick={() => setSearchOpen(true)}
							className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground active:bg-sidebar-accent/70 grid size-7 shrink-0 place-items-center rounded-md transition-colors"
						>
							<Search className="size-4" />
						</button>
						<button
							type="button"
							aria-label="Новый чат"
							title="Новый чат (Ctrl+O)"
							onClick={onNew}
							className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground active:bg-sidebar-accent/70 grid size-7 shrink-0 place-items-center rounded-md transition-colors"
						>
							<Plus className="size-4" />
						</button>
					</div>

					<ScrollArea className="min-h-0 flex-1">
						<div className="min-w-0 px-2 pb-2">
							{groups.length === 0 ? (
								<p className="text-muted-foreground px-1.5 py-2 text-[13px]">Пока пусто</p>
							) : (
								groups.map((group) => (
									<div key={group.label} className="mb-1 min-w-0">
										<p className="text-muted-foreground px-1.5 pt-3 pb-1 text-[12px]">
											{group.label}
										</p>
										<ul className="flex min-w-0 flex-col">{group.items.map(row)}</ul>
									</div>
								))
							)}
						</div>
					</ScrollArea>

					<div className="flex flex-col gap-0.5 border-t px-2 py-2">
						<button
							type="button"
							onClick={onNew}
							className="hover:bg-sidebar-accent active:bg-sidebar-accent/70 flex h-8 w-full items-center gap-2 rounded-md px-1.5 text-[14px] transition-colors"
						>
							<Plus className="size-4 shrink-0" />
							<span className="min-w-0 flex-1 truncate text-left">Новый чат</span>
							<span className="text-muted-foreground shrink-0 text-[11px]">Ctrl+O</span>
						</button>
						<button
							type="button"
							onClick={onOpenSettings}
							className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground active:bg-sidebar-accent/70 flex h-8 w-full items-center gap-2 rounded-md px-1.5 text-[14px] transition-colors"
						>
							<Settings className="size-4 shrink-0" />
							<span className="min-w-0 flex-1 truncate text-left">Настройки</span>
						</button>
					</div>
				</div>
			</aside>

			{collapsed ? null : (
				<div
					role="separator"
					aria-orientation="vertical"
					title="Потяните, чтобы изменить ширину (двойной клик — сбросить)"
					onMouseDown={startDrag}
					onDoubleClick={() => onWidth(SIDEBAR_DEFAULT)}
					className="group relative z-20 w-[4px] shrink-0 cursor-col-resize"
				>
					<span
						className={cn(
							"absolute inset-y-0 left-0 w-[2px] transition-colors",
							dragging ? "bg-brand" : "bg-transparent group-hover:bg-brand/60",
						)}
					/>
				</div>
			)}

			{searchOpen ? (
				<ChatSearch
					threads={sorted}
					query={query}
					onQuery={setQuery}
					onClose={() => {
						setSearchOpen(false)
						setQuery("")
					}}
					onPick={(id) => {
						unreads.clear(id)
						onSelect(id)
						setSearchOpen(false)
						setQuery("")
					}}
				/>
			) : null}
		</>
	)
}

/** Поиск по чатам: ищет и в названиях, и внутри сообщений. */
function ChatSearch({
	threads,
	query,
	onQuery,
	onClose,
	onPick,
}: {
	threads: Thread[]
	query: string
	onQuery: (v: string) => void
	onClose: () => void
	onPick: (id: string) => void
}) {
	type Hit = { thread: Thread; snippet?: string }
	const [hits, setHits] = useState<Hit[]>(threads.slice(0, 8).map((thread) => ({ thread })))
	const [loading, setLoading] = useState(false)

	useEffect(() => {
		const q = query.trim().toLowerCase()
		if (!q) {
			setHits(threads.slice(0, 8).map((thread) => ({ thread })))
			return
		}
		let cancelled = false
		setLoading(true)
		const timer = window.setTimeout(() => {
			void (async () => {
				// Поиск идёт в SQLite на бэкенде: и по названиям, и по тексту сообщений.
				let found: Thread[] = []
				try {
					found = (await api.searchThreads(q)) ?? []
				} catch {
					found = threads.filter((t) => (t.title ?? "").toLowerCase().includes(q))
				}
				const out: Hit[] = []
				for (const thread of found.slice(0, 30)) {
					if ((thread.title ?? "").toLowerCase().includes(q)) {
						out.push({ thread })
						continue
					}
					try {
						const msgs = await api.loadThread(thread.id)
						const hit = (msgs ?? []).find((m) => m.content.toLowerCase().includes(q))
						if (hit) {
							const at = hit.content.toLowerCase().indexOf(q)
							const from = Math.max(0, at - 40)
							out.push({
								thread,
								snippet: `${from > 0 ? "…" : ""}${hit.content
									.slice(from, at + q.length + 60)
									.replace(/\s+/g, " ")}…`,
							})
						} else {
							out.push({ thread })
						}
					} catch {
						out.push({ thread })
					}
				}
				if (!cancelled) {
					setHits(out)
					setLoading(false)
				}
			})()
		}, 160)
		return () => {
			cancelled = true
			window.clearTimeout(timer)
		}
	}, [query, threads])

	return (
		<div
			className="animate-in fade-in-0 fixed inset-0 z-[60] flex items-start justify-center bg-black/40 pt-[12vh] duration-150"
			onClick={onClose}
		>
			<div
				className="bg-popover shadow-sheet animate-in zoom-in-95 slide-in-from-top-2 w-[560px] max-w-[92vw] overflow-hidden rounded-xl border duration-150"
				onClick={(e) => e.stopPropagation()}
			>
				<div className="flex items-center gap-2 border-b px-3">
					<Search className="text-muted-foreground size-4 shrink-0" />
					<input
						autoFocus
						value={query}
						onChange={(e) => onQuery(e.target.value)}
						onKeyDown={(e) => {
							if (e.key === "Escape") onClose()
							if (e.key === "Enter" && hits[0]) onPick(hits[0].thread.id)
						}}
						placeholder="Поиск по чатам и сообщениям…"
						className="h-11 min-w-0 flex-1 bg-transparent text-[14px] outline-none"
					/>
					{loading ? <span className="text-muted-foreground text-[11px]">ищу…</span> : null}
					<button
						type="button"
						aria-label="Закрыть поиск"
						onClick={onClose}
						className="text-muted-foreground hover:bg-accent hover:text-foreground grid size-7 shrink-0 place-items-center rounded-md transition-colors"
					>
						<X className="size-4" />
					</button>
				</div>
				<ul className="max-h-[52vh] overflow-y-auto p-1">
					{hits.length === 0 ? (
						<li className="text-muted-foreground px-3 py-4 text-[13px]">Ничего не найдено</li>
					) : (
						hits.map(({ thread, snippet }) => (
							<li key={thread.id}>
								<button
									type="button"
									onClick={() => onPick(thread.id)}
									className="hover:bg-sidebar-accent flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
								>
									{snippet ? (
										<Check className="text-muted-foreground mt-0.5 size-4 shrink-0 opacity-0" />
									) : (
										<MessageSquareText className="text-muted-foreground mt-0.5 size-4 shrink-0" />
									)}
									<span className="min-w-0 flex-1">
										<span className="block truncate text-[13px]">
											{thread.title || "Новый чат"}
										</span>
										{snippet ? (
											<span className="text-muted-foreground block truncate text-[12px]">
												{snippet}
											</span>
										) : null}
									</span>
									<span className="text-muted-foreground shrink-0 text-[11px]">
										{new Date(thread.updatedAt).toLocaleDateString("ru-RU")}
									</span>
								</button>
							</li>
						))
					)}
				</ul>
			</div>
		</div>
	)
}
