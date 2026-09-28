import { useCallback, useEffect, useRef, useState } from "react"
import { api, errText, onChatEvent, type UploadedAttachment } from "../shared/api/api"
import type {
	AIUsage,
	ChatEvent,
	ConnectionState,
	Model,
	Part,
	Settings,
	StoredMessage,
	Thread,
	ToolPart,
	Turn,
} from "../shared/model/types"
import { cn } from "../shared/lib/utils"
import { onToastEvent } from "../shared/lib/toast"
import type { Artifact } from "../features/artifacts/model/artifacts"
import { artifactKind } from "../features/artifacts/model/artifacts"
import type { ComputerFile } from "../shared/lib/toolOutput"
import ArtifactPanel from "../features/artifacts/components/ArtifactPanel"
import Composer, { type Attachment } from "../features/chat/components/Composer"
import EmptyState from "../features/chat/components/EmptyState"
import Onboarding from "../features/onboarding/components/Onboarding"
import { AssistantMessage, surveysFromTools, UserMessage } from "../features/chat/components/Message"
import SettingsModal from "../features/settings/components/SettingsModal"
import Sidebar, { SIDEBAR_DEFAULT, SIDEBAR_MAX, SIDEBAR_MIN } from "../features/navigation/components/Sidebar"
import TitleBar from "../features/navigation/components/TitleBar"
import { ScrollArea } from "../shared/ui"

const DEFAULTS: Settings = {
	model: "",
	reasoningEffort: "",
	systemPrompt: "",
	autoPrependMcp: true,
	sendWithEnter: true,
	// «All sources I can access» выключено по умолчанию.
	searchAllSources: false,
	// Beta V2 is the stable agent workflow; legacy remains available explicitly.
	chatMode: "v2",
	browserEnabled: true,
	skipApprovals: true,
	useMemories: true,
	excludeChatFromMemories: false,
	suggestedEdits: true,
	theme: "notion-dark",
	motionOff: false,
	fontScale: 100,
	notcodeDir: "",
	notcodeCmd: "bun",
	notcodePort: 3000,
	ngrokPath: "ngrok",
	ngrokDomain: "",
	mcpServerName: "notcode",
	autoStartNotcode: true,
	rotateNotcodeToken: false,
	mcpIntegrationId: "",
	mcpServerUrl: "",
	mcpPaths: {},
	activeUserId: "",
	activeSpaceId: "",
	activeSpaceViewId: "",
	activeSpaceName: "",
}

// Что имеет смысл открывать в правой панели-браузере, а не скачивать.
const PREVIEW_EXT = new Set([
	".html",
	".htm",
	".svg",
	".md",
	".txt",
	".json",
	".csv",
	".js",
	".jsx",
	".ts",
	".tsx",
	".css",
	".py",
	".go",
	".yaml",
	".yml",
	".xml",
	".sql",
	".sh",
])

/** Сохраняет base64 на диск через обычную ссылку-скачивание. */
function saveBase64(dataBase64: string, fileName: string, mime: string) {
	const bytes = Uint8Array.from(atob(dataBase64), (c) => c.charCodeAt(0))
	const url = URL.createObjectURL(new Blob([bytes], { type: mime || "application/octet-stream" }))
	const link = document.createElement("a")
	link.href = url
	link.download = fileName
	link.click()
	setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function normalizeChatMode(raw: string | undefined): Settings["chatMode"] | null {
	switch ((raw ?? "").toLowerCase()) {
		case "agent":
		case "agent_service":
		case "agent-service":
		case "v3": return "v3"
		case "workflow":
		case "workflow_v2":
		case "workflow-v2":
		case "v2": return "v2"
		case "legacy": return "legacy"
		default: return null
	}
}

function cleanThreadTitle(raw: string) {
	let title = raw.trim()
	try {
		const parsed = JSON.parse(title) as unknown
		if (typeof parsed === "string") title = parsed
		else if (parsed && typeof parsed === "object") {
			const record = parsed as Record<string, unknown>
			if (typeof record.title === "string") title = record.title
			else if (typeof record.name === "string") title = record.name
		}
	} catch { /* обычная строка */ }
	return title.replace(/^#+\s*/, "").replace(/^["'`]+|["'`]+$/g, "").replace(/\s+/g, " ").trim().slice(0, 80)
}

function parseParts(raw: string | undefined, content: string): Part[] {
	if (raw) {
		try {
			const parsed = JSON.parse(raw)
			if (Array.isArray(parsed) && parsed.length > 0) return parsed as Part[]
		} catch {
			/* fall through to plain text */
		}
	}
	return content ? [{ kind: "text", text: content }] : []
}

/** История из SQLite/Notion → реплики для рендера. */
function toTurns(messages: StoredMessage[] | null | undefined): Turn[] {
	return (messages ?? []).map((m) => ({
		id: m.id,
		role: m.role,
		content: m.content,
		parts: parseParts(m.parts, m.content),
		createdAt: m.createdAt,
		streaming: false,
	}))
}

type RevealState = {
	queue: string
	raf: number
	lastFrame: number
	charsPerMs: number
	carry: number
	// Весь текст, который уже взят в работу (показан или в очереди).
	// Нужен, чтобы повторно присланный сервером кусок не проявлялся второй раз.
	seen: string
}

/**
 * Сколько из chunk уже есть в конце seen. Возвращает только новую часть.
 * Короткие совпадения не считаем дублем: «но но» или «и и» вполне легальны.
 */
/** Максимальное отставание анимации от реального потока Notion. */
const CATCH_UP_MS = 900
/** Потолок скорости — иначе проявление превращается в рывок. */
const MAX_CHARS_PER_MS = 3.5
/** Больше этого в очереди не держим: лишнее показываем сразу. */
const MAX_BACKLOG = 1400

function dropRepeat(seen: string, chunk: string): string {
	if (!chunk) return ""
	if (chunk.length >= 8 && seen.endsWith(chunk)) return ""
	const max = Math.min(chunk.length, seen.length)
	for (let k = max; k >= 16; k--) {
		if (seen.endsWith(chunk.slice(0, k))) return chunk.slice(k)
	}
	return chunk
}

export default function App() {
	const [settings, setSettings] = useState<Settings>(DEFAULTS)
	const [connection, setConnection] = useState<ConnectionState | null>(null)
	const [models, setModels] = useState<Model[]>([])
	const [usage, setUsage] = useState<AIUsage | null>(null)
	const [threads, setThreads] = useState<Thread[]>([])
	const [threadId, setThreadId] = useState("")
	const [turns, setTurns] = useState<Turn[]>([])
	const [input, setInput] = useState("")
	// Фотка открывается на весь экран, а не в панели-браузере.
	const [imageView, setImageView] = useState<{ name: string; src: string } | null>(null)
	// Отвеченные опросники больше не показываем в инпуте.
	const [answeredSurveys, setAnsweredSurveys] = useState<Set<string>>(() => new Set())
	// Запросы отслеживаются по чатам: можно открыть новый чат и писать в нём,
	// пока предыдущий продолжает работать в фоне.
	const [runningThreads, setRunningThreads] = useState<Set<string>>(() => new Set())
	const runningThreadsRef = useRef(new Set<string>())
	const [showSettings, setShowSettings] = useState(false)
	// С какого раздела открывать настройки (сайдбар ведёт сразу к участникам).
	const [settingsSection, setSettingsSection] = useState<string | undefined>()
	const [sidebarOpen, setSidebarOpen] = useState(true)
	// Ширина сайдбара тянется мышью и запоминается между запусками.
	const [sidebarWidth, setSidebarWidth] = useState(() => {
		const raw = Number(localStorage.getItem("neura:sidebarWidth"))
		if (!Number.isFinite(raw) || raw <= 0) return SIDEBAR_DEFAULT
		return Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, raw))
	})
	// Пока тянем сайдбар, анимации ширины выключены везде сразу,
	// иначе шапка окна едет за сайдбаром с заметной задержкой.
	const [resizing, setResizing] = useState(false)
	const [toast, setToast] = useState<{ msg: string; error?: boolean } | null>(null)
	const [atBottom, setAtBottom] = useState(true)
	// Открытый артефакт показывается в правой панели-браузере.
	const [artifact, setArtifact] = useState<Artifact | null>(null)
	// Файл шага, который сейчас открыт в правой панели.
	const [activeFile, setActiveFile] = useState("")
	// Онбординг можно закрыть кнопкой «Далее», даже если ключей ещё нет.
	const [onboardingDone, setOnboardingDone] = useState(false)

	useEffect(() => {
		try {
			localStorage.setItem("neura:sidebarWidth", String(sidebarWidth))
		} catch {
			/* приватный режим — не критично */
		}
	}, [sidebarWidth])

	const scroller = useRef<HTMLDivElement>(null)
	const assistantIds = useRef(new Map<string, string>())
	const threadIdRef = useRef("")
	const turnsRef = useRef<Turn[]>([])
	const threadCache = useRef(new Map<string, Turn[]>())
	const openSequence = useRef(0)
	// Таймеры опроса чатов, которые генерируются в Notion без нашего стрима.
	const remoteWatchers = useRef(new Map<string, number>())
	const syncing = useRef(new Set<string>())
	// Чаты, в которых прямо сейчас идёт НАША отправка: от загрузки файлов до
	// конца плавного проявления ответа. Пока чат здесь, сверка с Notion не
	// имеет права переписывать историю — снапшот записей может отставать
	// и не содержать только что отправленного сообщения.
	const localBusy = useRef(new Set<string>())
	const editingThreads = useRef(new Set<string>())
	const [hasEarlier, setHasEarlier] = useState(false)
	// Входящие NDJSON-дельты могут быть огромными. Держим отдельную очередь на
	// каждый параллельный чат и проявляем её короткими порциями, а не вставляем
	// в DOM одним блоком.
	const revealStates = useRef(new Map<string, RevealState>())
	const streamStartedAt = useRef(new Map<string, number>())
	// The chat used to read a stale `connection` snapshot, which is why sending
	// right after a cURL import still said "нет сессии".
	const connectedRef = useRef(false)

	useEffect(() => { threadIdRef.current = threadId }, [threadId])
	useEffect(() => { turnsRef.current = turns }, [turns])
	const markRunning = useCallback((id: string, running: boolean) => {
		const next = new Set(runningThreadsRef.current)
		if (running) next.add(id)
		else next.delete(id)
		runningThreadsRef.current = next
		setRunningThreads(next)
	}, [])

	const updateThreadTurns = useCallback((targetThreadId: string, update: (current: Turn[]) => Turn[]) => {
		const current = threadCache.current.get(targetThreadId) ?? (threadIdRef.current === targetThreadId ? turnsRef.current : [])
		const next = update(current)
		threadCache.current.set(targetThreadId, next)
		if (threadIdRef.current === targetThreadId) {
			turnsRef.current = next
			setTurns(next)
		}
		return next
	}, [])

	const notify = useCallback((msg: string, error?: boolean) => {
		setToast({ msg, error })
		window.setTimeout(() => setToast(null), error ? 6000 : 2500)
	}, [])

	const refreshUsage = useCallback(async () => {
		try {
			const next = await api.aiUsage()
			setUsage(next)
			return next
		} catch {
			return null
		}
	}, [])

	// Тосты из компонентов без onToast — например, копирование адреса
	// сноски правой кнопкой в теле ответа.
	useEffect(
		() => onToastEvent(({ message, error }) => notify(message, error)),
		[notify],
	)

	const applyConnection = useCallback((state: ConnectionState | null) => {
		setConnection(state)
		connectedRef.current = !!state?.connected
		const captureMode = normalizeChatMode(state?.captureMode)
		if (captureMode) {
			setSettings((current) => current.chatMode === captureMode
				? current
				: { ...current, chatMode: captureMode, model: "", reasoningEffort: "" })
		}
		if (state?.connected) {
			api.listModels()
				.then((m) => setModels(m ?? []))
				.catch(() => {})
		} else {
			setModels([])
		}
	}, [])

	useEffect(() => {
		if (!connection?.connected) {
			setUsage(null)
			return
		}
		void refreshUsage()
	}, [connection?.connected, connection?.spaceId, refreshUsage])

	// ---- theme / motion / font scale -------------------------------------
	useEffect(() => {
		const root = document.documentElement
		root.classList.toggle("dark", settings.theme !== "notion-light")
		root.dataset.motion = settings.motionOff ? "off" : "on"
		root.style.fontSize = `${settings.fontScale}%`
	}, [settings.theme, settings.motionOff, settings.fontScale])

	// ---- bootstrap -------------------------------------------------------
	useEffect(() => {
		void (async () => {
			// Подключение и настройки восстанавливаем независимо от сетевой
			// синхронизации списка чатов. Раньше ошибка listThreads отклоняла общий
			// Promise.all, applyConnection не вызывался и сохранённая cURL-сессия
			// после перезапуска выглядела как потерянная.
			try {
				const [s, conn] = await Promise.all([
					api.getSettings(),
					api.connectionStatus(),
				])
				setSettings({ ...DEFAULTS, ...s, mcpPaths: s.mcpPaths ?? {} })
				applyConnection(conn)
			} catch (e) {
				notify(errText(e), true)
				return
			}
			try {
				const list = await api.listThreads()
				setThreads(list ?? [])
				if (list?.length) await openThread(list[0].id)
			} catch (e) {
				// Сессия уже восстановлена; проблема синхронизации истории не должна
				// возвращать пользователя на экран подключения.
				notify(`Не удалось синхронизировать чаты: ${errText(e)}`, true)
			}
		})()
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [])

	// ---- плавный вывод текста ------------------------------------------
	const appendVisibleText = useCallback((targetThreadId: string, chunk: string) => {
		if (!chunk || !targetThreadId) return
		const assistantId = assistantIds.current.get(targetThreadId)
		if (!assistantId) return
		updateThreadTurns(targetThreadId, (prev) => {
			const next = [...prev]
			const idx = next.findIndex((t) => t.id === assistantId)
			if (idx < 0) return prev
			const turn = { ...next[idx], parts: [...next[idx].parts] }
			const i = turn.parts.length - 1
			if (i >= 0 && turn.parts[i].kind === "text") {
				const p = turn.parts[i] as { kind: "text"; text: string }
				turn.parts[i] = { kind: "text", text: p.text + chunk } as Part
			} else {
				turn.parts.push({ kind: "text", text: chunk } as Part)
			}
			next[idx] = turn
			return next
		})
	}, [updateThreadTurns])

	const appendText = useCallback((targetThreadId: string, chunk: string) => {
		if (!chunk || !targetThreadId) return
		let state = revealStates.current.get(targetThreadId)
		if (!state) {
			// Первый крупный блок проявляется примерно за 0.9–1.5 секунды. Если
			// модель долго думала до первой дельты, используем это время как темп,
			// но ограничиваем его, чтобы ответ не тянулся бесконечно.
			const waited = Date.now() - (streamStartedAt.current.get(targetThreadId) ?? Date.now())
			const targetMs = Math.min(1800, Math.max(1100, waited || 1300))
			state = {
				queue: "",
				seen: "",
				raf: 0,
				lastFrame: performance.now(),
				// Первый блок проявляется целиком за targetMs, но не быстрее ~0.6
				// символа за миллисекунду — иначе побуквенности уже не видно.
				charsPerMs: Math.min(0.6, Math.max(0.05, chunk.length / targetMs)),
				carry: 0,
			}
			revealStates.current.set(targetThreadId, state)
		}
		// Защита от рассинхрона: Notion иногда присылает уже показанный кусок
		// текста ещё раз (синхронизация снимка) — второй раз не проявляем.
		const fresh = dropRepeat(state.seen, chunk)
		if (!fresh) return
		state.seen = (state.seen + fresh).slice(-4000)
		state.queue += fresh
		if (state.raf) return

		const tick = (now: number) => {
			const current = revealStates.current.get(targetThreadId)
			if (!current) return
			const elapsed = Math.min(80, now - current.lastFrame)
			// ~45 FPS: буквы выходят заметно плавнее, но Markdown всё ещё не
			// переразбирается на каждом кадре монитора.
			if (elapsed < 22) {
				current.raf = requestAnimationFrame(tick)
				return
			}
			current.lastFrame = now
			// Синхрон с Notion. Главное правило: отставание от пришедшего текста
			// никогда не больше CATCH_UP_MS. Раньше темп был почти фиксирован,
			// и на длинном ответе приложение отставало от Notion на десятки секунд.
			const needed = current.queue.length / CATCH_UP_MS
			const speed = Math.min(MAX_CHARS_PER_MS, Math.max(current.charsPerMs, needed))
			const budget = current.carry + elapsed * speed
			let take = Math.floor(budget)
			current.carry = budget - take
			// Аварийный догон: если в очереди уже огромный хвост (модель ушла
			// вперёд на несколько абзацев), отдаём лишнее сразу и проявляем
			// побуквенно только последние MAX_BACKLOG символов.
			if (current.queue.length > MAX_BACKLOG) {
				const dump = current.queue.length - MAX_BACKLOG
				appendVisibleText(targetThreadId, current.queue.slice(0, dump))
				current.queue = current.queue.slice(dump)
			}
			if (take > 0 && current.queue) {
				take = Math.min(current.queue.length, Math.max(1, take))
				// Проявляем посимвольно: хвост с градиентом и блюром рисует CSS,
				// поэтому «рубленого» typewriter-эффекта не видно.
				// Markdown-разметку (**, ```, теги) проскакиваем целиком, иначе в
				// кадре мелькают звёздочки и половинки тегов.
				if (take < current.queue.length) {
					const limit = Math.min(current.queue.length, take + 48)
					while (take < limit && /[*_~`<>[\]()|#\\]/.test(current.queue[take])) take++
				}
				// Не режем surrogate pair посередине.
				if (take < current.queue.length && /[\uD800-\uDBFF]/.test(current.queue[take - 1])) take++
				const visible = current.queue.slice(0, take)
				current.queue = current.queue.slice(take)
				appendVisibleText(targetThreadId, visible)
			}
			if (current.queue) current.raf = requestAnimationFrame(tick)
			else current.raf = 0
		}
		state.raf = requestAnimationFrame(tick)
	}, [appendVisibleText])

	async function waitForReveal(targetThreadId: string) {
		const deadline = Date.now() + 12_000
		while (Date.now() < deadline) {
			const state = revealStates.current.get(targetThreadId)
			if (!state || (!state.queue && !state.raf)) return
			await new Promise((resolve) => window.setTimeout(resolve, 32))
		}
		// Защитный предел для аномально огромного ответа: ничего не теряем.
		const state = revealStates.current.get(targetThreadId)
		if (state?.queue) {
			appendVisibleText(targetThreadId, state.queue)
			state.queue = ""
		}
	}

	useEffect(() => () => {
		for (const state of revealStates.current.values()) if (state.raf) cancelAnimationFrame(state.raf)
		revealStates.current.clear()
	}, [])

	// ---- streaming events ------------------------------------------------
	const applyEvent = useCallback(
		(ev: ChatEvent) => {
			if (ev.type === "thread-title" && ev.title) {
				const target = ev.threadId
				const title = cleanThreadTitle(ev.title)
				if (target && title) {
					setThreads((prev) => prev.map((t) => t.id === target ? { ...t, title } : t))
					void api.renameThread(target, title).catch(() => {})
				}
				return
			}
			if (ev.type === "error") {
				notify(ev.message || "Ошибка запроса", true)
			}

			const targetThreadId = ev.threadId
			if (!targetThreadId) return
			if (ev.type === "text-delta") {
				appendText(targetThreadId, ev.delta ?? "")
				return
			}
			updateThreadTurns(targetThreadId, (prev) => {
				const next = [...prev]
				const assistantId = assistantIds.current.get(targetThreadId)
				if (!assistantId) return prev
				const idx = next.findIndex((t) => t.id === assistantId)
				if (idx < 0) return prev
				const turn = { ...next[idx], parts: [...next[idx].parts] }

				const lastOf = (kind: Part["kind"]) => {
					for (let i = turn.parts.length - 1; i >= 0; i--) {
						if (turn.parts[i].kind === kind) return i
					}
					return -1
				}

				switch (ev.type) {
					case "text-delta":
					case "reasoning-delta": {
						const kind = ev.type === "text-delta" ? "text" : "thought"
						const i = turn.parts.length - 1
						if (i >= 0 && turn.parts[i].kind === kind) {
							const p = turn.parts[i] as { kind: "text" | "thought"; text: string }
							turn.parts[i] = { kind, text: p.text + (ev.delta ?? "") } as Part
						} else {
							turn.parts.push({ kind, text: ev.delta ?? "" } as Part)
						}
						break
					}
					case "tool-call": {
						turn.parts.push({
							kind: "tool",
							id: ev.id ?? `tool-${turn.parts.length}`,
							name: ev.name ?? "tool",
							server: ev.server ?? "",
							args: ev.args,
							done: false,
						})
						break
					}
					case "tool-args": {
						const i = turn.parts.findIndex((p) => p.kind === "tool" && p.id === ev.id)
						const at = i >= 0 ? i : lastOf("tool")
						if (at >= 0) {
							const p = turn.parts[at] as ToolPart
							turn.parts[at] = { ...p, args: { ...(p.args ?? {}), ...(ev.args ?? {}) } }
						}
						break
					}
					case "tool-result": {
						const i = turn.parts.findIndex((p) => p.kind === "tool" && p.id === ev.id)
						const at = i >= 0 ? i : lastOf("tool")
						if (at >= 0) {
							const p = turn.parts[at] as ToolPart
							turn.parts[at] = { ...p, result: ev.result, done: true }
						}
						break
					}
					case "transcript-reset": {
						const reveal = revealStates.current.get(targetThreadId)
						if (reveal) reveal.queue = ""
						turn.parts = []
						break
					}
					case "done": {
						// send() снимет streaming только после того, как очередь плавного
						// проявления полностью дошла до UI.
						break
					}
				}

				next[idx] = turn
				return next
			})
		},
		[notify, appendText, updateThreadTurns],
	)

	useEffect(() => onChatEvent(applyEvent), [applyEvent])

	// ---- autoscroll ------------------------------------------------------
	useEffect(() => {
		if (!atBottom) return
		const el = scroller.current
		if (el) el.scrollTop = el.scrollHeight
	}, [turns, atBottom])

	function onScroll() {
		const el = scroller.current
		if (!el) return
		setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 64)
	}

	// ---- сверка с Notion ------------------------------------------------
	// Открытие чата, возврат в окно и конец ответа проверяют настоящее
	// состояние чата в Notion: история берётся из транскрипта, а не только из
	// локальной базы, и видно, что модель всё ещё пишет.
	function stopWatchingRemote(id: string) {
		const timer = remoteWatchers.current.get(id)
		if (timer !== undefined) {
			window.clearTimeout(timer)
			remoteWatchers.current.delete(id)
		}
	}

	function watchRemote(id: string) {
		if (remoteWatchers.current.has(id)) return
		const timer = window.setTimeout(() => {
			remoteWatchers.current.delete(id)
			void syncThread(id)
		}, 4000)
		remoteWatchers.current.set(id, timer)
	}

	async function syncThread(id: string, loadEarlier = false) {
		if (!id || syncing.current.has(id) || (editingThreads.current.has(id) && !loadEarlier)) return
		// Своя отправка в этом чате — история пишется локально, сверка подождёт,
		// но не теряем следующий опрос, если таймер попал внутрь отправки.
		if (!loadEarlier && localBusy.current.has(id)) {
			watchRemote(id)
			return
		}
		syncing.current.add(id)
		try {
			const state = loadEarlier ? await api.loadEarlierThread(id) : await api.syncThread(id)
			if (!state) return
			if (threadIdRef.current === id) setHasEarlier(Boolean(state.hasEarlier))
			if (state.streaming) {
				// Свой живой стрим рисует ответ сам — ничего не перезаписываем,
				// но продолжаем опрос до финального committed/idle состояния.
				markRunning(id, true)
				watchRemote(id)
				return
			}
			const loaded = toTurns(state.messages)
			if (loaded.length > 0) {
				if (state.running) {
					const last = loaded[loaded.length - 1]
					if (last.role === "assistant") last.streaming = true
				}
				threadCache.current.set(id, loaded)
				if (threadIdRef.current === id) {
					turnsRef.current = loaded
					setTurns(loaded)
				}
			}
			if (state.title) {
				setThreads((prev) => prev.map((t) => (t.id === id ? { ...t, title: state.title } : t)))
			}
			markRunning(id, state.running)
			if (state.running) watchRemote(id)
			else stopWatchingRemote(id)
		} catch {
			/* офлайн или нет сессии — остаёмся на локальной истории */
		} finally {
			syncing.current.delete(id)
		}
	}

	// Возврат в окно и фоновый такт подтягивают состояние открытого чата.
	useEffect(() => {
		const resync = () => {
			if (document.visibilityState === "hidden") return
			const id = threadIdRef.current
			if (id) void syncThread(id)
		}
		window.addEventListener("focus", resync)
		document.addEventListener("visibilitychange", resync)
		const beat = window.setInterval(resync, 20000)
		return () => {
			window.removeEventListener("focus", resync)
			document.removeEventListener("visibilitychange", resync)
			window.clearInterval(beat)
			for (const timer of remoteWatchers.current.values()) window.clearTimeout(timer)
			remoteWatchers.current.clear()
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [])

	// ---- threads ---------------------------------------------------------
	async function openThread(id: string) {
		const sequence = ++openSequence.current
		if (threadIdRef.current) threadCache.current.set(threadIdRef.current, turnsRef.current)
		threadIdRef.current = id
		setThreadId(id)
		setHasEarlier(false)
		// Пустой кэш больше не считается историей: раньше именно из-за этого
		// переписка выглядела потерянной при возврате в чат.
		const cached = threadCache.current.get(id)
		if (cached && cached.length > 0) {
			turnsRef.current = cached
			setTurns(cached)
			setAtBottom(true)
			void syncThread(id)
			return
		}
		try {
			const msgs = await api.loadThread(id)
			if (sequence !== openSequence.current || threadIdRef.current !== id) return
			const loaded = toTurns(msgs)
			threadCache.current.set(id, loaded)
			turnsRef.current = loaded
			setTurns(loaded)
			setAtBottom(true)
		} catch (e) {
			notify(errText(e), true)
		}
		// Историю и статус добираем из Notion: чат мог идти в веб-версии
		// или продолжаться после обрыва нашего потока.
		void syncThread(id)
	}

	function newChat() {
		if (threadIdRef.current) threadCache.current.set(threadIdRef.current, turnsRef.current)
		threadIdRef.current = ""
		setThreadId("")
		turnsRef.current = []
		setTurns([])
		setInput("")
	}

	// Переименование из контекстного меню чата (три точки).
	async function renameThread(id: string, title: string) {
		const before = threads
		setThreads((prev) => prev.map((t) => (t.id === id ? { ...t, title } : t)))
		try {
			await api.renameThread(id, title)
		} catch (e) {
			setThreads(before)
			notify(errText(e), true)
		}
	}

	// У каждого воркспейса своя история: после переключения список чатов
	// перечитывается, а открытый чат сбрасывается.
	async function switchedWorkspace() {
		newChat()
		try {
			applyConnection(await api.connectionStatus())
		} catch (e) {
			notify(errText(e), true)
			return
		}
		try {
			const list = await api.listThreads()
			setThreads(list ?? [])
			if (list?.length) await openThread(list[0].id)
		} catch (e) {
			notify(`Не удалось загрузить чаты: ${errText(e)}`, true)
		}
	}

	async function removeThread(id: string) {
		try {
			await api.deleteThread(id)
			const list = await api.listThreads()
			setThreads(list ?? [])
			if (id === threadId) {
				if (list?.length) await openThread(list[0].id)
				else newChat()
			}
		} catch (e) {
			notify(errText(e), true)
		}
	}

	function patchSettings(patch: Partial<Settings>) {
		setSettings((prev) => {
			const next = { ...prev, ...patch }
			api.saveSettings(next).catch((e) => notify(errText(e), true))
			return next
		})
	}

	// ---- sending ---------------------------------------------------------
	// Файлы из скрепки уезжают в Notion по-настоящему (S3 + разбор
	// processAgentAttachment) и прицепляются к сообщению отдельными шагами.
	async function uploadFiles(tid: string, files: Attachment[]) {
		const uploaded: UploadedAttachment[] = []
		const fallback: Attachment[] = []
		for (const file of files) {
			if (!file.dataBase64) {
				fallback.push(file)
				continue
			}
			try {
				uploaded.push(
					await api.uploadAttachment({
						threadId: tid,
						fileName: file.name,
						contentType: file.contentType,
						dataBase64: file.dataBase64,
					}),
				)
			} catch (e) {
				// Загрузка может не пройти (лимит, тип файла) — тогда хотя бы
				// текстовое содержимое вшиваем в само сообщение.
				fallback.push({ ...file, error: errText(e) })
			}
		}
		return { uploaded, fallback }
	}

	/** Текстовый фоллбек для файлов, которые не удалось загрузить. */
	function withAttachments(text: string, files: Attachment[]) {
		if (files.length === 0) return text
		const blocks = files.map((f) =>
			f.text === undefined
				? `### ${f.name}\n(${f.error ?? "содержимое не приложено"})`
				: `### ${f.name}\n\`\`\`\n${f.text}\n\`\`\``,
		)
		return `${text}\n\nПриложенные файлы:\n\n${blocks.join("\n\n")}`.trim()
	}

	// override нужен для ответов из опросника: текст приходит не из инпута.
	// Ответ на ask-survey: сначала пробуем штатный user.input_response — тогда
	// агент продолжает текущий ход. Если ход уже закрыт, шлём обычным сообщением.
	async function answerSurvey(answer: string, content?: Record<string, unknown>) {
		const tid = threadIdRef.current
		if (tid && content && Object.keys(content).length > 0) {
			try {
				const delivered = await api.sendSurveyAnswer(tid, "ask-survey", content)
				if (delivered) return
			} catch {
				/* падаем на обычное сообщение */
			}
		}
		await send([], answer)
	}

	async function send(files: Attachment[] = [], override?: string) {
		let text = (override ?? input).trim()
		if (!text && files.length === 0) return
		// Блокируем только повторную отправку в ЭТОТ чат. Другие чаты свободны.
		if (threadId && runningThreadsRef.current.has(threadId)) return

		// Re-check with the backend instead of trusting stale local state.
		let live = connection
		if (!connectedRef.current) {
			try {
				live = await api.connectionStatus()
				applyConnection(live)
			} catch {
				/* handled below */
			}
		}
		if (!live?.connected) {
			notify("Сначала импортируйте cURL в Настройки → Подключение", true)
			setShowSettings(true)
			return
		}

		let tid = threadId
		let assistantTurnId = ""
		let requestSucceeded = false
		try {
			if (!tid) {
				const title = text.slice(0, 60) || files[0]?.name || "Новый чат"
				const t = await api.createThread(title)
				tid = t.id
				threadIdRef.current = tid
				turnsRef.current = []
				setThreadId(tid)
				setThreads((prev) => [t, ...prev])
			}

			// Чат занят своей отправкой начиная с этой секунды.
			localBusy.current.add(tid)

			// Сначала файлы, потом сам запрос: Notion ждёт готовые
			// вложения в том же transcript.
			let attachments: UploadedAttachment[] = []
			if (files.length > 0) {
				markRunning(tid, true)
				const result = await uploadFiles(tid, files)
				attachments = result.uploaded
				text = withAttachments(text, result.fallback)
				if (result.fallback.some((f) => f.error)) {
					notify("Не все файлы удалось загрузить", true)
				}
				if (!text) {
					text =
						files.length === 1
							? `Посмотри файл: ${files[0].name}`
							: `Посмотри файлы: ${files.map((f) => f.name).join(", ")}`
				}
			}

			const now = Date.now()
			const userTurn: Turn = {
				id: `u-${now}`,
				role: "user",
				content: text,
				parts: [
					{ kind: "text", text },
					...attachments.map((attachment) => ({ kind: "attachment" as const, attachment })),
				],
				createdAt: now,
			}
			assistantTurnId = `a-${now}-${Math.random().toString(36).slice(2, 8)}`
			assistantIds.current.set(tid, assistantTurnId)
			const aTurn: Turn = {
				id: assistantTurnId,
				role: "assistant",
				content: "",
				parts: [],
				createdAt: now,
				streaming: true,
			}
			// История теперь включает и ответы ассистента — без этого модель
			// теряла контекст предыдущих шагов в диалоге.
			const history = (threadCache.current.get(tid) ?? turnsRef.current)
				.filter((t) => t.content.trim() !== "")
				.map((t) => ({
					id: t.id, role: t.role, content: t.content,
					attachments: t.parts.flatMap((part) => part.kind === "attachment" ? [part.attachment] : []),
				}))

			updateThreadTurns(tid, (prev) => [...prev, userTurn, aTurn])
			setInput("")
			setAtBottom(true)
			markRunning(tid, true)

			// Сохраняем реплику ДО запуска сети. Так даже быстрый переход в другой
			// чат не успеет открыть пустой кэш раньше записи в SQLite.
			await persistTurn(tid, userTurn)
			streamStartedAt.current.set(tid, Date.now())

			await api.sendMessage({
				threadId: tid,
				messages: [
					...history,
					{ id: userTurn.id, role: "user", content: text, attachments },
				],
				model: settings.model,
				reasoningEffort: settings.reasoningEffort,
			})
			requestSucceeded = true
		} catch (e) {
			notify(errText(e), true)
		} finally {
			if (tid) editingThreads.current.delete(tid)
			await waitForReveal(tid)
			markRunning(tid, false)
			const completed = updateThreadTurns(tid, (prev) => prev.map((t) =>
				t.id === assistantTurnId ? { ...t, streaming: false } : t,
			))
			const done = completed.find((t) => t.id === assistantTurnId)
			if (done) await persistTurn(tid, done)
			// Usage у Notion обновляется с небольшой задержкой после закрытия хода.
			if (requestSucceeded) window.setTimeout(() => void refreshUsage(), 600)
			assistantIds.current.delete(tid)
			streamStartedAt.current.delete(tid)
			revealStates.current.delete(tid)
			api.listThreads()
				.then((l) => setThreads(l ?? []))
				.catch(() => {})
			// Сверка сразу после ответа: если наш поток оборвался, а Notion
			// продолжает писать, ответ всё равно доедет до приложения.
			localBusy.current.delete(tid)
			void syncThread(tid)
		}
	}

	// persistTurn пишет реплику в SQLite вместе с рендер-частями (текст,
	// размышления, карточки инструментов), чтобы чат открывался таким же.
	async function persistTurn(tid: string, turn: Turn) {
		if (!tid) return
		const text =
			turn.content ||
			turn.parts
				.filter((p): p is { kind: "text"; text: string } => p.kind === "text")
				.map((p) => p.text)
				.join("")
		if (!text && turn.parts.length === 0) return
		try {
			await api.saveMessage({
				id: turn.id,
				threadId: tid,
				role: turn.role,
				content: text,
				parts: JSON.stringify(turn.parts ?? []),
				createdAt: turn.createdAt || Date.now(),
			})
		} catch (error) {
			notify(`Не удалось сохранить сообщение: ${errText(error)}`, true)
			// Пользовательскую реплику не отправляем, если она не переживёт
			// перезапуск. Ошибка сохранения ответа уже не отменяет готовый ответ.
			if (turn.role === "user") throw error
		}
	}

	async function editTurn(turn: Turn) {
		setInput(turn.content)
		try {
			if (threadId) await api.trimThreadFrom(threadId, turn.id)
			const current = turnsRef.current
			const index = current.findIndex((item) => item.id === turn.id)
			const trimmed = index < 0 ? current : current.slice(0, index)
			turnsRef.current = trimmed
			setTurns(trimmed)
			if (threadId) {
				threadCache.current.set(threadId, trimmed)
				editingThreads.current.add(threadId)
			}
		} catch (e) {
			notify(errText(e), true)
		}
	}

	// Файлы из шагов (index.html, archive.zip…) открываются в панели-браузере:
	// текстовые и HTML — с превью, бинарные — ссылкой на скачивание.
	async function openFile(file: ComputerFile) {
		if (file.fileName.toLowerCase().endsWith(".zip")) {
			// Зипы никогда не открываем — только скачиваем.
			try {
				const got = await api.fetchAttachment(threadIdRef.current, file.fileUrl, file.fileName)
				if (got.dataBase64) {
					saveBase64(got.dataBase64, file.fileName, got.contentType || "application/zip")
					notify(`Архив ${file.fileName} скачан`)
					return
				}
				if (got.signedUrl) { await api.openURL(got.signedUrl); return }
			} catch (e) {
				notify(errText(e), true)
				return
			}
		}
		try {
			const got = await api.fetchAttachment(threadIdRef.current, file.fileUrl, file.fileName)
			// Картинки — сразу в лайтбокс, без панели-браузера.
			if (got.dataBase64 && got.contentType.startsWith("image/")) {
				setImageView({ name: file.fileName, src: `data:${got.contentType};base64,${got.dataBase64}` })
				return
			}
			const dot = file.fileName.lastIndexOf(".")
			const ext = dot > 0 ? file.fileName.slice(dot).toLowerCase() : ".txt"
			const name = dot > 0 ? file.fileName.slice(0, dot) : file.fileName
			if (got.text) {
				setArtifact({
					id: file.id,
					name,
					ext,
					lang: ext.replace(".", ""),
					code: got.text,
					kind: artifactKind(ext),
				})
				setActiveFile(file.fileUrl)
				return
			}
			if (got.dataBase64) {
				// Архивы и другие бинарники показывать нечего — сразу скачиваем.
				if (!got.contentType.startsWith("image/") && !PREVIEW_EXT.has(ext)) {
					saveBase64(got.dataBase64, file.fileName, got.contentType)
					notify(`Файл ${file.fileName} скачан`)
					return
				}
				setArtifact({ id: file.id, name, ext, lang: ext.replace(".", ""), code: "", kind: got.contentType.startsWith("image/") ? "web" : "data", mime: got.contentType, dataBase64: got.dataBase64 })
				setActiveFile(file.fileUrl)
				return
			}
			// Нет тела файла — отдаём подписанную ссылку браузеру/скачиванию.
			if (got.signedUrl) { await api.openURL(got.signedUrl); return }
			notify("Файл нельзя показать в превью", true)
		} catch (e) {
			notify(errText(e), true)
		}
	}

	/** Подтягивает байты файла для показа картинки прямо в ответе. */
	const loadFile = useCallback(
		(file: ComputerFile) => api.fetchAttachment(threadIdRef.current, file.fileUrl, file.fileName),
		[],
	)

	// Опросник ask-survey из последнего завершённого ответа живёт в инпуте.
	const lastAssistant = [...turns].reverse().find((t) => t.role === "assistant")
	const pendingSurveys =
		lastAssistant && !lastAssistant.streaming
			? surveysFromTools(lastAssistant.parts).filter(
					(s) => !answeredSurveys.has(`${lastAssistant.id}:${s.id}`),
				)
			: []

	function closeSurveys() {
		if (!lastAssistant) return
		setAnsweredSurveys((prev) => {
			const next = new Set(prev)
			for (const s of pendingSurveys) next.add(`${lastAssistant.id}:${s.id}`)
			return next
		})
	}

	const isEmpty = turns.length === 0
	// Стартовый экран показываем, пока нет подключения и его не пропустили.
	const showOnboarding = !onboardingDone && !connection?.connected

	return (
		<div className="bg-background flex h-full flex-col">
			<TitleBar
				title={threads.find((t) => t.id === threadId)?.title || "Чат"}
				collapsed={!sidebarOpen}
				sidebarWidth={sidebarWidth}
				animate={!resizing}
				onToggleSidebar={() => setSidebarOpen((v) => !v)}
				onNewChat={newChat}
			/>

			<div className="relative flex min-h-0 flex-1">
				<Sidebar
					collapsed={!sidebarOpen}
					width={sidebarWidth}
					onWidth={setSidebarWidth}
					onResizing={setResizing}
					threads={threads}
					activeId={threadId}
					onSelect={(id) => void openThread(id)}
					onNew={newChat}
					onDelete={(id) => void removeThread(id)}
					onRename={(id, title) => void renameThread(id, title)}
					onOpenSettings={(target) => { setSettingsSection(target); setShowSettings(true) }}
					onToast={notify}
					onWorkspaceSwitched={() => void switchedWorkspace()}
					runningIds={runningThreads}
				/>

				{showOnboarding ? (
					<main className="flex min-h-0 min-w-0 flex-1 flex-col">
						<Onboarding
							onDone={() => setOnboardingDone(true)}
							onConnectionChange={applyConnection}
							onToast={notify}
							onOpenArtifact={setArtifact}
						/>
					</main>
				) : (
				<main className="flex min-h-0 min-w-0 flex-1 flex-col">
					<ScrollArea
						className="min-h-0 flex-1"
						viewportRef={scroller}
						onViewportScroll={onScroll}
					>
						{isEmpty ? (
							<EmptyState
								onPersonalize={() => setShowSettings(true)}
								onPick={(prompt) => setInput(prompt)}
							/>
						) : (
							<div className="mx-auto flex w-full max-w-[798px] flex-col gap-6 px-13 pt-4 pb-8">
								{hasEarlier ? <button className="text-muted-foreground hover:text-foreground self-center text-xs" onClick={() => void syncThread(threadId, true)}>Загрузить более ранние сообщения</button> : null}
								{turns.map((t) => (
									<div key={t.id} className="min-w-0">
										{t.role === "user" ? (
											<UserMessage
												turn={t}
												onEdit={(x) => void editTurn(x)}
												onOpenFile={(file) => void openFile(file)}
												onLoadFile={loadFile}
											/>
										) : (
											<AssistantMessage
												turn={t}
												activeArtifactId={artifact?.id}
												activeFile={activeFile}
												onOpenArtifact={setArtifact}
												onOpenFile={(file) => void openFile(file)}
												onLoadFile={loadFile}
												onSurveyAnswer={(answer, content) => void answerSurvey(answer, content)}
											/>
										)}
									</div>
								))}
							</div>
						)}
					</ScrollArea>

					<Composer
						value={input}
						onChange={setInput}
						onSend={(files) => void send(files)}
						onStop={() => void api.stopInference(threadId).catch(() => {})}
						busy={runningThreads.has(threadId)}
						settings={settings}
						models={models}
						usage={usage}
						onOpenUsage={() => { setSettingsSection("usage"); setShowSettings(true) }}
						onPatchSettings={patchSettings}
						surveys={pendingSurveys}
						onSurveyAnswer={(result) => {
							closeSurveys()
							void answerSurvey(result.text, result.content)
						}}
						onSurveySkip={closeSurveys}
						showScrollDown={!atBottom && !isEmpty}
						onScrollDown={() => {
							setAtBottom(true)
							const el = scroller.current
							if (el) el.scrollTop = el.scrollHeight
						}}
					/>
				</main>
				)}

				{artifact ? (
					<ArtifactPanel
						artifact={artifact}
						onClose={() => {
							setArtifact(null)
							setActiveFile("")
						}}
						onToast={notify}
					/>
				) : null}

				{/* Настройки лежат ВНУТРИ контентной области, поэтому тайтлбар
				    с кнопками окна остаётся видимым и окно не «прыгает». */}
				<SettingsModal
					open={showSettings}
					initialSection={settingsSection as never}
					initialUsage={usage}
					onUsageChange={setUsage}
					onOpenChange={(next) => { setShowSettings(next); if (!next) setSettingsSection(undefined) }}
					navWidth={sidebarWidth}
					settings={settings}
					models={models}
					connection={connection}
					onSaved={(s) => {
						const next = { ...DEFAULTS, ...s, mcpPaths: s.mcpPaths ?? {} }
						const modeChanged = next.chatMode !== settings.chatMode
						setSettings(next)
						if (modeChanged && connection?.connected) {
							setModels([])
							api.listModels().then((items) => setModels(items ?? [])).catch(() => setModels([]))
						}
					}}
					onModelsChanged={setModels}
					onConnectionChange={applyConnection}
					onToast={notify}
				/>
			</div>

			{imageView ? (
				<div
					className="lightbox-backdrop fixed inset-0 z-[70] flex items-center justify-center bg-black/80 p-8"
					onClick={() => setImageView(null)}
				>
					<img
						src={imageView.src}
						alt={imageView.name}
						className="lightbox-image max-h-full max-w-full cursor-zoom-out rounded-xl object-contain shadow-sheet"
					/>
				</div>
			) : null}

			{toast ? (
				<div
					className={cn(
						"fixed bottom-24 left-1/2 z-[60] -translate-x-1/2 rounded-lg border px-3 py-2 text-sm shadow-sheet",
						"animate-in fade-in-0 slide-in-from-bottom-2 duration-200",
						toast.error ? "bg-destructive text-destructive-foreground" : "bg-card",
					)}
				>
					{toast.msg}
				</div>
			) : null}
		</div>
	)
}
