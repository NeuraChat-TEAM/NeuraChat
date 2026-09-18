import { useEffect, useMemo, useState } from "react"
import {
	Activity,
	ArrowLeft,
	Bot,
	Check,
	ChevronRight,
	CircleUserRound,
	Clock3,
	Copy,
	Cpu,
	Database,
	Download,
	ExternalLink,
	Eye,
	EyeOff,
	FileText,
	Gauge,
	Info,
	LayoutDashboard,
	LifeBuoy,
	Loader2,
	Network,
	Palette,
	Plug,
	Plus,
	RefreshCw,
	Search,
	Server,
	Settings2,
	ShieldAlert,
	Sparkles,
	Terminal,
	Trash2,
	Users,
	Wrench,
} from "lucide-react"
import { api, errText, type MarketplaceServer, type NgrokAuthState } from "../../../shared/api/api"
import { cn } from "../../../shared/lib/utils"
import type {
	AIUsage,
	Account,
	ConnectionState,
	DebugEntry,
	McpModule,
	Model,
	NotcodeLogLine,
	NotcodeStatus,
	Settings,
	Space,
	UsageWindow,
	WorkspaceState,
} from "../../../shared/model/types"
import {
	Badge,
	Button,
	Card,
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
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
} from "../../../shared/ui"

type SectionKey = "overview" | "accounts" | "usage" | "workspaces" | "ai" | "tools" | "appearance" | "advanced" | "about"
type ToolTab = "builtin" | "servers" | "catalog" | "developer"

const SECTIONS = [
	{ key: "overview", label: "Обзор", group: "Основное", icon: LayoutDashboard },
	{ key: "accounts", label: "Аккаунты", group: "Основное", icon: Users },
	{ key: "workspaces", label: "Рабочие пространства", group: "Основное", icon: Database },
	{ key: "usage", label: "Лимиты и использование", group: "Основное", icon: Gauge },
	{ key: "ai", label: "AI и чат", group: "Настройки", icon: Sparkles },
	{ key: "tools", label: "Инструменты", group: "Настройки", icon: Plug },
	{ key: "appearance", label: "Внешний вид", group: "Настройки", icon: Palette },
	{ key: "advanced", label: "Дополнительно", group: "Система", icon: Settings2 },
	{ key: "about", label: "О приложении", group: "Система", icon: Info },
] as const

const TOOL_TABS: Array<{ key: ToolTab; label: string }> = [
	{ key: "builtin", label: "Встроенные" },
	{ key: "servers", label: "MCP-серверы" },
	{ key: "catalog", label: "Каталог" },
	{ key: "developer", label: "Для разработчиков" },
]

function redact(value: string) {
	return value
		.replace(/(authorization|cookie|token|secret|password)(["'\s:=]+)[^\s,"'}]+/gi, "$1$2[скрыто]")
		.replace(/\b[a-f0-9]{32,}\b/gi, "[скрыто]")
}

function initials(account?: Account) {
	return (account?.name || account?.email || "?").trim().slice(0, 1).toUpperCase()
}

function formatTime(value?: number | string) {
	if (!value) return "нет данных"
	const date = typeof value === "number" ? new Date(value) : new Date(value)
	return Number.isNaN(date.getTime()) ? "нет данных" : date.toLocaleString()
}

function usageState(used: number, limit: number) {
	if (!limit) return { label: "Нет данных", color: "bg-muted-foreground" }
	const pct = used / limit
	if (pct >= 1) return { label: "Лимит исчерпан", color: "bg-red-500" }
	if (pct >= 0.9) return { label: "Почти исчерпан", color: "bg-red-500" }
	if (pct >= 0.75) return { label: "Повышенный расход", color: "bg-amber-500" }
	return { label: "В норме", color: "bg-emerald-500" }
}

function UsageMeter({ title, window, fallbackUsed = 0, fallbackLimit = 0 }: { title: string; window?: UsageWindow; fallbackUsed?: number; fallbackLimit?: number }) {
	const used = window?.used ?? fallbackUsed
	const limit = window?.limit ?? fallbackLimit
	const percent = limit > 0 ? Math.min(100, Math.max(0, used / limit * 100)) : 0
	const state = usageState(used, limit)
	return <div className="space-y-2">
		<div className="flex items-center justify-between gap-3">
			<div><p className="text-sm font-medium">{title}</p><p className="text-muted-foreground text-xs">{state.label}</p></div>
			<p className="text-sm tabular-nums">{limit ? `${used.toFixed(2)} из ${limit}` : "—"}</p>
		</div>
		<div className="bg-muted h-2 overflow-hidden rounded-full"><div className={cn("h-full rounded-full transition-[width]", state.color)} style={{ width: `${percent}%` }} /></div>
		<div className="text-muted-foreground flex justify-between text-[11px]"><span>{percent.toFixed(0)}%</span><span>{window?.periodEndMs ? `Сброс: ${formatTime(window.periodEndMs)}` : ""}</span></div>
	</div>
}

function EmptyState({ icon: Icon, title, text, action }: { icon: typeof Info; title: string; text: string; action?: React.ReactNode }) {
	return <div className="border-border/70 flex min-h-40 flex-col items-center justify-center rounded-xl border border-dashed px-6 text-center">
		<Icon className="text-muted-foreground mb-3 size-6" />
		<p className="text-sm font-medium">{title}</p><p className="text-muted-foreground mt-1 max-w-md text-xs">{text}</p>{action ? <div className="mt-4">{action}</div> : null}
	</div>
}

/**
 * Преднастроенные MCP-серверы с mcpservers.org.
 * needsKey — сервер без ключа ответит отказом, поэтому открываем диалог
 * с полями, а не ставим в один клик.
 */
const MCP_PRESETS: Array<{ name: string; title: string; url: string; hint: string; needsKey?: "query" | "header" | "token" }> = [
	{ name: "deepwiki", title: "DeepWiki (Devin)", url: "https://mcp.deepwiki.com/mcp", hint: "Документация по любому GitHub-репозиторию. Ключ не нужен." },
	{ name: "context7", title: "Context7", url: "https://mcp.context7.com/mcp", hint: "Свежая документация библиотек. Ключ Context7 опционален.", needsKey: "header" },
	{ name: "github", title: "GitHub", url: "https://api.githubcopilot.com/mcp/", hint: "Официальный GitHub MCP. Нужен Personal Access Token.", needsKey: "token" },
	{ name: "browserbase", title: "Browserbase", url: "https://server.smithery.ai/@browserbasehq/mcp-browserbase/mcp", hint: "Управление облачным браузером. Smithery требует api_key и profile в query.", needsKey: "query" },
	{ name: "playwright", title: "Playwright", url: "https://server.smithery.ai/@microsoft/playwright-mcp/mcp", hint: "Автоматизация браузера. Через Smithery — ключ в query.", needsKey: "query" },
	{ name: "knowledge-graph", title: "Knowledge Graph Memory", url: "https://server.smithery.ai/@shaneholloman/mcp-knowledge-graph/mcp", hint: "Память в виде графа знаний.", needsKey: "query" },
	{ name: "dowse", title: "Dowse (LTSpace)", url: "https://server.smithery.ai/@ltspace/dowse/mcp", hint: "Поиск и анализ данных.", needsKey: "query" },
	{ name: "so-me-studio", title: "So-Me Studio", url: "https://server.smithery.ai/@so-me/studio/mcp", hint: "Соцсети и контент.", needsKey: "query" },
	{ name: "vexralabs-docs", title: "Vexralabs Docs", url: "https://zerodom.vexralabs.com/docs/mcp", hint: "Документация Zerodom / Vexralabs." },
]

// Разбор строки вида "a=1&b=2" или "X-Api-Key: abc; X-Profile: main" в объект.
// Нужно для Smithery (ключ в query) и серверов с кастомными заголовками.
function parsePairs(raw: string, sep: "=" | ":"): Record<string, string> | undefined {
	const text = (raw || "").trim().replace(/^\?/, "")
	if (!text) return undefined
	const out: Record<string, string> = {}
	for (const part of text.split(/[&;\n]/)) {
		const chunk = part.trim()
		if (!chunk) continue
		const at = chunk.indexOf(sep)
		if (at <= 0) continue
		const key = chunk.slice(0, at).trim()
		const value = chunk.slice(at + 1).trim()
		if (key) out[key] = value
	}
	return Object.keys(out).length ? out : undefined
}

function SectionHeader({ title, description, action }: { title: string; description?: string; action?: React.ReactNode }) {
	return <div className="flex min-w-0 items-start justify-between gap-4">
		<div><h2 className="text-[22px] font-semibold tracking-tight">{title}</h2>{description ? <p className="text-muted-foreground mt-1 max-w-2xl text-sm">{description}</p> : null}</div>{action}
	</div>
}

export default function SettingsModal({ open, onOpenChange, navWidth = 276, settings, models, connection, onSaved, onConnectionChange, onToast }: {
	open: boolean
	onOpenChange: (open: boolean) => void
	navWidth?: number
	settings: Settings
	models: Model[]
	connection: ConnectionState | null
	onSaved: (s: Settings) => void
	onConnectionChange: (c: ConnectionState) => void
	onToast: (message: string, error?: boolean) => void
}) {
	const [section, setSection] = useState<SectionKey>("overview")
	const [navQuery, setNavQuery] = useState("")
	const [draft, setDraft] = useState(settings)
	const [workspaces, setWorkspaces] = useState<WorkspaceState | null>(null)
	const [usage, setUsage] = useState<AIUsage | null>(null)
	const [modules, setModules] = useState<McpModule[]>([])
	const [status, setStatus] = useState<NotcodeStatus | null>(null)
	const [ngrok, setNgrok] = useState<NgrokAuthState | null>(null)
	const [debug, setDebug] = useState<DebugEntry[]>([])
	const [logs, setLogs] = useState<NotcodeLogLine[]>([])
	const [busy, setBusy] = useState("")
	const [loadedAt, setLoadedAt] = useState<number>()
	const [loadError, setLoadError] = useState("")
	const [toolTab, setToolTab] = useState<ToolTab>("builtin")
	const [accountDialog, setAccountDialog] = useState(false)
	const [serverDialog, setServerDialog] = useState(false)
	const [curl, setCurl] = useState("")
	const [workspaceQuery, setWorkspaceQuery] = useState("")
	const [accountFilter, setAccountFilter] = useState("all")
	const [marketQuery, setMarketQuery] = useState("")
	const [market, setMarket] = useState<MarketplaceServer[]>([])
	const [marketError, setMarketError] = useState("")
	const [logQuery, setLogQuery] = useState("")
	const [logSource, setLogSource] = useState("all")
	const [logsPaused, setLogsPaused] = useState(false)
	const [newServer, setNewServer] = useState({ name: "", url: "", token: "", query: "", headers: "", autoRun: true })
	const [ngrokToken, setNgrokToken] = useState("")
	const [revealDeveloper, setRevealDeveloper] = useState(false)

	const activeAccount = workspaces?.accounts.find(a => a.userId === workspaces.activeUserId)
	const activeSpace = activeAccount?.spaces.find(s => s.id === workspaces?.activeSpaceId)
	const dirtyPrompt = draft.systemPrompt !== settings.systemPrompt || draft.autoPrependMcp !== settings.autoPrependMcp

	async function refreshAll() {
		setBusy("refresh")
		setLoadError("")
		const results = await Promise.allSettled([
			api.listWorkspaces(), api.aiUsage(), api.mcpList(), api.notcodeStatus(), api.ngrokTokenStatus(), api.debugLogs(), api.notcodeLogs(),
		])
		if (results[0].status === "fulfilled") setWorkspaces(results[0].value)
		if (results[1].status === "fulfilled") setUsage(results[1].value)
		if (results[2].status === "fulfilled") setModules(results[2].value ?? [])
		if (results[3].status === "fulfilled") setStatus(results[3].value)
		if (results[4].status === "fulfilled") setNgrok(results[4].value)
		if (results[5].status === "fulfilled") setDebug(results[5].value ?? [])
		if (results[6].status === "fulfilled") setLogs(results[6].value ?? [])
		const failed = results.filter(r => r.status === "rejected")
		if (failed.length) setLoadError(`Не удалось обновить часть данных (${failed.length}). Показаны последние доступные значения.`)
		setLoadedAt(Date.now())
		setBusy("")
	}

	useEffect(() => { if (open) { setDraft(settings); void refreshAll() } }, [open, settings])
	useEffect(() => {
		if (!open || section !== "advanced" || logsPaused) return
		const timer = window.setInterval(() => api.notcodeLogs().then(setLogs).catch(() => {}), 2500)
		return () => window.clearInterval(timer)
	}, [open, section, logsPaused])
	useEffect(() => {
		if (!open) return
		const close = (event: KeyboardEvent) => { if (event.key === "Escape" && !(event.target instanceof HTMLInputElement) && !(event.target instanceof HTMLTextAreaElement)) onOpenChange(false) }
		window.addEventListener("keydown", close); return () => window.removeEventListener("keydown", close)
	}, [open, onOpenChange])

	async function run(key: string, action: () => Promise<void>) {
		setBusy(key)
		try { await action() } catch (error) { onToast(errText(error), true) } finally { setBusy("") }
	}
	async function saveSettings(next: Settings, message = "Сохранено") {
		setDraft(next)
		const saved = await api.saveSettings(next)
		onSaved(saved); setDraft(saved); onToast(message)
	}
	async function patchSettings(patch: Partial<Settings>, message?: string) { await saveSettings({ ...draft, ...patch }, message) }
	async function switchSpace(account: Account, space: Space) {
		await run(`space:${space.id}`, async () => {
			const next = await api.switchWorkspace(account.userId, space.id, space.spaceViewId, space.name)
			setWorkspaces(next); onConnectionChange(await api.connectionStatus()); setUsage(await api.aiUsage()); onToast(`Открыто: ${space.name}`)
		})
	}
	async function loadCatalog(reset = true) {
		setBusy("catalog"); setMarketError("")
		try {
			const page = await api.marketplaceList(marketQuery.trim(), "", true)
			const seen = new Set<string>()
			setMarket((page.servers ?? []).filter(item => { const key = (item.serverUrl || item.name).toLowerCase(); if (seen.has(key)) return false; seen.add(key); return true }))
		} catch (error) { setMarketError(errText(error)) } finally { setBusy("") }
	}
	function exportDiagnostics() {
		const payload = { generatedAt: new Date().toISOString(), connection: { connected: connection?.connected, userId: connection?.userId, spaceId: connection?.spaceId }, status: { notcodeRunning: status?.notcodeRunning, ngrokRunning: status?.ngrokRunning, tunnelOk: status?.tunnelOk, error: status?.error }, logs: logs.map(line => ({ ...line, text: redact(line.text) })), requests: debug.map(entry => ({ ...entry, requestCurl: "[скрыто]", responsePreview: redact(entry.responsePreview) })) }
		const url = URL.createObjectURL(new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" }))
		const link = document.createElement("a"); link.href = url; link.download = `neura-diagnostics-${Date.now()}.json`; link.click(); URL.revokeObjectURL(url)
	}

	const filteredSections = SECTIONS.filter(item => !navQuery.trim() || item.label.toLowerCase().includes(navQuery.trim().toLowerCase()))
	const grouped = filteredSections.reduce<Record<string, typeof filteredSections>>((all, item) => { (all[item.group] ||= []).push(item); return all }, {})
	const workspaceRows = (workspaces?.accounts ?? []).flatMap(account => account.spaces.map(space => ({ account, space }))).filter(({ account, space }) => (accountFilter === "all" || account.userId === accountFilter) && space.name.toLowerCase().includes(workspaceQuery.toLowerCase()))
	const filteredLogs = logs.filter(line => (logSource === "all" || line.source === logSource) && redact(line.text).toLowerCase().includes(logQuery.toLowerCase()))
	const warnings = [!connection?.connected ? "Подключите Notion-аккаунт" : "", !ngrok?.configured ? "Для встроенных инструментов нужен ngrok authtoken" : "", status?.error || "", usage?.limitReached ? "Лимит AI активного пространства исчерпан" : ""].filter(Boolean)

	if (!open) return null
	return <div className="bg-background absolute inset-0 z-40 flex min-h-0 min-w-0">
		<nav style={{ width: navWidth }} className="bg-sidebar flex h-full shrink-0 flex-col border-r p-2">
			<div className="relative mb-2"><Search className="text-muted-foreground absolute left-2 top-2 size-3.5" /><Input value={navQuery} onChange={e => setNavQuery(e.target.value)} placeholder="Найти настройку" className="h-8 pl-7" /></div>
			<div className="min-h-0 flex-1 overflow-y-auto">
				{Object.entries(grouped).map(([group, items]) => <div key={group} className="mb-3"><p className="text-muted-foreground px-2 pb-1 text-[10px] font-medium uppercase tracking-wider">{group}</p>{items.map(item => <button key={item.key} onClick={() => setSection(item.key)} className={cn("hover:bg-sidebar-accent flex h-9 w-full items-center gap-2 rounded-md px-2 text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40", section === item.key ? "bg-sidebar-accent text-foreground" : "text-muted-foreground")}><item.icon className="size-4" /><span className="truncate">{item.label}</span></button>)}</div>)}
			</div>
			<button onClick={() => onOpenChange(false)} className="text-muted-foreground hover:bg-sidebar-accent flex h-9 items-center gap-2 rounded-md border-t px-2 text-sm"><ArrowLeft className="size-4" />Вернуться в чат<span className="ml-auto text-[10px]">Esc</span></button>
		</nav>
		<div className="min-w-0 flex-1"><ScrollArea className="h-full"><main className="mx-auto flex w-full max-w-[1060px] flex-col gap-6 px-8 py-7 pb-16">
			{loadError ? <div className="border-amber-500/30 bg-amber-500/10 flex items-center gap-2 rounded-lg border px-3 py-2 text-sm text-amber-500"><ShieldAlert className="size-4" />{loadError}<Button className="ml-auto" size="sm" onClick={() => void refreshAll()}>Повторить</Button></div> : null}

			{section === "overview" ? <>
				<SectionHeader title="Обзор" description="Текущее состояние Neura без технического шума." action={<Button variant="secondary" onClick={() => void refreshAll()} disabled={busy === "refresh"}><RefreshCw className={cn("size-4", busy === "refresh" && "animate-spin")} />Обновить</Button>} />
				{warnings.length ? <div className="border-amber-500/30 bg-amber-500/10 rounded-xl border p-4"><p className="font-medium text-amber-500">Требуется внимание</p><ul className="mt-2 space-y-1 text-sm">{warnings.map(item => <li key={item}>• {item}</li>)}</ul></div> : null}
				<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
					<button onClick={() => setSection("accounts")} className="bg-card hover:bg-accent rounded-xl border p-4 text-left"><div className="flex items-center gap-2"><CircleUserRound className="size-4" /><b>Аккаунт</b><ChevronRight className="ml-auto size-4" /></div><p className="mt-3 truncate text-sm">{activeAccount?.name || "Не подключён"}</p><p className="text-muted-foreground truncate text-xs">{activeAccount?.email || "Добавьте аккаунт Notion"}</p></button>
					<button onClick={() => setSection("workspaces")} className="bg-card hover:bg-accent rounded-xl border p-4 text-left"><div className="flex items-center gap-2"><Database className="size-4" /><b>Пространство</b><ChevronRight className="ml-auto size-4" /></div><p className="mt-3 truncate text-sm">{activeSpace?.name || workspaces?.activeName || "Не выбрано"}</p><p className="text-muted-foreground text-xs">{activeSpace?.planType || "—"}</p></button>
					<button onClick={() => setSection("tools")} className="bg-card hover:bg-accent rounded-xl border p-4 text-left"><div className="flex items-center gap-2"><Wrench className="size-4" /><b>Инструменты</b><ChevronRight className="ml-auto size-4" /></div><p className="mt-3 text-sm">{status?.notcodeRunning ? "Запущены" : "Остановлены"}</p><p className="text-muted-foreground text-xs">{modules.length} MCP · {status?.tools ?? 0} инструментов</p></button>
				</div>
				<Card><div className="flex items-center justify-between"><div><p className="font-medium">Лимит активного пространства</p><p className="text-muted-foreground text-xs">{activeAccount?.email} · {activeSpace?.name}</p></div><Button variant="ghost" onClick={() => setSection("usage")}>Подробнее <ChevronRight className="size-4" /></Button></div><UsageMeter title="Rolling" window={usage?.rolling} /></Card>
				<p className="text-muted-foreground text-xs">Последнее обновление: {formatTime(loadedAt)}</p>
			</> : null}

			{section === "accounts" ? <>
				<SectionHeader title="Аккаунты" description="Все Notion-профили из подключённой браузерной сессии." action={<Button variant="brand" onClick={() => setAccountDialog(true)}><Plus className="size-4" />Добавить аккаунт</Button>} />
				<div className="space-y-3">{(workspaces?.accounts ?? []).map(account => <div key={account.userId} className="bg-card rounded-xl border p-4"><div className="flex items-center gap-4">{account.avatar ? <img src={account.avatar} alt="" className="size-11 rounded-full object-cover" /> : <div className="bg-muted grid size-11 place-items-center rounded-full font-semibold">{initials(account)}</div>}<div className="min-w-0 flex-1"><div className="flex items-center gap-2"><p className="truncate font-medium">{account.name || "Notion account"}</p>{account.active ? <Badge tone="ok">активен</Badge> : null}</div><p className="text-muted-foreground truncate text-sm">{account.email}</p><p className="text-muted-foreground mt-1 text-xs">{account.spaces.length} рабочих пространств · проверено {formatTime(loadedAt)}</p></div><div className="flex gap-2">{!account.active && account.spaces[0] ? <Button variant="secondary" onClick={() => void switchSpace(account, account.spaces[0])}>Сделать активным</Button> : null}<Button variant="ghost" onClick={() => { setAccountFilter(account.userId); setSection("workspaces") }}>Пространства</Button></div></div></div>)}{!workspaces?.accounts.length ? <EmptyState icon={Users} title="Нет подключённых аккаунтов" text="Добавьте Notion-аккаунт через безопасный мастер подключения." action={<Button variant="brand" onClick={() => setAccountDialog(true)}>Добавить аккаунт</Button>} /> : null}</div>
			</> : null}

			{section === "usage" ? <>
				<SectionHeader title="Лимиты и использование" description="Notion возвращает лимиты для активного рабочего пространства. Мы не показываем выдуманную историю." action={<Button variant="secondary" onClick={() => run("usage", async () => { setUsage(await api.aiUsage()); setLoadedAt(Date.now()) })}><RefreshCw className={cn("size-4", busy === "usage" && "animate-spin")} />Обновить</Button>} />
				<Card><div className="flex flex-wrap items-center gap-3"><div className="bg-muted grid size-10 place-items-center rounded-full font-medium">{initials(activeAccount)}</div><div className="min-w-0"><p className="truncate font-medium">{activeAccount?.name || "Аккаунт не выбран"}</p><p className="text-muted-foreground truncate text-xs">{activeAccount?.email} · {activeSpace?.name}</p></div><Badge className="ml-auto" tone={usage?.limitReached ? "bad" : "ok"}>{usage?.limitReached ? "исчерпан" : "в норме"}</Badge></div><Separator /><UsageMeter title="Rolling limit" window={usage?.rolling} /><UsageMeter title="Месячный период" window={usage?.monthly} fallbackUsed={usage?.premiumUsed} fallbackLimit={usage?.premiumLimit} /><p className="text-muted-foreground text-xs">Обновлено: {formatTime(loadedAt)}</p></Card>
			</> : null}

			{section === "workspaces" ? <>
				<SectionHeader title="Рабочие пространства" description="Здесь явно видно, через какой аккаунт доступно каждое пространство." action={<Button variant="secondary" onClick={() => run("spaces", async () => { setWorkspaces(await api.listWorkspaces()); setLoadedAt(Date.now()) })}><RefreshCw className="size-4" />Обновить</Button>} />
				<div className="flex gap-2"><div className="relative flex-1"><Search className="text-muted-foreground absolute left-2.5 top-2.5 size-4" /><Input value={workspaceQuery} onChange={e => setWorkspaceQuery(e.target.value)} placeholder="Поиск по названию" className="pl-8" /></div><Select value={accountFilter} onValueChange={setAccountFilter}><SelectTrigger className="w-64"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">Все аккаунты</SelectItem>{workspaces?.accounts.map(account => <SelectItem key={account.userId} value={account.userId}>{account.name || account.email}</SelectItem>)}</SelectContent></Select></div>
				<div className="overflow-hidden rounded-xl border">{workspaceRows.map(({ account, space }) => <div key={`${account.userId}:${space.id}`} className={cn("bg-card flex items-center gap-3 border-b p-3 last:border-b-0 transition-colors", space.active && usage?.limitReached && "border-red-500/30 bg-red-500/10")}><div className="bg-muted grid size-9 place-items-center rounded-lg">{space.icon || <Database className="size-4" />}</div><div className="min-w-0 flex-1"><div className="flex items-center gap-2"><p className="truncate text-sm font-medium">{space.name}</p>{space.active ? <Badge tone="ok">активно</Badge> : null}{space.isGuest ? <Badge>гость</Badge> : null}</div><p className="text-muted-foreground truncate text-xs">{account.name || account.email} · {space.planType || "план не указан"}</p></div><Button variant={space.active ? "ghost" : "secondary"} disabled={space.active || busy === `space:${space.id}`} onClick={() => void switchSpace(account, space)}>{space.active ? <Check className="size-4" /> : "Выбрать"}</Button></div>)}{!workspaceRows.length ? <div className="p-4"><EmptyState icon={Database} title="Ничего не найдено" text="Измените поиск или фильтр аккаунта." /></div> : null}</div>
			</> : null}

			{section === "ai" ? <>
				<SectionHeader title="AI и чат" description="Простые параметры сохраняются сразу. Для системного промпта используется отдельное сохранение." />
				<div className="divide-y overflow-hidden rounded-xl border bg-card"><label className="flex items-center gap-4 p-4"><Network className="text-muted-foreground size-5" /><span className="flex-1"><b className="text-sm">Искать во всех доступных источниках</b><span className="text-muted-foreground block text-xs">Ответ может быть полнее, но занять больше времени.</span></span><Switch checked={draft.searchAllSources} onCheckedChange={value => void patchSettings({ searchAllSources: value })} /></label><label className="flex items-center gap-4 p-4"><FileText className="text-muted-foreground size-5" /><span className="flex-1"><b className="text-sm">Отправлять по Enter</b><span className="text-muted-foreground block text-xs">Shift+Enter добавляет новую строку.</span></span><Switch checked={draft.sendWithEnter} onCheckedChange={value => void patchSettings({ sendWithEnter: value })} /></label></div>
				<Card><div><Label>Модель</Label><Select value={draft.model} onValueChange={value => void patchSettings({ model: value })}><SelectTrigger className="mt-2"><SelectValue placeholder="Выберите модель" /></SelectTrigger><SelectContent>{models.map(model => <SelectItem key={model.id} value={model.id}>{model.label}</SelectItem>)}</SelectContent></Select></div></Card>
				<Card><div className="flex items-center justify-between"><div><p className="font-medium">Системный промпт</p><p className="text-muted-foreground text-xs">Добавляется к вашим сообщениям, если переключатель включён.</p></div>{dirtyPrompt ? <Badge tone="brand">есть изменения</Badge> : <Badge tone="ok">сохранено</Badge>}</div><Textarea value={draft.systemPrompt} onChange={e => setDraft(current => ({ ...current, systemPrompt: e.target.value }))} className="min-h-56 font-mono text-xs" /><div className="flex items-center gap-3"><Switch checked={draft.autoPrependMcp} onCheckedChange={value => setDraft(current => ({ ...current, autoPrependMcp: value }))} /><span className="text-sm">Добавлять к сообщениям</span><span className="text-muted-foreground ml-auto text-xs">{draft.systemPrompt.length} символов</span></div><div className="flex justify-end gap-2"><Button variant="secondary" onClick={() => run("default-prompt", async () => { const prompt = await api.defaultSystemPrompt(); setDraft(current => ({ ...current, systemPrompt: prompt })) })}>Сбросить</Button><Button variant="brand" disabled={!dirtyPrompt || busy === "save-prompt"} onClick={() => run("save-prompt", async () => saveSettings(draft, "Системный промпт сохранён"))}>Сохранить промпт</Button></div></Card>
			</> : null}

			{section === "tools" ? <>
				<SectionHeader title="Инструменты и интеграции" description="Пользовательские функции отделены от сетевой и отладочной конфигурации." />
				<div className="bg-muted inline-flex w-fit gap-1 rounded-lg p-1">{TOOL_TABS.map(item => <button key={item.key} onClick={() => { setToolTab(item.key); if (item.key === "catalog" && !market.length) void loadCatalog() }} className={cn("rounded-md px-3 py-1.5 text-sm", toolTab === item.key ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}>{item.label}</button>)}</div>
				{toolTab === "builtin" && !ngrok?.configured ? <Card className="border-amber-500/30 bg-amber-500/5"><div className="flex items-center justify-between gap-3"><div><p className="font-medium">Сначала подключите ngrok</p><p className="text-muted-foreground text-xs">Без authtoken Notion не сможет обратиться к встроенным инструментам.</p></div><Badge tone="bad">нужен ключ</Badge></div><div className="flex gap-2"><Input type="password" value={ngrokToken} onChange={e => setNgrokToken(e.target.value)} placeholder="ngrok authtoken" /><Button variant="brand" disabled={!ngrokToken.trim()} onClick={() => run("ngrok-save", async () => { setNgrok(await api.saveNgrokToken(ngrokToken)); setNgrokToken(""); onToast("ngrok подключён") })}>Сохранить</Button></div><p className="text-muted-foreground text-xs">Нажмите «?» на стартовом экране, чтобы увидеть короткую инструкцию получения ключа.</p></Card> : null}
				{toolTab === "builtin" ? <Card><div className="flex items-start gap-4"><div className={cn("mt-1 size-3 rounded-full", status?.notcodeRunning ? "bg-emerald-500" : "bg-muted-foreground")} /><div className="flex-1"><p className="font-medium">Встроенные инструменты</p><p className="text-muted-foreground text-sm">Файлы, терминал и git для работы агента.</p><p className="text-muted-foreground mt-2 text-xs">{status?.notcodeRunning ? `Запущено · ${status.tools ?? 0} инструментов` : "Остановлено"}</p></div><div className="flex gap-2"><Button variant="brand" disabled={busy === "tools-start"} onClick={() => run("tools-start", async () => { const result = await api.connectNotcodeMcp(); setStatus(result.status); setModules(await api.mcpList()); onToast("Инструменты запущены") })}>{status?.notcodeRunning ? "Перезапустить" : "Запустить"}</Button>{status?.notcodeRunning ? <Button variant="secondary" onClick={() => run("tools-stop", async () => setStatus(await api.notcodeStop()))}>Остановить</Button> : null}</div></div><Separator /><label className="flex items-center justify-between text-sm"><span>Запускать автоматически</span><Switch checked={draft.autoStartNotcode} onCheckedChange={value => void patchSettings({ autoStartNotcode: value })} /></label>{status?.error ? <p className="text-destructive text-sm">{status.error}</p> : null}</Card> : null}
				{toolTab === "servers" ? <><div className="flex justify-end"><Button variant="brand" onClick={() => setServerDialog(true)}><Plus className="size-4" />Добавить сервер</Button></div>{modules.length ? <div className="overflow-hidden rounded-xl border">{modules.map(module => <div key={module.integrationId} className="bg-card flex items-center gap-3 border-b p-4 last:border-b-0"><Server className="text-muted-foreground size-5" /><div className="min-w-0 flex-1"><div className="flex items-center gap-2"><p className="font-medium">{module.name}</p><Badge tone={module.enabled ? "ok" : "muted"}>{module.enabled ? "включён" : "выключен"}</Badge></div><p className="text-muted-foreground truncate text-xs">{module.serverUrl.replace(/([?&](token|key|secret)=)[^&]+/gi, "$1[скрыто]")}</p></div><Badge>{module.toolCount} tools</Badge><Switch checked={module.enabled} onCheckedChange={enabled => run(`toggle:${module.integrationId}`, async () => { await api.mcpSetEnabled(module.integrationId, enabled); setModules(await api.mcpList()) })} /><Button variant="subtle" size="icon" aria-label={`Удалить ${module.name}`} onClick={() => { if (window.confirm(`Удалить MCP-сервер «${module.name}»?`)) void run(`remove:${module.integrationId}`, async () => { await api.mcpDisconnect(module.integrationId); setModules(await api.mcpList()); onToast("Сервер удалён") }) }}><Trash2 className="size-4" /></Button></div>)}</div> : <EmptyState icon={Server} title="Нет MCP-серверов" text="Добавьте собственный сервер или установите интеграцию из каталога." />}</> : null}
				{toolTab === "catalog" ? <><div className="space-y-3"><p className="text-sm font-medium">Готовые серверы</p><div className="grid gap-3 md:grid-cols-2">{MCP_PRESETS.map(preset => { const installed = modules.some(module => (module.serverUrl || "").split("?")[0] === preset.url); return <div key={preset.name} className="bg-card rounded-xl border p-4"><div className="flex gap-3"><Plug className="text-muted-foreground size-5" /><div className="min-w-0 flex-1"><p className="font-medium">{preset.title}</p><p className="text-muted-foreground mt-1 text-xs">{preset.hint}</p><p className="text-muted-foreground mt-1 truncate font-mono text-[11px]">{preset.url}</p></div></div><Button className="mt-4 w-full" variant={installed ? "secondary" : "brand"} disabled={installed} onClick={() => { if (preset.needsKey) { setNewServer({ name: preset.title, url: preset.url, token: "", query: preset.needsKey === "query" ? "api_key=&profile=" : "", headers: preset.needsKey === "header" ? "CONTEXT7_API_KEY: " : "", autoRun: true }); setServerDialog(true); return } void run(`preset:${preset.name}`, async () => { await api.mcpConnect({ name: preset.title, serverUrl: preset.url, autoRun: true, runWriteToolsAutomatically: true }); setModules(await api.mcpList()); onToast(`${preset.title} подключён`) }) }}>{installed ? "Установлено" : preset.needsKey ? "Настроить и установить" : "Установить"}</Button></div> })}</div></div><div className="flex gap-2"><Input value={marketQuery} onChange={e => setMarketQuery(e.target.value)} onKeyDown={e => { if (e.key === "Enter") void loadCatalog() }} placeholder="GitHub, Figma, базы данных…" /><Button variant="secondary" onClick={() => void loadCatalog()}><Search className="size-4" />Найти</Button></div>{marketError ? <p className="text-destructive text-sm">Не удалось загрузить каталог: {marketError}</p> : null}<div className="grid gap-3 md:grid-cols-2">{market.map(item => { const installed = modules.some(module => module.serverUrl === item.serverUrl); return <div key={item.serverUrl || item.name} className="bg-card rounded-xl border p-4"><div className="flex gap-3"><Plug className="text-muted-foreground size-5" /><div className="min-w-0 flex-1"><p className="font-medium">{item.title || item.name}</p><p className="text-muted-foreground mt-1 line-clamp-2 text-xs">{item.description || "MCP integration"}</p></div></div><Button className="mt-4 w-full" variant={installed ? "secondary" : "brand"} disabled={installed || !item.serverUrl} onClick={() => run(`install:${item.name}`, async () => { await api.mcpConnect({ name: item.title || item.name, serverUrl: item.serverUrl! }); setModules(await api.mcpList()); onToast("Интеграция установлена") })}>{installed ? "Установлено" : "Установить"}</Button></div> })}</div>{busy === "catalog" ? <div className="flex items-center justify-center py-8"><Loader2 className="size-5 animate-spin" /></div> : null}</> : null}
				{toolTab === "developer" ? <div className="space-y-4"><Card><button className="flex items-center text-left" onClick={() => setRevealDeveloper(value => !value)}><Terminal className="mr-2 size-4" /><span className="font-medium">Технические сведения</span>{revealDeveloper ? <EyeOff className="ml-auto size-4" /> : <Eye className="ml-auto size-4" />}</button>{revealDeveloper ? <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 font-mono text-xs"><dt className="text-muted-foreground">NotCode</dt><dd className="break-all">{status?.localUrl || "—"}</dd><dt className="text-muted-foreground">Public MCP</dt><dd className="break-all">{status?.mcpUrl || "—"}</dd><dt className="text-muted-foreground">PID</dt><dd>{status?.notcodePid || "—"}</dd><dt className="text-muted-foreground">Mode</dt><dd>{status?.mode || "—"}</dd><dt className="text-muted-foreground">Config</dt><dd className="break-all">{status?.configFile || "—"}</dd><dt className="text-muted-foreground">Bearer</dt><dd>••••••••••••••••</dd></dl> : null}</Card></div> : null}
			</> : null}

			{section === "appearance" ? <><SectionHeader title="Внешний вид" description="Изменения применяются и сохраняются сразу." /><div className="divide-y overflow-hidden rounded-xl border bg-card"><div className="flex items-center gap-4 p-4"><Palette className="text-muted-foreground size-5" /><div className="flex-1"><p className="text-sm font-medium">Тема</p><p className="text-muted-foreground text-xs">Оформление интерфейса приложения.</p></div><Select value={draft.theme} onValueChange={value => void patchSettings({ theme: value as Settings["theme"] })}><SelectTrigger className="w-44"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="notion-dark">Тёмная</SelectItem><SelectItem value="notion-light">Светлая</SelectItem></SelectContent></Select></div><div className="p-4"><div className="mb-3 flex items-center"><span className="text-sm font-medium">Масштаб интерфейса</span><span className="text-muted-foreground ml-auto text-sm tabular-nums">{draft.fontScale}%</span><Button className="ml-2" size="sm" variant="ghost" onClick={() => void patchSettings({ fontScale: 100 })}>100%</Button></div><Slider min={85} max={125} step={5} value={[draft.fontScale]} onValueCommit={([value]) => void patchSettings({ fontScale: value })} onValueChange={([value]) => setDraft(current => ({ ...current, fontScale: value }))} /></div><label className="flex items-center gap-4 p-4"><Activity className="text-muted-foreground size-5" /><span className="flex-1"><b className="text-sm">Уменьшить движение</b><span className="text-muted-foreground block text-xs">Отключает декоративные переходы и анимации.</span></span><Switch checked={draft.motionOff} onCheckedChange={value => void patchSettings({ motionOff: value })} /></label></div></> : null}

			{section === "advanced" ? <><SectionHeader title="Дополнительно" description="Диагностика, экспорт и опасные действия." action={<Button variant="secondary" onClick={exportDiagnostics}><Download className="size-4" />Экспорт отчёта</Button>} /><Card><div className="flex flex-wrap items-center gap-2"><p className="font-medium">Журнал приложения</p><Select value={logSource} onValueChange={setLogSource}><SelectTrigger className="ml-auto w-40"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">Все источники</SelectItem><SelectItem value="notcode">NotCode</SelectItem><SelectItem value="ngrok">ngrok</SelectItem><SelectItem value="app">Приложение</SelectItem></SelectContent></Select><Button variant="secondary" size="sm" onClick={() => setLogsPaused(value => !value)}>{logsPaused ? "Продолжить" : "Пауза"}</Button><Button variant="ghost" size="sm" onClick={() => api.notcodeLogs().then(setLogs)}>Обновить</Button></div><Input value={logQuery} onChange={e => setLogQuery(e.target.value)} placeholder="Поиск в журнале" /><div className="max-h-80 overflow-auto rounded-lg border">{filteredLogs.length ? filteredLogs.slice(-300).reverse().map((line, index) => <div key={`${line.time}:${index}`} className="grid grid-cols-[80px_70px_1fr] gap-2 border-b px-3 py-2 font-mono text-[11px] last:border-b-0"><span className="text-muted-foreground">{formatTime(line.time).split(", ").pop()}</span><span>{line.source}</span><span className={cn("break-all", line.level === "error" && "text-red-500", line.level === "warn" && "text-amber-500")}>{redact(line.text)}</span></div>) : <p className="text-muted-foreground p-4 text-sm">Записей нет.</p>}</div></Card><Card><div><p className="font-medium">HTTP-диагностика</p><p className="text-muted-foreground text-xs">{debug.length} запросов · User {connection?.userId || "—"} · Space {connection?.spaceId || "—"}</p></div><div className="max-h-72 overflow-auto rounded-lg border">{debug.slice().reverse().slice(0, 150).map(entry => <details key={entry.id} className="border-b last:border-b-0"><summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-xs"><Badge tone={entry.error ? "bad" : entry.status && entry.status < 400 ? "ok" : "muted"}>{entry.status || "—"}</Badge><span className="font-mono">{entry.method}</span><span className="min-w-0 flex-1 truncate font-mono">{entry.path}</span><span className="text-muted-foreground">{entry.durationMs || 0} ms</span></summary><pre className="bg-background max-h-48 overflow-auto border-t p-3 text-[11px] whitespace-pre-wrap">{redact(entry.error || entry.responsePreview || "Нет деталей")}</pre></details>)}</div></Card><div className="border-destructive/30 rounded-xl border p-4"><p className="font-medium text-destructive">Опасная зона</p><p className="text-muted-foreground mt-1 text-xs">Эти действия удаляют локальные данные подключения или журналы.</p><div className="mt-4 flex gap-2"><Button variant="secondary" onClick={() => run("clear-logs", async () => { await api.notcodeClearLogs(); await api.clearDebugLogs(); setLogs([]); setDebug([]); onToast("Диагностика очищена") })}>Очистить журналы</Button><Button variant="destructive" onClick={() => { if (window.confirm("Удалить сохранённую сессию Notion? Потребуется подключить аккаунт заново.")) void run("clear-session", async () => { await api.clearSession(); onConnectionChange(await api.connectionStatus()); setWorkspaces(null); onToast("Сессия удалена") }) }}>Сбросить подключение</Button></div></div></> : null}

			{section === "about" ? <><SectionHeader title="О приложении" description="Neura — desktop-клиент для общения с Notion AI и подключёнными инструментами." /><Card><div className="flex items-center gap-4"><div className="bg-foreground text-background grid size-12 place-items-center rounded-xl"><Bot className="size-6" /></div><div><p className="text-lg font-semibold">Neura</p><p className="text-muted-foreground text-sm">Desktop application</p></div></div><Separator /><Button variant="secondary" onClick={() => void api.openURL("https://" + "www.notion.so")}>Открыть Notion <ExternalLink className="size-4" /></Button></Card></> : null}
		</main></ScrollArea></div>

		<Dialog open={accountDialog} onOpenChange={setAccountDialog}><DialogContent className="max-w-2xl p-0"><div className="border-b p-5"><DialogTitle>Добавить Notion-аккаунт</DialogTitle><DialogDescription>Сессия импортируется из запроса Notion и хранится локально.</DialogDescription></div><div className="space-y-4 p-5"><ol className="space-y-2 text-sm"><li><b>1.</b> Откройте Notion в браузере и отправьте сообщение Notion AI.</li><li><b>2.</b> В DevTools → Network найдите <code>runInferenceTranscript</code>.</li><li><b>3.</b> Copy as cURL и вставьте результат ниже.</li></ol><Textarea value={curl} onChange={e => setCurl(e.target.value)} className="min-h-44 font-mono text-xs" placeholder="Вставьте Copy as cURL из DevTools" /><p className="text-muted-foreground text-xs">Cookie и заголовки не отображаются после импорта и не попадают в диагностику.</p><div className="flex justify-end gap-2"><Button variant="secondary" onClick={() => setAccountDialog(false)}>Отмена</Button><Button variant="brand" disabled={!curl.trim() || busy === "import-account"} onClick={() => run("import-account", async () => { const next = await api.importCurl(curl); onConnectionChange(next); setWorkspaces(await api.listWorkspaces()); setCurl(""); setAccountDialog(false); onToast("Аккаунт подключён") })}>Проверить и подключить</Button></div></div></DialogContent></Dialog>
		<Dialog open={serverDialog} onOpenChange={setServerDialog}><DialogContent className="max-w-lg p-0"><div className="border-b p-5"><DialogTitle>Добавить MCP-сервер</DialogTitle><DialogDescription>Секрет сохраняется через существующий защищённый контур подключения.</DialogDescription></div><div className="space-y-4 p-5"><div><Label>Название</Label><Input className="mt-2" value={newServer.name} onChange={e => setNewServer(current => ({ ...current, name: e.target.value }))} /></div><div><Label>URL</Label><Input className="mt-2" value={newServer.url} onChange={e => setNewServer(current => ({ ...current, url: e.target.value }))} placeholder="https://example.com/mcp" /></div><div><Label>Токен, если нужен</Label><Input className="mt-2" type="password" autoComplete="off" value={newServer.token} onChange={e => setNewServer(current => ({ ...current, token: e.target.value }))} /></div><div><Label>Query-параметры</Label><Input className="mt-2" value={newServer.query} onChange={e => setNewServer(current => ({ ...current, query: e.target.value }))} placeholder="api_key=...&profile=..." /><p className="text-muted-foreground mt-1 text-xs">Smithery и подобные хостинги требуют ключ прямо в адресе.</p></div><div><Label>Доп. заголовки</Label><Input className="mt-2" value={newServer.headers} onChange={e => setNewServer(current => ({ ...current, headers: e.target.value }))} placeholder="X-API-Key: abc; X-Profile: main" /></div><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={newServer.autoRun} onChange={e => setNewServer(current => ({ ...current, autoRun: e.target.checked }))} />Разрешить запуск инструментов автоматически</label><div className="flex justify-end gap-2"><Button variant="secondary" onClick={() => setServerDialog(false)}>Отмена</Button><Button variant="brand" disabled={!newServer.name.trim() || !newServer.url.trim()} onClick={() => run("add-server", async () => { await api.mcpConnect({ name: newServer.name.trim(), serverUrl: newServer.url.trim(), token: newServer.token.trim() || undefined, query: parsePairs(newServer.query, "="), headers: parsePairs(newServer.headers, ":"), autoRun: newServer.autoRun, runWriteToolsAutomatically: newServer.autoRun }); setModules(await api.mcpList()); setNewServer({ name: "", url: "", token: "", query: "", headers: "", autoRun: true }); setServerDialog(false); onToast("MCP-сервер добавлен") })}>Проверить и добавить</Button></div></div></DialogContent></Dialog>
	</div>
}
