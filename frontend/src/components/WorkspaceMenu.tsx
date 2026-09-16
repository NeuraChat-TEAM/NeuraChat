import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { Check, ChevronsUpDown, LogOut, Plus, Settings, UserPlus, Users } from "lucide-react"
import { api, errText } from "../lib/api"
import type { AIUsage, Space, WorkspaceState } from "../lib/types"
import { cn } from "../lib/utils"

/**
 * Попап аккаунтов и воркспейсов — как в Notion: сверху текущее
 * пространство с тарифом, ниже списки воркспейсов по аккаунтам,
 * в конце — создание воркспейса и подключение аккаунта.
 */
function initials(text: string) {
	const trimmed = text.trim()
	return trimmed ? trimmed.slice(0, 1).toUpperCase() : "?"
}

/** Дата сброса лимитов — до неё воркспейс остаётся красным. */
function resetLabel(ms?: number) {
	if (!ms) return ""
	return new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "long" })
}

function planLabel(plan: string) {
	switch (plan) {
		case "team":
			return "Business Trial"
		case "personal":
			return "Free plan"
		case "personal_pro":
			return "Plus plan"
		case "enterprise":
			return "Enterprise"
		default:
			return plan || "Workspace"
	}
}

function SpaceIcon({ space }: { space: Space }) {
	const isImage = space.icon?.startsWith("http")
	return isImage ? (
		<img src={space.icon} alt="" className="size-5 shrink-0 rounded" />
	) : (
		<span className="bg-sidebar-accent text-foreground grid size-5 shrink-0 place-items-center rounded text-[11px]">
			{space.icon && !space.icon.startsWith("/") ? space.icon : initials(space.name)}
		</span>
	)
}

export default function WorkspaceMenu({
	state,
	onState,
	onOpenSettings,
	onToast,
	onSwitched,
}: {
	state: WorkspaceState | null
	onState: (next: WorkspaceState) => void
	onOpenSettings: () => void
	onToast: (msg: string, error?: boolean) => void
	onSwitched: () => void
}) {
	const [open, setOpen] = useState(false)
	const [busy, setBusy] = useState(false)
	const [creating, setCreating] = useState(false)
	const [name, setName] = useState("")
	const wrap = useRef<HTMLDivElement>(null)
	const pop = useRef<HTMLDivElement>(null)
	const trigger = useRef<HTMLButtonElement>(null)
	// Попап живёт в portal на body: иначе его режет сайдбар с overflow-hidden.
	const [box, setBox] = useState<{ left: number; top: number; width: number; maxHeight: number } | null>(null)
	// Лимиты AI: если месячные кредиты выбраны — воркспейс светится красным
	// до даты сброса, а не до перезапуска приложения.
	const [usage, setUsage] = useState<AIUsage | null>(null)

	useEffect(() => {
		let alive = true
		const load = () => {
			api
				.aiUsage()
				.then((u) => {
					if (alive) setUsage(u)
				})
				.catch(() => {})
		}
		load()
		// Обновляем раз в 5 минут: чаще не нужно, Notion сам кэширует.
		const timer = window.setInterval(load, 5 * 60 * 1000)
		return () => {
			alive = false
			window.clearInterval(timer)
		}
	}, [state?.activeSpaceId])

	const limited =
		!!usage?.limitReached && (!usage.resetAtMs || usage.resetAtMs > Date.now())

	const place = useCallback(() => {
		const rect = trigger.current?.getBoundingClientRect()
		if (!rect) return
		const width = Math.max(300, Math.min(360, rect.width + 48))
		const left = Math.min(Math.max(8, rect.left), window.innerWidth - width - 8)
		const top = rect.bottom + 6
		setBox({ left, top, width, maxHeight: Math.max(240, window.innerHeight - top - 16) })
	}, [])

	useLayoutEffect(() => {
		if (!open) return
		place()
		window.addEventListener("resize", place)
		window.addEventListener("scroll", place, true)
		return () => {
			window.removeEventListener("resize", place)
			window.removeEventListener("scroll", place, true)
		}
	}, [open, place])

	// Закрытие по клику снаружи и по Escape — попап не должен залипать.
	useEffect(() => {
		if (!open) return
		function onDown(e: MouseEvent) {
			const target = e.target as Node
			if (wrap.current?.contains(target) || pop.current?.contains(target)) return
			setOpen(false)
		}
		function onKey(e: KeyboardEvent) {
			if (e.key === "Escape") setOpen(false)
		}
		document.addEventListener("mousedown", onDown)
		document.addEventListener("keydown", onKey)
		return () => {
			document.removeEventListener("mousedown", onDown)
			document.removeEventListener("keydown", onKey)
		}
	}, [open])

	async function refresh() {
		try {
			onState(await api.listWorkspaces())
		} catch (e) {
			onToast(errText(e), true)
		}
	}

	async function pick(userId: string, space: Space) {
		if (busy) return
		setBusy(true)
		try {
			onState(await api.switchWorkspace(userId, space.id, space.spaceViewId, space.name))
			onSwitched()
			setOpen(false)
		} catch (e) {
			onToast(errText(e), true)
		} finally {
			setBusy(false)
		}
	}

	async function create() {
		const title = name.trim()
		if (!title || busy) return
		setBusy(true)
		try {
			onState(await api.createWorkspace(title))
			onSwitched()
			setName("")
			setCreating(false)
			setOpen(false)
			onToast(`Воркспейс «${title}» создан`)
		} catch (e) {
			onToast(errText(e), true)
		} finally {
			setBusy(false)
		}
	}

	const title = state?.activeName || "Нет подключения"

	return (
		<div ref={wrap} className="relative min-w-0 flex-1">
			<button
				type="button"
				ref={trigger}
				title={title}
				aria-expanded={open}
				onClick={() => {
					setOpen((v) => !v)
					if (!open) void refresh()
				}}
				className={cn(
					"hover:bg-sidebar-accent active:bg-sidebar-accent/80 flex h-8 w-full min-w-0 cursor-pointer items-center gap-2 rounded-md px-1.5 text-left transition-colors",
					open && "bg-sidebar-accent",
					limited && "bg-red-500/10 hover:bg-red-500/15",
				)}
			>
				<span
					className={cn(
						"bg-sidebar-accent text-foreground grid size-5 shrink-0 place-items-center rounded text-[11px]",
						limited && "bg-red-500/20 text-red-500",
					)}
				>
					{initials(title)}
				</span>
				<span
					className={cn(
						"text-foreground min-w-0 flex-1 truncate text-[14px] font-medium",
						limited && "text-red-500",
					)}
				>
					{title}
				</span>
				{limited ? (
					<span
						title={`Лимит AI исчерпан${usage?.resetAtMs ? ` — сброс ${resetLabel(usage.resetAtMs)}` : ""}`}
						className="shrink-0 rounded bg-red-500/15 px-1 text-[10px] font-medium text-red-500"
					>
						лимит
					</span>
				) : null}
				<ChevronsUpDown className="text-muted-foreground size-3.5 shrink-0" />
			</button>

			{open && box
				? createPortal(
				<div
					ref={pop}
					style={{
						left: box.left,
						top: box.top,
						width: box.width,
						maxHeight: box.maxHeight,
					}}
					className="bg-popover shadow-sheet animate-in fade-in-0 zoom-in-95 slide-in-from-top-1 fixed z-[80] flex origin-top flex-col overflow-hidden rounded-xl border duration-150"
				>
					<div className="flex items-start gap-2 border-b px-3 py-2.5">
						<span className="bg-sidebar-accent grid size-8 shrink-0 place-items-center rounded-md text-[13px]">
							{initials(title)}
						</span>
						<div className="min-w-0 flex-1">
							<p className="truncate text-[14px] font-medium">{title}</p>
							<p className="text-muted-foreground truncate text-[12px]">
								{planLabel(state?.activePlan ?? "")}
							</p>
							{limited ? (
								<p className="mt-0.5 text-[11px] text-red-500">
									Лимит AI исчерпан
									{usage?.resetAtMs ? ` · сброс ${resetLabel(usage.resetAtMs)}` : ""}
								</p>
							) : null}
						</div>
					</div>

					<div className="border-b p-1">
						<button
							type="button"
							onClick={() => {
								setOpen(false)
								onOpenSettings()
							}}
							className="hover:bg-sidebar-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-[13px]"
						>
							<Settings className="size-4" /> Настройки
						</button>
						<button
							type="button"
							onClick={() =>
								void api
									.openURL("https://" + "www.notion.so" + "/settings/members")
									.catch((e) => onToast(errText(e), true))
							}
							className="hover:bg-sidebar-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-[13px]"
						>
							<Users className="size-4" /> Участники воркспейса
						</button>
					</div>

					<div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
						{(state?.accounts ?? []).map((account) => (
							<div key={account.userId} className="pb-1">
								<p className="text-muted-foreground truncate px-2 pt-2 pb-1 text-[11px] tracking-wide uppercase">
									{account.email || account.name || account.userId}
								</p>
								{account.spaces.map((space) => (
									<button
										key={space.id}
										type="button"
										disabled={busy}
										onClick={() => void pick(account.userId, space)}
										className={cn(
											"hover:bg-sidebar-accent flex h-9 w-full items-center gap-2 rounded-md px-2 text-left",
											busy && "opacity-60",
										)}
									>
										<SpaceIcon space={space} />
										<span className="min-w-0 flex-1 truncate text-[13px]">{space.name}</span>
										{space.isGuest ? (
											<span className="text-muted-foreground border-border rounded border px-1 text-[10px]">
												Guest
											</span>
										) : null}
										{space.id === state?.activeSpaceId &&
										account.userId === state?.activeUserId ? (
											<Check className="size-4 shrink-0" />
										) : null}
									</button>
								))}
							</div>
						))}
						{(state?.accounts ?? []).length === 0 ? (
							<p className="text-muted-foreground px-2 py-3 text-[13px]">
								Подключите Notion в настройках, чтобы увидеть воркспейсы.
							</p>
						) : null}
					</div>

					<div className="border-t p-1">
						{creating ? (
							<div className="flex items-center gap-1 p-1">
								<input
									autoFocus
									value={name}
									onChange={(e) => setName(e.target.value)}
									onKeyDown={(e) => {
										if (e.key === "Enter") void create()
										if (e.key === "Escape") setCreating(false)
									}}
									placeholder="Название воркспейса"
									className="bg-background h-8 min-w-0 flex-1 rounded-md border px-2 text-[13px] outline-none"
								/>
								<button
									type="button"
									disabled={busy || !name.trim()}
									onClick={() => void create()}
									className="bg-primary text-primary-foreground h-8 shrink-0 rounded-md px-2 text-[13px] disabled:opacity-50"
								>
									Создать
								</button>
							</div>
						) : (
							<button
								type="button"
								onClick={() => setCreating(true)}
								className="hover:bg-sidebar-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-[13px]"
							>
								<Plus className="size-4" /> Новый воркспейс
							</button>
						)}
						<button
							type="button"
							onClick={() => {
								setOpen(false)
								onOpenSettings()
								onToast("Добавьте аккаунт: Настройки → Подключение")
							}}
							className="hover:bg-sidebar-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-[13px]"
						>
							<UserPlus className="size-4" /> Добавить аккаунт
						</button>
						<button
							type="button"
							onClick={() => {
								setOpen(false)
								void api
									.clearSession()
									.then(() => {
										onToast("Сессия очищена")
										onSwitched()
									})
									.catch((e) => onToast(errText(e), true))
							}}
							className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground flex h-8 w-full items-center gap-2 rounded-md px-2 text-[13px]"
						>
							<LogOut className="size-4" /> Выйти из всех аккаунтов
						</button>
					</div>
				</div>,
						document.body,
				  )
				: null}
		</div>
	)
}
