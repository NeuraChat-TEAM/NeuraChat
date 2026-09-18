// Thin, typed bridge over the Wails runtime.
//
// We call the bound Go methods through `window.go.main.App` instead of the
// generated `wailsjs` modules so the frontend type-checks before `wails dev`
// has ever generated bindings.

import type {
	AIUsage,
	ChatEvent,
	ConnectionState,
	DebugEntry,
	McpModule,
	Model,
	NotcodeLogLine,
	NotcodeStatus,
	Settings,
	StoredMessage,
	Thread,
	WorkspaceState,
} from "../model/types"

/** Вложение, уже загруженное в Notion и готовое уехать в transcript. */
export type UploadedAttachment = {
	stepId: string
	stepType: string
	fileUrl: string
	signedGetUrl?: string
	fileName: string
	contentType: string
	metadata?: Record<string, unknown>
	sizeBytes?: number
	previewUrl?: string
}

/** Скачанный файл артефакта: текст или base64 для бинарных. */
export type NgrokAuthState = { configured: boolean; bundled: boolean; path: string }

export type FetchedFile = {
	fileName: string
	contentType: string
	sizeBytes: number
	text?: string
	dataBase64?: string
	signedUrl?: string
}

/** Участник воркспейса. */
export type SpaceMember = {
	userId: string
	name: string
	email: string
	avatar: string
	role: string
	isSelf: boolean
	isOwner: boolean
}

/** Аккаунт сессии, которого ещё нет в воркспейсе. */
export type InviteCandidate = {
	userId: string
	name: string
	email: string
	avatar: string
}

export type SpaceMembers = {
	spaceId: string
	spaceName: string
	members: SpaceMember[] | null
	candidates: InviteCandidate[] | null
}

/** Карточка MCP-сервера из официального реестра. */
export type MarketplaceServer = {
	name: string
	title?: string
	description?: string
	version?: string
	website?: string
	serverUrl?: string
	transport?: string
	install?: string
}

export type MarketplacePage = {
	servers: MarketplaceServer[]
	nextCursor?: string
	total: number
	source: string
}

type GoApp = Record<string, (...args: unknown[]) => Promise<unknown>>

declare global {
	interface Window {
		go?: { application?: { App?: GoApp }; main?: { App?: GoApp } }
		runtime?: {
			EventsOn: (name: string, cb: (...data: unknown[]) => void) => () => void
			EventsOff: (name: string) => void
		}
	}
}

function app(): GoApp {
	const a = window.go?.application?.App ?? window.go?.main?.App
	if (!a) throw new Error("Не удалось запустить встроенный API приложения")
	return a
}

async function call<T>(name: string, ...args: unknown[]): Promise<T> {
	const fn = app()[name]
	if (typeof fn !== "function") throw new Error(`Метод ${name} не найден в биндингах`)
	return (await fn(...args)) as T
}

export const api = {
	// session / settings
	connectionStatus: () => call<ConnectionState>("ConnectionStatus"),
	importCurl: (curl: string) => call<ConnectionState>("ImportCurl", curl),
	clearSession: () => call<void>("ClearSession"),
	getSettings: () => call<Settings>("GetSettings"),
	saveSettings: (s: Settings) => call<Settings>("SaveSettings", s),
	defaultSystemPrompt: () => call<string>("DefaultSystemPrompt"),

	// window
	windowMinimise: () => call<void>("WindowMinimise"),
	windowToggleMaximise: () => call<void>("WindowToggleMaximise"),
	windowClose: () => call<void>("WindowClose"),
	openURL: (url: string) => call<void>("OpenURL", url),

	// chat
	// Go expects the full user-message history so it can map local turns onto
	// Notion transcript steps (needed for edit/regenerate).
	sendMessage: (payload: {
		threadId: string
		messages: Array<{
			id: string
			role: "user" | "assistant"
			content: string
			attachments?: UploadedAttachment[]
		}>
		model?: string
		reasoningEffort?: string
	}) => call<void>("SendMessage", payload),
	// Файл уезжает в Notion до отправки сообщения: S3 + processAgentAttachment.
	uploadAttachment: (payload: {
		threadId: string
		fileName: string
		contentType: string
		dataBase64: string
	}) => call<UploadedAttachment>("UploadAttachment", payload),
	// Содержимое файла из шага computer-file — для предпросмотра и скачивания.
	fetchAttachment: (fileUrl: string, fileName: string) =>
		call<FetchedFile>("FetchAttachment", fileUrl, fileName),
	stopInference: (threadId: string) => call<void>("StopInference", threadId),
	listModels: () => call<Model[]>("ListModels"),

	// threads
	listThreads: () => call<Thread[]>("ListThreads"),
	// Поиск по названиям и тексту сообщений в текущем воркспейсе.
	searchThreads: (query: string) => call<Thread[]>("SearchThreads", query),
	createThread: (title: string) => call<Thread>("CreateThread", title),
	renameThread: (id: string, title: string) => call<void>("RenameThread", id, title),
	deleteThread: (id: string) => call<void>("DeleteThread", id),
	loadThread: (id: string) => call<StoredMessage[]>("LoadThread", id),
	saveMessage: (m: StoredMessage) => call<void>("SaveMessage", m),
	trimThreadFrom: (threadId: string, messageId: string) =>
		call<void>("TrimThreadFrom", threadId, messageId),

	// аккаунты и воркспейсы
	listWorkspaces: () => call<WorkspaceState>("ListWorkspaces"),
	switchWorkspace: (userId: string, spaceId: string, spaceViewId: string, spaceName: string) =>
		call<WorkspaceState>("SwitchWorkspace", userId, spaceId, spaceViewId, spaceName),
	createWorkspace: (name: string) => call<WorkspaceState>("CreateWorkspace", name),
	// Участники воркспейса: состав + аккаунты сессии без этого воркспейса.
	listMembers: (spaceId: string) => call<SpaceMembers>("ListMembers", spaceId),
	inviteMembers: (spaceId: string, userIds: string[], emails: string[], role: string) =>
		call<SpaceMembers>(
			"InviteMembers",
			spaceId,
			userIds,
			emails,
			role,
		),
	removeMember: (spaceId: string, userId: string) =>
		call<SpaceMembers>("RemoveMember", spaceId, userId),
	updateWorkspace: (spaceId: string, name: string, icon: string) =>
		call<WorkspaceState>("UpdateWorkspace", spaceId, name, icon),
	updateAccount: (name: string, avatar: string) =>
		call<WorkspaceState>("UpdateAccount", name, avatar),

	// notcode + mcp
	notcodeStatus: () => call<NotcodeStatus>("NotcodeStatus"),
	ngrokTokenStatus: () => call<NgrokAuthState>("NgrokTokenStatus"),
	saveNgrokToken: (token: string) => call<NgrokAuthState>("SaveNgrokToken", token),
	clearNgrokToken: () => call<NgrokAuthState>("ClearNgrokToken"),
	// Логи NotCode и ngrok раздельно — вкладка «Логи» в настройках.
	notcodeLogs: () => call<NotcodeLogLine[]>("NotcodeLogs"),
	notcodeClearLogs: () => call<void>("NotcodeClearLogs"),
	// Лимиты AI: rolling-окно и биллинговый период активного воркспейса.
	aiUsage: () => call<AIUsage>("AIUsage"),
	notcodeStart: () => call<NotcodeStatus>("NotcodeStart"),
	notcodeStop: () => call<NotcodeStatus>("NotcodeStop"),
	connectNotcodeMcp: () =>
		call<{ module: McpModule; status: NotcodeStatus; systemPrompt: string }>(
			"ConnectNotcodeMcp",
		),
	mcpList: () => call<McpModule[]>("McpList"),
	mcpConnect: (input: {
		name: string
		serverUrl: string
		token?: string
		// Smithery и подобные хостинги читают ключ из query (?api_key=…&profile=…),
		// часть серверов — из произвольных заголовков (X-API-Key и т.п.).
		headers?: Record<string, string>
		query?: Record<string, string>
		// Сразу разрешить инструменты без ручного подтверждения.
		autoRun?: boolean
		runWriteToolsAutomatically?: boolean
	}) => call<McpModule>("McpConnect", input),
	mcpDisconnect: (integrationId: string) => call<void>("McpDisconnect", integrationId),
	mcpSetEnabled: (integrationId: string, enabled: boolean) =>
		call<void>("McpSetEnabled", integrationId, enabled),
	mcpEndpoints: () => call<Record<string, string>>("McpEndpoints"),
	// Маркетплейс: живой каталог реестра MCP (тысячи серверов).
	marketplaceList: (query: string, cursor: string, remoteOnly: boolean) =>
		call<MarketplacePage>("MarketplaceList", query, cursor, remoteOnly),

	// debug
	debugLogs: () => call<DebugEntry[]>("DebugLogs"),
	clearDebugLogs: () => call<void>("ClearDebugLogs"),
}

export function onChatEvent(handler: (e: ChatEvent) => void): () => void {
	const rt = window.runtime
	if (!rt) return () => {}
	return rt.EventsOn("chat:event", (...data: unknown[]) => {
		const payload = data[0] as ChatEvent | undefined
		if (payload && typeof payload === "object") handler(payload)
	})
}

export function errText(e: unknown): string {
	if (e instanceof Error) return e.message
	if (typeof e === "string") return e
	return JSON.stringify(e)
}
