import { useEffect, useMemo, useRef, useState } from "react"
import {
	ArrowLeft,
	Bug,
	Check,
	ClipboardPaste,
	Copy,
	Cpu,
	ScrollText,
	KeyRound,
	LayoutGrid,
	LifeBuoy,
	Palette,
	Plug,
	Plus,
	RefreshCw,
	Search,
	Server,
	Sparkles,
	Store,
	Terminal,
	Trash2,
} from "lucide-react"
import { api, errText } from "../lib/api"
import type { MarketplaceServer } from "../lib/api"
import type {
	AIUsage,
	ConnectionState,
	DebugEntry,
	McpModule,
	Model,
	NotcodeLogLine,
	NotcodeStatus,
	Settings,
	UsageWindow,
	WorkspaceState,
} from "../lib/types"

/** Круглый прогресс-бар расхода с процентами — как в Notion AI Usage. */
function UsageBar({
	title,
	hint,
	used,
	limit,
}: {
	title: string
	hint?: string
	used: number
	limit: number
}) {
	const percent = limit > 0 ? Math.min(100, (used / limit) * 100) : 0
	const full = limit > 0 && used >= limit
	const near = percent >= 80
	const fmt = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(2))
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<div className="flex min-w-0 items-baseline justify-between gap-2">
				<span className="truncate text-[13px] font-medium">{title}</span>
				<span
					className={cn(
						"shrink-0 text-[13px] tabular-nums",
						full ? "text-red-500" : near ? "text-amber-500" : "text-muted-foreground",
					)}
				>
					{percent.toFixed(percent < 10 ? 1 : 0)}%
				</span>
			</div>
			<div className="bg-muted h-2 w-full overflow-hidden rounded-full">
				<div
					style={{ width: `${Math.max(percent, percent > 0 ? 2 : 0)}%` }}
					className={cn(
						"h-full rounded-full transition-[width] duration-500 ease-out",
						full ? "bg-red-500" : near ? "bg-amber-500" : "bg-[var(--blue-accent)]",
					)}
				/>
			</div>
			<p className="text-muted-foreground text-[11px] break-words">
				{fmt(used)} из {fmt(limit)}
				{hint ? ` · ${hint}` : ""}
			</p>
		</div>
	)
}

/**
 * Время строки лога. Go отдаёт RFC3339, но старые сборки присылали "15:04:05" —
 * тогда new Date(...) давал "Invalid Date", поэтому такие значения показываем как есть.
 */
function logTime(value?: string) {
	const raw = (value ?? "").trim()
	if (!raw) return "--:--:--"
	const parsed = new Date(raw)
	if (Number.isNaN(parsed.getTime())) return raw
	return parsed.toLocaleTimeString()
}

/** Описание окна лимита для подписи под баром. */
function windowHint(w?: UsageWindow) {
	if (!w) return ""
	const parts = [w.creditType?.replace(/_/g, " "), w.scope, w.window ?? w.cadence]
	return parts.filter(Boolean).join(" · ")
}

/** Статус-карточка процесса (NotCode / ngrok). */
function StatusRow({
	title,
	ok,
	lines,
}: {
	title: string
	ok: boolean
	lines: Array<[string, string]>
}) {
	return (
		<div className="min-w-0 rounded-lg border p-3">
			<div className="flex min-w-0 items-center gap-2">
				<span
					className={cn(
						"size-2.5 shrink-0 rounded-full",
						ok ? "bg-emerald-500" : "bg-muted-foreground",
					)}
				/>
				<span className="min-w-0 flex-1 truncate text-[14px] font-medium">{title}</span>
				<Badge tone={ok ? "ok" : "muted"}>{ok ? "запущен" : "остановлен"}</Badge>
			</div>
			<dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
				{lines.map(([key, value]) => (
					<div key={key} className="contents">
						<dt className="text-muted-foreground text-[11px] uppercase">{key}</dt>
						<dd className="min-w-0 font-mono text-[11px] break-all">{value || "—"}</dd>
					</div>
				))}
			</dl>
		</div>
	)
}
import { cn } from "../lib/utils"
import {
	Badge,
	Button,
	Card,
	Field,
	Input,
	Label,
	ScrollArea,
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
	Separator,
	Slider,
	Switch,
	Textarea,
} from "./ui"

/**
 * Настройки — страница внутри контентной области (тайтлбар с кнопками
 * окна всегда виден). Навигация: группы + быстрый фильтр разделов.
 * Технические поля (папка notcode, команда запуска, порт, ngrok,
 * эндпоинты) убраны из интерфейса — всё работает автоматически.
 */
const SECTIONS = [
	{ key: "connection", label: "Аккаунты", group: "Аккаунт", icon: KeyRound },
	{ key: "workspaces", label: "Воркспейсы", group: "Аккаунт", icon: LayoutGrid },
	{ key: "mcp", label: "Подключённые инструменты", group: "Инструменты", icon: Plug },
	{ key: "servers", label: "Свои серверы", group: "Инструменты", icon: Server },
	{ key: "marketplace", label: "Маркетплейс", group: "Инструменты", icon: Store },
	{ key: "chat", label: "Поведение ответов", group: "Чат", icon: Sparkles },
	{ key: "usage", label: "Лимиты и расход", group: "Аккаунт", icon: Cpu },
	{ key: "prompt", label: "Промпт", group: "Чат", icon: Terminal },
	{ key: "appearance", label: "Внешний вид", group: "Приложение", icon: Palette },
	{ key: "logs", label: "Логи", group: "Приложение", icon: ScrollText },
	{ key: "advanced", label: "Дополнительно", group: "Приложение", icon: LifeBuoy },
	{ key: "debug", label: "Отладка", group: "Приложение", icon: Bug },
] as const

/**
 * Маркетплейс: популярные публичные MCP-серверы в один клик.
 * Те, что требуют токен, после установки настраиваются в «Свои серверы».
 */
const FALLBACK_MARKETPLACE = [
	{
		name: "Context7",
		icon: "📚",
		url: "https://mcp.context7.com/mcp",
		about: "Документация библиотек и фреймворков актуальных версий.",
	},
	{
		name: "DeepWiki",
		icon: "🔍",
		url: "https://mcp.deepwiki.com/mcp",
		about: "Вопросы по любому публичному репозиторию GitHub.",
	},
	{
		name: "GitHub",
		icon: "🐙",
		url: "https://api.githubcopilot.com/mcp/",
		about: "Иссью, PR и код в ваших репозиториях (нужен токен).",
	},
	{
		name: "Sentry",
		icon: "🚨",
		url: "https://mcp.sentry.dev/mcp",
		about: "Ошибки и трейсы из Sentry прямо в чате.",
	},
	{
		name: "Hugging Face",
		icon: "🤗",
		url: "https://huggingface.co/mcp",
		about: "Модели, датасеты и Spaces с Hugging Face.",
	},
] as const

export default function SettingsModal({
	open,
	onOpenChange,
	navWidth = 276,
	settings,
	models,
	connection,
	onSaved,
	onConnectionChange,
	onToast,
}: {
	open: boolean
	onOpenChange: (open: boolean) => void
	/** Навигация настроек повторяет ширину сайдбара чата. */
	navWidth?: number
	settings: Settings
	models: Model[]
	connection: ConnectionState | null
	onSaved: (s: Settings) => void
	onConnectionChange: (c: ConnectionState) => void
	onToast: (msg: string, error?: boolean) => void
}) {
	const [tab, setTab] = useState<string>("connection")
	const [navQuery, setNavQuery] = useState("")
	const [draft, setDraft] = useState<Settings>(settings)
	const [saving, setSaving] = useState(false)
	const [busy, setBusy] = useState("")

	const [curl, setCurl] = useState("")
	const [modules, setModules] = useState<McpModule[]>([])
	const [status, setStatus] = useState<NotcodeStatus | null>(null)
	const [newName, setNewName] = useState("")
	const [newUrl, setNewUrl] = useState("")
	const [newToken, setNewToken] = useState("")
	const [workspaces, setWorkspaces] = useState<WorkspaceState | null>(null)
	const [newSpace, setNewSpace] = useState("")
	// Маркетплейс: живой каталог registry.modelcontextprotocol.io.
	const [marketQuery, setMarketQuery] = useState("")
	const [marketServers, setMarketServers] = useState<MarketplaceServer[] | null>(null)
	const [marketCursor, setMarketCursor] = useState("")
	const [marketError, setMarketError] = useState("")
	const [logs, setLogs] = useState<DebugEntry[]>([])
	// Раздельные логи NotCode / ngrok / приложения и расход лимитов.
	const [logLines, setLogLines] = useState<NotcodeLogLine[]>([])
	const [logSource, setLogSource] = useState<"all" | "notcode" | "ngrok" | "app">("all")
	const [usage, setUsage] = useState<AIUsage | null>(null)
	const [usageError, setUsageError] = useState("")
	const [openLog, setOpenLog] = useState<string | null>(null)
	const curlRef = useRef<HTMLTextAreaElement>(null)

	useEffect(() => {
		if (open) setDraft(settings)
	}, [open, settings])

	// Esc закрывает страницу, но только если фокус не в поле ввода.
	useEffect(() => {
		if (!open) return
		const onKey = (e: KeyboardEvent) => {
			if (e.key !== "Escape") return
			const t = e.target as HTMLElement | null
			if (t && /input|textarea/i.test(t.tagName)) return
			onOpenChange(false)
		}
		window.addEventListener("keydown", onKey)
		return () => window.removeEventListener("keydown", onKey)
	}, [open, onOpenChange])

	// cURL растёт под текст, чтобы вставленная строка не вылезала.
	useEffect(() => {
		const el = curlRef.current
		if (!el) return
		el.style.height = "auto"
		el.style.height = `${Math.min(420, Math.max(160, el.scrollHeight))}px`
	}, [curl, tab])

	const set = <K extends keyof Settings>(k: K, v: Settings[K]) =>
		setDraft((d) => ({ ...d, [k]: v }))

	useEffect(() => {
		if (!open) return
		if (tab === "mcp" || tab === "servers") void refreshMcp()
		if (tab === "workspaces") {
			api
				.listWorkspaces()
				.then(setWorkspaces)
				.catch(() => {})
		}
		if (tab === "debug") api.debugLogs().then(setLogs).catch(() => {})
		if (tab === "logs") {
			void refreshLogs()
			// Живой хвост лога: обновляем каждые 2 с, пока раздел открыт.
			const timer = window.setInterval(() => void refreshLogs(), 2000)
			return () => window.clearInterval(timer)
		}
		if (tab === "usage") void refreshUsage()
		if (tab === "marketplace") {
			void refreshMcp()
			if (marketServers === null) void loadMarket(true)
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [tab, open])

	const nav = useMemo(() => {
		const q = navQuery.trim().toLowerCase()
		const items = q ? SECTIONS.filter((s) => s.label.toLowerCase().includes(q)) : SECTIONS
		const groups: { group: string; items: (typeof SECTIONS)[number][] }[] = []
		for (const item of items) {
			const last = groups[groups.length - 1]
			if (last && last.group === item.group) last.items.push(item)
			else groups.push({ group: item.group, items: [item] })
		}
		return groups
	}, [navQuery])

	// Загрузка каталога: reset=true — новый поиск, иначе «показать ещё».
	async function loadMarket(reset: boolean) {
		setBusy("market")
		setMarketError("")
		try {
			const page = await api.marketplaceList(marketQuery.trim(), reset ? "" : marketCursor, true)
			setMarketServers((prev) =>
				reset || !prev ? (page.servers ?? []) : [...prev, ...(page.servers ?? [])],
			)
			setMarketCursor(page.nextCursor ?? "")
		} catch (e) {
			setMarketError(errText(e))
			if (marketServers === null) setMarketServers([])
		} finally {
			setBusy("")
		}
	}

	// Карточки каталога; если реестр недоступен — показываем проверенные пресеты.
	const marketItems = useMemo(() => {
		const live = (marketServers ?? [])
			.filter((s: MarketplaceServer) => (s.serverUrl ?? "") !== "")
			.map((s: MarketplaceServer) => ({
				name: s.title?.trim() || s.name.split("/").pop() || s.name,
				icon: "🔌",
				url: s.serverUrl as string,
				about: s.description?.trim() || s.name,
			}))
		if (live.length > 0) return live
		return FALLBACK_MARKETPLACE.map((item) => ({ ...item }))
	}, [marketServers])

	async function refreshLogs() {
		try {
			const [lines, st] = await Promise.all([api.notcodeLogs(), api.notcodeStatus()])
			setLogLines(lines ?? [])
			setStatus(st)
		} catch {
			// логи не критичны — молча пробуем снова
		}
	}

	async function refreshUsage() {
		setUsageError("")
		try {
			setUsage(await api.aiUsage())
		} catch (e) {
			setUsageError(errText(e))
		}
	}

	async function refreshMcp() {
		try {
			const [mods, st] = await Promise.all([
				api.mcpList().catch(() => [] as McpModule[]),
				api.notcodeStatus(),
			])
			setModules(mods ?? [])
			setStatus(st)
		} catch (e) {
			onToast(errText(e), true)
		}
	}

	async function run(key: string, fn: () => Promise<void>) {
		setBusy(key)
		try {
			await fn()
		} catch (e) {
			onToast(errText(e), true)
		} finally {
			setBusy("")
		}
	}

	async function save() {
		setSaving(true)
		try {
			onSaved(await api.saveSettings(draft))
			onToast("Настройки сохранены")
		} catch (e) {
			onToast(errText(e), true)
		} finally {
			setSaving(false)
		}
	}

	if (!open) return null

	const activeLabel = SECTIONS.find((s) => s.key === tab)?.label ?? "Настройки"

	return (
		// Навигация — на всю высоту, а кнопка «Сохранить» живёт только под
		// контентом, иначе сайдбар выглядел короче и «подпрыгивал».
		<div className="bg-background animate-in fade-in-0 slide-in-from-bottom-2 absolute inset-0 z-40 flex min-h-0 min-w-0 duration-200 ease-out">
			<nav
				style={{ width: navWidth }}
				className="bg-sidebar flex h-full shrink-0 flex-col p-2"
			>
					<div className="relative mb-1">
						<Search className="text-muted-foreground pointer-events-none absolute top-2 left-2 size-3.5" />
						<Input
							value={navQuery}
							onChange={(e) => setNavQuery(e.target.value)}
							placeholder="Найти раздел"
							className="h-7 pl-7 text-[13px]"
						/>
					</div>
					{nav.map((group) => (
						<div key={group.group} className="flex flex-col">
							<p className="text-muted-foreground px-2 pt-2 pb-1 text-[11px] tracking-wide uppercase">
								{group.group}
							</p>
							{group.items.map((s) => (
								<button
									key={s.key}
									type="button"
									onClick={() => setTab(s.key)}
									className={cn(
										"hover:bg-sidebar-accent flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[14px] transition-colors",
										tab === s.key ? "bg-sidebar-accent text-foreground" : "text-muted-foreground",
									)}
								>
									<s.icon className="size-4 shrink-0" />
									<span className="min-w-0 flex-1 truncate">{s.label}</span>
								</button>
							))}
						</div>
					))}
					<div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
						{nav.length === 0 ? (
							<p className="text-muted-foreground px-2 py-2 text-[13px]">Ничего не найдено</p>
						) : null}
					</div>

					{/* Возврат в чат — внизу слева, как продолжение сайдбара. */}
					<div className="mt-auto border-t pt-2">
						<button
							type="button"
							onClick={() => onOpenChange(false)}
							className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground active:bg-sidebar-accent/70 flex h-8 w-full items-center gap-2 rounded-md px-1.5 text-[14px] transition-colors"
						>
							<ArrowLeft className="size-4 shrink-0" />
							<span className="min-w-0 flex-1 truncate text-left">Вернуться в чат</span>
							<span className="text-muted-foreground shrink-0 text-[11px]">Esc</span>
						</button>
					</div>
			</nav>

			<div className="flex min-h-0 min-w-0 flex-1 flex-col">
				<ScrollArea className="min-h-0 min-w-0 flex-1">
					<div
						key={tab}
						className="animate-in fade-in-0 slide-in-from-right-1 mx-auto flex w-full max-w-[720px] min-w-0 flex-col gap-4 p-5 duration-200 ease-out"
					>
						<div className="flex min-w-0 items-center justify-between gap-3">
							<h2 className="min-w-0 truncate text-[20px] leading-7 font-semibold">{activeLabel}</h2>
						</div>
						{/* -------------------------------------------------- connection */}
						{tab === "connection" ? (
							<>
								<Card>
									<div className="flex min-w-0 items-start gap-3">
										<span
											className={cn(
												"mt-1 size-2.5 shrink-0 rounded-full",
												connection?.connected ? "bg-emerald-500" : "bg-muted-foreground",
											)}
										/>
										<div className="min-w-0 flex-1">
											<p className="text-[15px] font-medium">
												{connection?.connected ? "Аккаунт подключён" : "Аккаунт не подключён"}
											</p>
											<p className="text-muted-foreground text-[13px] break-words">
												{connection?.connected
													? "Сессия живая — можно переключать воркспейсы в сайдбаре."
													: "Вставьте сессию из браузера — ниже три шага."}
											</p>
										</div>
										<Button
											variant="secondary"
											size="sm"
											onClick={() =>
												run("recheck", async () => onConnectionChange(await api.connectionStatus()))
											}
										>
											<RefreshCw className="size-3.5" /> Проверить
										</Button>
									</div>

									{connection?.connected ? (
										<div className="grid gap-2 sm:grid-cols-2">
											<div className="min-w-0 rounded-lg border p-2">
												<p className="text-muted-foreground text-[11px] uppercase">Воркспейс</p>
												<p className="font-mono text-[11px] break-all">{connection.spaceId || "—"}</p>
											</div>
											<div className="min-w-0 rounded-lg border p-2">
												<p className="text-muted-foreground text-[11px] uppercase">Аккаунт</p>
												<p className="font-mono text-[11px] break-all">{connection.userId || "—"}</p>
											</div>
										</div>
									) : null}
								</Card>

								<Card>
									<Label>Как подключить аккаунт</Label>
									<ol className="flex flex-col gap-2">
										{[
											"Откройте Notion в браузере и напишите любое сообщение Notion AI.",
											"В DevTools → Network найдите запрос runInferenceTranscript.",
											"Правый клик → Copy as cURL (bash) и вставьте сюда.",
										].map((step, i) => (
											<li key={step} className="flex min-w-0 items-start gap-2 text-[13px]">
												<span className="bg-muted text-foreground grid size-5 shrink-0 place-items-center rounded-full text-[11px]">
													{i + 1}
												</span>
												<span className="min-w-0 break-words">{step}</span>
											</li>
										))}
									</ol>

									<Textarea
										ref={curlRef}
										value={curl}
										onChange={(e) => setCurl(e.target.value)}
										spellCheck={false}
										className="max-h-[420px] min-h-40 w-full font-mono text-xs break-all whitespace-pre-wrap"
										placeholder="curl '…/api/v3/runInferenceTranscript' -H 'cookie: …' --data-raw '…'"
									/>

									<div className="flex flex-wrap gap-2">
										<Button
											variant="brand"
											disabled={!curl.trim() || busy === "curl"}
											onClick={() =>
												run("curl", async () => {
													const st = await api.importCurl(curl)
													onConnectionChange(st)
													setCurl("")
													onToast("Аккаунт подключён")
												})
											}
										>
											{busy === "curl" ? "Подключаю…" : "Подключить аккаунт"}
										</Button>
										<Button
											variant="secondary"
											onClick={() =>
												run("paste", async () => {
													const text = await navigator.clipboard.readText()
													if (!text.trim()) throw new Error("буфер обмена пуст")
													setCurl(text)
												})
											}
										>
											<ClipboardPaste className="size-3.5" /> Вставить из буфера
										</Button>
										<Button
											variant="destructive"
											onClick={() =>
												run("clear", async () => {
													await api.clearSession()
													onConnectionChange(await api.connectionStatus())
													onToast("Сессия удалена")
												})
											}
										>
											Выйти
										</Button>
									</div>
									<p className="text-muted-foreground text-xs break-words">
										Чтобы добавить второй аккаунт, войдите в него в браузере и вставьте cURL
										оттуда — оба аккаунта появятся в переключателе сайдбара.
									</p>
								</Card>
							</>
						) : null}

						{/* --------------------------------------------------------- mcp */}
						{tab === "mcp" ? (
							<>
								<Card>
									<div className="flex min-w-0 flex-wrap items-center gap-2">
										<span className="text-sm font-medium">Встроенные инструменты</span>
										<Badge tone={status?.notcodeRunning ? "ok" : "muted"}>
											{status?.notcodeRunning ? "готовы" : "выключены"}
										</Badge>
									</div>
									<p className="text-muted-foreground text-xs break-words">
										Файлы, терминал и git встроены в приложение. Одна кнопка делает всё:
										запускает сервер инструментов, поднимает туннель и регистрирует их в Notion.
										Никаких папок, команд и портов указывать не нужно.
									</p>

									<div className="flex flex-wrap gap-2">
										<Button
											variant="brand"
											disabled={busy === "connect"}
											onClick={() =>
												run("connect", async () => {
													const res = await api.connectNotcodeMcp()
													setStatus(res.status)
													const fresh = await api.getSettings()
													setDraft(fresh)
													onSaved(fresh)
													await refreshMcp()
													onToast(
														`Инструменты подключены: ${res.module?.toolCount ?? 0}`,
													)
												})
											}
										>
											{busy === "connect" ? "Подключаю…" : "Включить инструменты"}
										</Button>
										<Button
											variant="secondary"
											disabled={busy === "stop"}
											onClick={() => run("stop", async () => setStatus(await api.notcodeStop()))}
										>
											Выключить
										</Button>
										<Button variant="ghost" onClick={() => void refreshMcp()}>
											<RefreshCw className="size-3.5" /> Обновить
										</Button>
									</div>

									<label className="flex items-center justify-between gap-3 text-[13px]">
										<span className="min-w-0">Включать инструменты автоматически при старте</span>
										<Switch
											checked={draft.autoStartNotcode}
											onCheckedChange={(v) => set("autoStartNotcode", v)}
										/>
									</label>

									{status?.error ? (
										<p className="text-destructive text-xs break-words">{status.error}</p>
									) : null}
								</Card>

								{/* Статусы процессов: сразу понятно, что запущено, где и с каким токеном. */}
								<Card>
									<Label>Состояние</Label>
									<div className="grid min-w-0 gap-2 sm:grid-cols-2">
										<StatusRow
											title="NotCode"
											ok={!!status?.notcodeRunning}
											lines={[
												["адрес", status?.localUrl ?? ""],
												["версия", status?.version ?? ""],
												["тулов", status?.tools ? String(status.tools) : ""],
												["сессий", status?.sessions !== undefined ? String(status.sessions) : ""],
												["pid", status?.notcodePid ? String(status.notcodePid) : "внешний"],
												["режим", status?.mode ?? ""],
												["корень", status?.workspaceRoot ?? ""],
												["конфиг", status?.configFile ?? ""],
											]}
										/>
										<StatusRow
											title="ngrok"
											ok={!!status?.ngrokRunning && status?.tunnelOk !== false}
											lines={[
												["туннель", status?.publicUrl ?? ""],
												[
													"проверка",
													status?.publicUrl
														? status?.tunnelOk
															? "туннель доводит до NotCode"
															: (status?.tunnelError ?? "туннель не отвечает")
														: "",
												],
												["mcp", status?.mcpUrl ?? ""],
												["pid", status?.ngrokPid ? String(status.ngrokPid) : "внешний"],
												["панель", status?.ngrokDashboard ?? ""],
											]}
										/>
									</div>

									<Field
										label="Bearer-токен NotCode"
										hint="Генерируется заново при каждом запуске и сразу уходит в Notion."
									>
										<div className="flex min-w-0 gap-2">
											<Input readOnly value={status?.token ?? ""} className="font-mono text-xs" />
											<Button
												variant="secondary"
												size="sm"
												disabled={!status?.token}
												onClick={() => {
													void navigator.clipboard.writeText(status?.token ?? "")
													onToast("Токен скопирован")
												}}
											>
												<Copy className="size-3.5" /> Копировать
											</Button>
										</div>
									</Field>

									<Field label="Адрес для Notion" hint="Этот URL регистрируется как MCP-сервер.">
										<div className="flex min-w-0 gap-2">
											<Input
												readOnly
												value={status?.mcpUrl || draft.mcpServerUrl || ""}
												className="font-mono text-xs"
											/>
											<Button
												variant="secondary"
												size="sm"
												disabled={!(status?.mcpUrl || draft.mcpServerUrl)}
												onClick={() => {
													void navigator.clipboard.writeText(
														status?.mcpUrl || draft.mcpServerUrl || "",
													)
													onToast("Адрес скопирован")
												}}
											>
												<Copy className="size-3.5" /> Копировать
											</Button>
										</div>
									</Field>

									<Button variant="ghost" size="sm" onClick={() => setTab("logs")}>
										<ScrollText className="size-3.5" /> Смотреть логи
									</Button>
								</Card>
							</>
						) : null}

						{/* -------------------------------------------------------- logs */}
						{tab === "logs" ? (
							<Card>
								<div className="flex min-w-0 flex-wrap items-center gap-2">
									{(
										[
											["all", "Всё"],
											["notcode", "NotCode"],
											["ngrok", "ngrok"],
											["app", "Приложение"],
										] as Array<["all" | "notcode" | "ngrok" | "app", string]>
									).map(([key, label]) => (
										<Button
											key={key}
											size="sm"
											variant={logSource === key ? "secondary" : "ghost"}
											onClick={() => setLogSource(key)}
										>
											{label}
										</Button>
									))}
									<span className="flex-1" />
									<Button variant="ghost" size="sm" onClick={() => void refreshLogs()}>
										<RefreshCw className="size-3.5" /> Обновить
									</Button>
									<Button
										variant="secondary"
										size="sm"
										onClick={() =>
											run("clear-logs", async () => {
												await api.notcodeClearLogs()
												setLogLines([])
											})
										}
									>
										Очистить
									</Button>
								</div>
								<p className="text-muted-foreground text-xs break-words">
									Здесь видно всё, что пишут NotCode и ngrok: токен, адрес туннеля
									и причины ошибок подключения.
								</p>
								<div className="bg-accent/30 max-h-[420px] min-w-0 overflow-auto rounded-lg border p-2">
									{logLines.filter((l) => logSource === "all" || l.source === logSource).length ===
									0 ? (
										<p className="text-muted-foreground p-1 text-[13px]">
											Пока пусто — запустите инструменты, и здесь появится живой лог.
										</p>
									) : (
										<ul className="flex min-w-0 flex-col">
											{logLines
												.filter((l) => logSource === "all" || l.source === logSource)
												.map((line, i) => (
													<li
														key={`${line.time}-${i}`}
														className="flex min-w-0 gap-2 py-0.5 font-mono text-[11px] leading-[1.5]"
													>
														<span className="text-muted-foreground shrink-0">
															{logTime(line.time)}
														</span>
														<span className="text-muted-foreground w-14 shrink-0 truncate">
															{line.source}
														</span>
														<span
															className={cn(
																"min-w-0 flex-1 break-all whitespace-pre-wrap",
																line.level === "error" && "text-red-500",
																line.level === "warn" && "text-amber-500",
															)}
														>
															{line.text}
														</span>
													</li>
												))}
										</ul>
									)}
								</div>
							</Card>
						) : null}

						{/* ------------------------------------------------------- usage */}
						{tab === "usage" ? (
							<Card>
								<div className="flex min-w-0 items-center justify-between gap-2">
									<Label>Расход AI-кредитов</Label>
									<Button variant="ghost" size="sm" onClick={() => void refreshUsage()}>
										<RefreshCw className="size-3.5" /> Обновить
									</Button>
								</div>

								{usageError ? (
									<p className="text-destructive text-xs break-words">{usageError}</p>
								) : null}

								{usage && !usage.limitReached ? (
									<p className="text-muted-foreground text-xs">
										Лимиты в норме{usage.status ? ` (статус: ${usage.status})` : ""}.
									</p>
								) : null}

								{usage?.limitReached ? (
									<div className="rounded-lg border border-red-500/40 bg-red-500/10 p-3 text-[13px] text-red-500">
										Лимит исчерпан.
										{usage.resetAtMs
											? ` Сброс ${new Date(usage.resetAtMs).toLocaleString()}.`
											: ""}
									</div>
								) : null}

								<div className="flex min-w-0 flex-col gap-4">
									<UsageBar
										title={`Rolling${usage?.rolling?.window ? ` (${usage.rolling.window})` : ""}`}
										hint={
											usage?.resetsInSeconds
												? `сброс через ${Math.max(1, Math.round(usage.resetsInSeconds / 3600))} ч`
												: windowHint(usage?.rolling)
										}
										used={usage?.rolling?.used ?? 0}
										limit={usage?.rolling?.limit ?? 0}
									/>
									<UsageBar
										title="Monthly"
										hint={
											usage?.monthly?.periodEndMs
												? `до ${new Date(usage.monthly.periodEndMs).toLocaleDateString()}`
												: windowHint(usage?.monthly)
										}
										used={usage?.monthly?.used ?? usage?.premiumUsed ?? 0}
										limit={usage?.monthly?.limit ?? usage?.premiumLimit ?? 0}
									/>
									{usage?.basicUserLimit ? (
										<UsageBar
											title="Basic-кредиты (вы)"
											used={usage.basicUserUsed}
											limit={usage.basicUserLimit}
										/>
									) : null}
								</div>

								<Separator />
								<div className="grid min-w-0 gap-2 sm:grid-cols-3">
									{[
										["Тарифный уровень", usage?.creditTier ?? "—"],
										["Баланс кредитов", usage ? usage.creditBalance.toFixed(2) : "—"],
										["Перерасход", usage ? usage.creditsInOverage.toFixed(2) : "—"],
										[
											"За всё время (воркспейс)",
											usage?.lifetimeSpaceUsed !== undefined
												? usage.lifetimeSpaceUsed.toFixed(0)
												: "—",
										],
										[
											"За всё время (вы)",
											usage?.lifetimeUserUsed !== undefined ? usage.lifetimeUserUsed.toFixed(0) : "—",
										],
									].map(([k, v]) => (
										<div key={k} className="min-w-0 rounded-lg border p-2">
											<p className="text-muted-foreground text-[11px] uppercase">{k}</p>
											<p className="truncate text-[13px] font-medium">{v}</p>
										</div>
									))}
								</div>
							</Card>
						) : null}

						{/* ----------------------------------------------------- servers */}
						{tab === "servers" ? (
							<>
								<Card>
									<Label>Подключённые серверы</Label>
									{modules.length === 0 ? (
										<p className="text-muted-foreground text-xs">Пока ничего не подключено.</p>
									) : (
										<ul className="flex flex-col gap-2">
											{modules.map((m) => (
												<li
													key={m.integrationId}
													className="flex min-w-0 items-center gap-2 rounded-lg border p-2"
												>
													<div className="min-w-0 flex-1">
														<div className="truncate text-sm font-medium">{m.name}</div>
														<div className="text-muted-foreground truncate text-xs">{m.serverUrl}</div>
													</div>
													<Badge>{m.toolCount} tools</Badge>
													<Switch
														checked={m.enabled}
														onCheckedChange={(v) =>
															run(`en-${m.integrationId}`, async () => {
																await api.mcpSetEnabled(m.integrationId, v)
																await refreshMcp()
															})
														}
													/>
													<Button
														variant="subtle"
														size="icon-sm"
														className="hover:text-destructive"
														onClick={() =>
															run(`rm-${m.integrationId}`, async () => {
																await api.mcpDisconnect(m.integrationId)
																await refreshMcp()
																onToast("Сервер отключён")
															})
														}
													>
														<Trash2 className="size-3.5" />
													</Button>
												</li>
											))}
										</ul>
									)}

									<Separator />

									<Label>Добавить свой сервер</Label>
									<div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-3">
										<Field label="Имя">
											<Input value={newName} onChange={(e) => setNewName(e.target.value)} />
										</Field>
										<Field label="Адрес" className="sm:col-span-2">
											<Input
												value={newUrl}
												onChange={(e) => setNewUrl(e.target.value)}
												placeholder="https://…/mcp"
											/>
										</Field>
										<Field label="Токен (если нужен)" className="sm:col-span-2">
											<Input value={newToken} onChange={(e) => setNewToken(e.target.value)} />
										</Field>
										<div className="flex items-end">
											<Button
												variant="brand"
												className="w-full"
												disabled={!newUrl.trim() || busy === "add"}
												onClick={() =>
													run("add", async () => {
														await api.mcpConnect({
															name: newName.trim() || "mcp",
															serverUrl: newUrl.trim(),
															authToken: newToken.trim() || undefined,
														})
														setNewName("")
														setNewUrl("")
														setNewToken("")
														await refreshMcp()
														onToast("Сервер добавлен")
													})
												}
											>
												Добавить
											</Button>
										</div>
									</div>
								</Card>
							</>
						) : null}

						{/* -------------------------------------------------------- chat */}
						{tab === "chat" ? (
							<Card>
								<Label>Источники для ответов</Label>
								<label className="flex items-start justify-between gap-3 text-[13px]">
									<span className="min-w-0">
										Искать во всём, к чему есть доступ
										<span className="text-muted-foreground block text-xs break-words">
											По умолчанию выключено: модель не шарит весь воркспейс и отвечает
											быстрее, а MCP-инструменты работают как надо.
										</span>
									</span>
									<Switch
										checked={draft.searchAllSources}
										onCheckedChange={(v) => set("searchAllSources", v)}
									/>
								</label>
								<Separator />
								<label className="flex items-center justify-between gap-3 text-[13px]">
									<span className="min-w-0">Отправлять по Enter (Shift+Enter — новая строка)</span>
									<Switch
										checked={draft.sendWithEnter}
										onCheckedChange={(v) => set("sendWithEnter", v)}
									/>
								</label>
							</Card>
						) : null}

						{/* ------------------------------------------------------ prompt */}
						{tab === "prompt" ? (
							<Card>
								<Field
									label="Системный промпт"
									hint="Подставляется перед каждым вашим сообщением."
								>
									<Textarea
										value={draft.systemPrompt}
										onChange={(e) => set("systemPrompt", e.target.value)}
										className="min-h-48 break-words whitespace-pre-wrap"
									/>
								</Field>
								<div className="flex flex-wrap items-center justify-between gap-3">
									<label className="flex items-center gap-2 text-[13px]">
										<Switch
											checked={draft.autoPrependMcp}
											onCheckedChange={(v) => set("autoPrependMcp", v)}
										/>
										Добавлять промпт к каждому сообщению
									</label>
									<Button
										variant="secondary"
										onClick={() =>
											run("prompt", async () => set("systemPrompt", await api.defaultSystemPrompt()))
										}
									>
										Сбросить по умолчанию
									</Button>
								</div>
							</Card>
						) : null}

						{/* ------------------------------------------------------- model */}
						{tab === "model" ? (
							<Card>
								<Field label="Модель" hint="Список приходит из Notion после подключения аккаунта.">
									<Select
										value={draft.model || "__auto__"}
										onValueChange={(v) => set("model", v === "__auto__" ? "" : v)}
									>
										<SelectTrigger>
											<SelectValue placeholder="Авто" />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value="__auto__">Авто</SelectItem>
											{models.map((m) => (
												<SelectItem key={m.id} value={m.id}>
													{m.label || m.id}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</Field>
								<Field label="Глубина рассуждений">
									<Select
										value={draft.reasoningEffort || "__auto__"}
										onValueChange={(v) => set("reasoningEffort", v === "__auto__" ? "" : v)}
									>
										<SelectTrigger>
											<SelectValue placeholder="авто" />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value="__auto__">авто</SelectItem>
											{["minimal", "low", "medium", "high"].map((v) => (
												<SelectItem key={v} value={v}>
													{v}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</Field>
							</Card>
						) : null}

						{/* -------------------------------------------------- appearance */}
						{tab === "appearance" ? (
							<Card>
								<Field label="Тема">
									<Select value={draft.theme} onValueChange={(v) => set("theme", v as Settings["theme"])}>
										<SelectTrigger>
											<SelectValue />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value="notion-dark">Notion Dark</SelectItem>
											<SelectItem value="notion-light">Notion Light</SelectItem>
										</SelectContent>
									</Select>
								</Field>
								<Field label={`Масштаб шрифта: ${draft.fontScale}%`}>
									<Slider
										min={80}
										max={130}
										step={5}
										value={[draft.fontScale]}
										onValueChange={([v]) => set("fontScale", v)}
									/>
								</Field>
								<label className="flex items-center justify-between text-[13px]">
									Отключить анимации
									<Switch checked={draft.motionOff} onCheckedChange={(v) => set("motionOff", v)} />
								</label>
							</Card>
						) : null}

						{/* ------------------------------------------------ workspaces */}
						{tab === "workspaces" ? (
							<>
								{(workspaces?.accounts ?? []).map((acc) => (
									<Card key={acc.userId}>
										<div className="flex min-w-0 items-center gap-2">
											<div className="min-w-0 flex-1">
												<p className="truncate text-[15px] font-medium">{acc.name || acc.email}</p>
												<p className="text-muted-foreground truncate text-xs">{acc.email}</p>
											</div>
											{acc.active ? <Badge tone="ok">активен</Badge> : null}
										</div>
										<ul className="flex flex-col gap-1.5">
											{acc.spaces.map((sp) => (
												<li
													key={sp.id}
													className={cn(
														"flex min-w-0 items-center gap-2 rounded-lg border p-2",
														sp.active ? "border-[var(--blue-accent)]" : "",
													)}
												>
													<span className="bg-muted grid size-6 shrink-0 place-items-center rounded-md text-[12px]">
														{sp.icon || sp.name.slice(0, 1).toUpperCase()}
													</span>
													<div className="min-w-0 flex-1">
														<div className="truncate text-[13px]">{sp.name}</div>
														<div className="text-muted-foreground truncate text-[11px]">
															{sp.planType || "—"}
															{sp.isGuest ? " · гость" : ""}
														</div>
													</div>
													<Button
														variant={sp.active ? "ghost" : "secondary"}
														size="sm"
														disabled={sp.active || busy === `sw-${sp.id}`}
														onClick={() =>
															run(`sw-${sp.id}`, async () => {
																setWorkspaces(
																	await api.switchWorkspace(acc.userId, sp.id, sp.spaceViewId, sp.name),
																)
																onConnectionChange(await api.connectionStatus())
																onToast(`Воркспейс: ${sp.name}`)
															})
														}
													>
														{sp.active ? "текущий" : "Перейти"}
													</Button>
												</li>
											))}
										</ul>
									</Card>
								))}

								<Card>
									<Label>Новый воркспейс</Label>
									<div className="flex min-w-0 flex-wrap items-end gap-2">
										<Field label="Название" className="min-w-[200px] flex-1">
											<Input value={newSpace} onChange={(e) => setNewSpace(e.target.value)} />
										</Field>
										<Button
											variant="brand"
											disabled={!newSpace.trim() || busy === "space"}
											onClick={() =>
												run("space", async () => {
													setWorkspaces(await api.createWorkspace(newSpace.trim()))
													setNewSpace("")
													onToast("Воркспейс создан")
												})
											}
										>
											<Plus className="size-3.5" /> {busy === "space" ? "Создаю…" : "Создать"}
										</Button>
										<Button variant="ghost" onClick={() => void api.listWorkspaces().then(setWorkspaces)}>
											<RefreshCw className="size-3.5" /> Обновить
										</Button>
									</div>
									{workspaces && !workspaces.canCreate ? (
										<p className="text-muted-foreground text-xs">
											Для этого аккаунта создание воркспейсов недоступно.
										</p>
									) : null}
								</Card>
							</>
						) : null}

						{/* ----------------------------------------------- marketplace */}
						{tab === "marketplace" ? (
							<Card>
								<div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
									<Label>Каталог MCP-серверов</Label>
									<span className="text-muted-foreground text-[11px]">
										{marketServers ? `${marketItems.length} из реестра MCP` : "загрузка…"}
									</span>
								</div>
								<div className="flex min-w-0 flex-wrap items-center gap-2">
									<Input
										value={marketQuery}
										placeholder="Поиск: github, figma, postgres…"
										className="min-w-[200px] flex-1"
										onChange={(e) => setMarketQuery(e.target.value)}
										onKeyDown={(e) => {
											if (e.key === "Enter") void loadMarket(true)
										}}
									/>
									<Button variant="secondary" disabled={busy === "market"} onClick={() => void loadMarket(true)}>
										<Search className="size-3.5" /> {busy === "market" ? "Ищу…" : "Найти"}
									</Button>
									{marketCursor ? (
										<Button variant="ghost" disabled={busy === "market"} onClick={() => void loadMarket(false)}>
											Показать ещё
										</Button>
									) : null}
								</div>
								{marketError ? (
									<p className="text-destructive text-xs break-words">{marketError}</p>
								) : null}
								<p className="text-muted-foreground text-xs break-words">
									Одна кнопка — и сервер регистрируется в Notion для этого аккаунта.
									Если нужен токен — добавьте его в разделе «Свои серверы».
								</p>
								<ul className="flex flex-col gap-2">
									{marketItems.map((item) => {
										const installed = modules.some((m) => m.serverUrl === item.url)
										return (
											<li key={item.url} className="flex min-w-0 items-center gap-3 rounded-lg border p-2.5">
												<span className="bg-muted grid size-8 shrink-0 place-items-center rounded-lg text-[15px]">
													{item.icon}
												</span>
												<div className="min-w-0 flex-1">
													<div className="truncate text-[14px] font-medium">{item.name}</div>
													<div className="text-muted-foreground text-xs break-words">{item.about}</div>
												</div>
												<Button
													variant={installed ? "ghost" : "secondary"}
													size="sm"
													disabled={installed || busy === `mk-${item.url}`}
													onClick={() =>
														run(`mk-${item.url}`, async () => {
															await api.mcpConnect({ name: item.name, serverUrl: item.url })
															await refreshMcp()
															onToast(`${item.name} подключён`)
														})
													}
												>
													{installed ? "установлен" : "Установить"}
												</Button>
											</li>
										)
									})}
								</ul>
							</Card>
						) : null}

						{/* --------------------------------------------------- support */}
						{tab === "support" ? (
							<>
								<Card>
									<Label>Если что-то не работает</Label>
									<ol className="flex flex-col gap-2">
										{[
											"Сессия живёт недолго: если ответы пропали — вставьте cURL заново.",
											"Если воркспейс подсвечен красным — закончились ответы Notion AI.",
											"Перед обращением скопируйте последние записи из раздела «Отладка».",
										].map((step, i) => (
											<li key={step} className="flex min-w-0 items-start gap-2 text-[13px]">
												<span className="bg-muted text-foreground grid size-5 shrink-0 place-items-center rounded-full text-[11px]">
													{i + 1}
												</span>
												<span className="min-w-0 break-words">{step}</span>
											</li>
										))}
									</ol>
									<div className="flex flex-wrap gap-2">
										<Button variant="secondary" onClick={() => setTab("debug")}>
											<Bug className="size-3.5" /> Открыть отладку
										</Button>
										<Button
											variant="ghost"
											onClick={() =>
												run("copylogs", async () => {
													const entries = await api.debugLogs()
													await navigator.clipboard.writeText(JSON.stringify(entries, null, 2))
													onToast("Логи скопированы")
												})
											}
										>
											Скопировать логи
										</Button>
									</div>
								</Card>
							</>
						) : null}

						{/* ------------------------------------------------------- debug */}
						{tab === "debug" ? (
							<Card>
								<div className="flex flex-wrap gap-2">
									<Button variant="secondary" onClick={() => api.debugLogs().then(setLogs)}>
										<RefreshCw className="size-3.5" /> Обновить
									</Button>
									<Button
										variant="destructive"
										onClick={() =>
											run("clearlogs", async () => {
												await api.clearDebugLogs()
												setLogs([])
											})
										}
									>
										Очистить
									</Button>
								</div>
								{logs.length === 0 ? (
									<p className="text-muted-foreground text-xs">Записей пока нет.</p>
								) : (
									<ul className="flex min-w-0 flex-col gap-2">
										{logs.map((l) => {
											const expanded = openLog === l.id
											return (
												<li key={l.id} className="min-w-0 overflow-hidden rounded-lg border">
													<button
														type="button"
														onClick={() => setOpenLog(expanded ? null : l.id)}
														className="hover:bg-muted/60 flex w-full min-w-0 items-center gap-2 p-2 text-left text-xs"
													>
														<Badge tone={l.error ? "bad" : l.status && l.status < 400 ? "ok" : "muted"}>
															{l.status ?? "—"}
														</Badge>
														<span className="min-w-0 flex-1 truncate font-mono">{l.path}</span>
														<span className="text-muted-foreground shrink-0">
															{l.durationMs ?? "?"} ms
														</span>
													</button>
													{expanded ? (
														<div className="min-w-0 border-t p-2">
															{l.error ? (
																<p className="text-destructive mb-1 text-xs break-words">{l.error}</p>
															) : null}
															<pre className="text-muted-foreground max-h-64 w-full max-w-full overflow-auto font-mono text-[11px] break-all whitespace-pre-wrap">
																{l.responsePreview}
															</pre>
														</div>
													) : null}
												</li>
											)
										})}
									</ul>
								)}
							</Card>
						) : null}
					</div>
				</ScrollArea>

				<div className="flex shrink-0 items-center justify-end gap-2 border-t px-4 py-3">
					<Button variant="brand" disabled={saving} onClick={() => void save()}>
						{saving ? "Сохраняю…" : <><Check className="size-3.5" /> Сохранить</>}
					</Button>
				</div>
			</div>
		</div>
	)
}
