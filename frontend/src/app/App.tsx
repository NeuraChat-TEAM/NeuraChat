import { useCallback, useEffect, useRef, useState } from "react"
import { api, errText, onChatEvent, type UploadedAttachment } from "../shared/api/api"
import type {
	ChatEvent,
	ConnectionState,
	Model,
	Part,
	Settings,
	Thread,
	ToolPart,
	Turn,
} from "../shared/model/types"
import { cn } from "../shared/lib/utils"
import type { Artifact } from "../features/artifacts/model/artifacts"
import { artifactKind } from "../features/artifacts/model/artifacts"
import type { ComputerFile } from "../shared/lib/toolOutput"
import ArtifactPanel from "../features/artifacts/components/ArtifactPanel"
import Composer, { type Attachment } from "../features/chat/components/Composer"
import EmptyState from "../features/chat/components/EmptyState"
import Onboarding from "../features/onboarding/components/Onboarding"
import { AssistantMessage, UserMessage } from "../features/chat/components/Message"
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
	mcpIntegrationId: "",
	mcpServerUrl: "",
	mcpPaths: {},
	activeUserId: "",
	activeSpaceId: "",
	activeSpaceViewId: "",
	activeSpaceName: "",
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

type RevealState = {
	queue: string
	raf: number
	lastFrame: number
	charsPerMs: number
	carry: number
}

export default function App() {
	const [settings, setSettings] = useState<Settings>(DEFAULTS)
	const [connection, setConnection] = useState<ConnectionState | null>(null)
	const [models, setModels] = useState<Model[]>([])
	const [threads, setThreads] = useState<Thread[]>([])
	const [threadId, setThreadId] = useState("")
	const [turns, setTurns] = useState<Turn[]>([])
	const [input, setInput] = useState("")
	// Запросы отслеживаются по чатам: можно открыть новый чат и писать в нём,
	// пока предыдущий продолжает работать в фоне.
	const [runningThreads, setRunningThreads] = useState<Set<string>>(() => new Set())
	const runningThreadsRef = useRef(new Set<string>())
	const [showSettings, setShowSettings] = useState(false)
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

	const applyConnection = useCallback((state: ConnectionState | null) => {
		setConnection(state)
		connectedRef.current = !!state?.connected
		if (state?.connected) {
			api.listModels()
				.then((m) => setModels(m ?? []))
				.catch(() => {})
		} else {
			setModels([])
		}
	}, [])

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
			try {
				const [s, conn, list] = await Promise.all([
					api.getSettings(),
					api.connectionStatus(),
					api.listThreads(),
				])
				setSettings({ ...DEFAULTS, ...s, mcpPaths: s.mcpPaths ?? {} })
				applyConnection(conn)
				setThreads(list ?? [])
				if (list?.length) await openThread(list[0].id)
			} catch (e) {
				notify(errText(e), true)
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
			const targetMs = Math.min(1500, Math.max(900, waited || 1200))
			state = {
				queue: "",
				raf: 0,
				lastFrame: performance.now(),
				charsPerMs: Math.min(1.2, Math.max(0.08, chunk.length / targetMs)),
				carry: 0,
			}
			revealStates.current.set(targetThreadId, state)
		}
		state.queue += chunk
		if (state.raf) return

		const tick = (now: number) => {
			const current = revealStates.current.get(targetThreadId)
			if (!current) return
			const elapsed = Math.min(80, now - current.lastFrame)
			// 30 FPS достаточно для мягкого проявления и не заставляет Markdown
			// полностью переразбираться на каждом кадре монитора.
			if (elapsed < 28) {
				current.raf = requestAnimationFrame(tick)
				return
			}
			current.lastFrame = now
			const backlogBoost = Math.min(3, 1 + current.queue.length / 1800)
			const budget = current.carry + elapsed * current.charsPerMs * backlogBoost
			let take = Math.floor(budget)
			current.carry = budget - take
			if (take > 0 && current.queue) {
				take = Math.min(current.queue.length, Math.max(1, take))
				// Проявляем целыми словами, а не побуквенно: дотягиваемся до
				// ближайшей границы слова/строки, иначе выглядит как typewriter.
				if (take < current.queue.length) {
					const limit = Math.min(current.queue.length, take + 24)
					let boundary = take
					while (boundary < limit && !/[\s.,;:!?)\]}—–"'`]/.test(current.queue[boundary])) boundary++
					while (boundary < current.queue.length && /\s/.test(current.queue[boundary])) boundary++
					take = boundary
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

	// ---- threads ---------------------------------------------------------
	async function openThread(id: string) {
		const sequence = ++openSequence.current
		if (threadIdRef.current) threadCache.current.set(threadIdRef.current, turnsRef.current)
		threadIdRef.current = id
		setThreadId(id)
		const cached = threadCache.current.get(id)
		if (cached) { turnsRef.current = cached; setTurns(cached); setAtBottom(true); return }
		try {
			const msgs = await api.loadThread(id)
			if (sequence !== openSequence.current || threadIdRef.current !== id) return
			const loaded = (msgs ?? []).map((m) => ({
					id: m.id,
					role: m.role,
					content: m.content,
					parts: parseParts(m.parts, m.content),
					createdAt: m.createdAt,
					streaming: false,
				}))
			threadCache.current.set(id, loaded)
			turnsRef.current = loaded
			setTurns(loaded)
			setAtBottom(true)
		} catch (e) {
			notify(errText(e), true)
		}
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
			const [conn, list] = await Promise.all([api.connectionStatus(), api.listThreads()])
			applyConnection(conn)
			setThreads(list ?? [])
			if (list?.length) await openThread(list[0].id)
		} catch (e) {
			notify(errText(e), true)
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
				parts: [{ kind: "text", text }],
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
			const history = (threadCache.current.get(tid) ?? turns)
				.filter((t) => t.content.trim() !== "")
				.map((t) => ({ id: t.id, role: t.role, content: t.content }))

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
		} catch (e) {
			notify(errText(e), true)
		} finally {
			await waitForReveal(tid)
			markRunning(tid, false)
			const completed = updateThreadTurns(tid, (prev) => prev.map((t) =>
				t.id === assistantTurnId ? { ...t, streaming: false } : t,
			))
			const done = completed.find((t) => t.id === assistantTurnId)
			if (done) void persistTurn(tid, done)
			assistantIds.current.delete(tid)
			streamStartedAt.current.delete(tid)
			revealStates.current.delete(tid)
			api.listThreads()
				.then((l) => setThreads(l ?? []))
				.catch(() => {})
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
		} catch {
			/* локальная история — не повод ломать ответ */
		}
	}

	async function editTurn(turn: Turn) {
		setInput(turn.content)
		try {
			if (threadId) await api.trimThreadFrom(threadId, turn.id)
			setTurns((prev) => {
				const i = prev.findIndex((t) => t.id === turn.id)
				return i < 0 ? prev : prev.slice(0, i)
			})
		} catch (e) {
			notify(errText(e), true)
		}
	}

	// Файлы из шагов (index.html, archive.zip…) открываются в панели-браузере:
	// текстовые и HTML — с превью, бинарные — ссылкой на скачивание.
	async function openFile(file: ComputerFile) {
		try {
			const got = await api.fetchAttachment(file.fileUrl, file.fileName)
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
				setArtifact({ id: file.id, name, ext, lang: ext.replace(".", ""), code: "", kind: got.contentType.startsWith("image/") ? "web" : "data", mime: got.contentType, dataBase64: got.dataBase64 })
				setActiveFile(file.fileUrl)
				return
			}
			if (got.signedUrl) { await api.openURL(got.signedUrl); return }
			notify("Файл нельзя показать в превью", true)
		} catch (e) {
			notify(errText(e), true)
		}
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
					onOpenSettings={() => setShowSettings(true)}
					onToast={notify}
					onWorkspaceSwitched={() => void switchedWorkspace()}
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
								{turns.map((t) => (
									<div key={t.id} className="min-w-0">
										{t.role === "user" ? (
											<UserMessage turn={t} onEdit={(x) => void editTurn(x)} />
										) : (
											<AssistantMessage
												turn={t}
												activeArtifactId={artifact?.id}
												activeFile={activeFile}
												onOpenArtifact={setArtifact}
												onOpenFile={(file) => void openFile(file)}
												onSurveyAnswer={(answer) => void send([], answer)}
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
						onPatchSettings={patchSettings}
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
					onOpenChange={setShowSettings}
					navWidth={sidebarWidth}
					settings={settings}
					models={models}
					connection={connection}
					onSaved={(s) => setSettings({ ...DEFAULTS, ...s, mcpPaths: s.mcpPaths ?? {} })}
					onConnectionChange={applyConnection}
					onToast={notify}
				/>
			</div>

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
