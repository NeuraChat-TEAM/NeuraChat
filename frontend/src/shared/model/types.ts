// Shared shapes between Go bindings and the UI.

export type Thread = {
	id: string
	title: string
	/** Воркспейс, к которому привязан чат (истории не смешиваются). */
	spaceId?: string
	createdAt: number
	updatedAt: number
}

export type StoredMessage = {
	id: string
	threadId: string
	role: "user" | "assistant"
	content: string
	parts: string
	createdAt: number
}

export type ToolPart = {
	kind: "tool"
	id: string
	name: string
	server: string
	args?: Record<string, unknown>
	result?: unknown
	done: boolean
}

export type Part =
	| { kind: "text"; text: string }
	| { kind: "thought"; text: string }
	| {
			kind: "attachment"
			attachment: {
				stepId: string
				stepType: string
				fileUrl: string
				fileName: string
				contentType: string
				agentFileId?: string
				metadata?: Record<string, unknown>
			}
		}
	| ToolPart

export type Turn = {
	id: string
	role: "user" | "assistant"
	content: string
	parts: Part[]
	createdAt: number
	streaming?: boolean
}

export type ChatEvent = {
	type:
		| "text-delta"
		| "reasoning-delta"
		| "tool-call"
		| "tool-args"
		| "tool-result"
		| "thread-title"
		| "transcript-reset"
		| "error"
		| "done"
	delta?: string
	id?: string
	name?: string
	server?: string
	title?: string
	args?: Record<string, unknown>
	result?: unknown
	threadId?: string
	message?: string
}

export type Settings = {
	model: string
	reasoningEffort: string
	systemPrompt: string
	autoPrependMcp: boolean
	sendWithEnter: boolean
	/** «All sources I can access» — по умолчанию выключено. */
	searchAllSources: boolean
	/** legacy, Beta V2 workflow, or Agent Service V3. */
	chatMode: "legacy" | "v2" | "v3"
	/** Общие агентные опции; transport-specific mapping happens in Go. */
	browserEnabled: boolean
	skipApprovals: boolean
	useMemories: boolean
	excludeChatFromMemories: boolean
	suggestedEdits: boolean
	theme: "notion-dark" | "notion-light"
	motionOff: boolean
	fontScale: number
	notcodeDir: string
	notcodeCmd: string
	notcodePort: number
	ngrokPath: string
	ngrokDomain: string
	mcpServerName: string
	autoStartNotcode: boolean
	rotateNotcodeToken: boolean
	activeUserId: string
	activeSpaceId: string
	activeSpaceViewId: string
	activeSpaceName: string
	mcpIntegrationId: string
	mcpServerUrl: string
	/** Manual overrides for Notion's private MCP endpoints, keyed by step. */
	mcpPaths: Record<string, string>
}

export type Space = {
	id: string
	name: string
	icon: string
	planType: string
	spaceViewId: string
	isGuest: boolean
	active: boolean
}

export type Account = {
	userId: string
	name: string
	email: string
	avatar: string
	active: boolean
	spaces: Space[]
}

export type WorkspaceState = {
	accounts: Account[]
	activeUserId: string
	activeSpaceId: string
	activeName: string
	activePlan: string
	canCreate: boolean
}

export type McpEndpointStep = "validate" | "connect" | "verifyModal" | "verifyConnections" | "verifyConnection" | "settings" | "disconnect"

export type McpTool = { name: string; title?: string; description?: string }

export type McpModule = {
	integrationId: string
	name: string
	serverUrl: string
	enabled: boolean
	toolCount: number
	tools?: McpTool[]
	connectionId?: string
	connectionState?: string
	runReadToolsAutomatically?: boolean
	runWriteToolsAutomatically?: boolean
	settingsWarning?: string
}

/** Одна строка лога NotCode/ngrok/приложения для вкладки «Логи». */
export type NotcodeLogLine = {
	time: string
	source: "app" | "notcode" | "ngrok" | string
	level: "info" | "warn" | "error" | string
	text: string
}

export type NotcodeStatus = {
	notcodeRunning: boolean
	ngrokRunning: boolean
	token: string
	localUrl: string
	publicUrl: string
	/** Готовый адрес, который уходит в Notion: <ngrok>/mcp. */
	mcpUrl?: string
	mcpPath?: string
	workspaceRoot: string
	mode: string
	port?: number
	notcodePid?: number
	ngrokPid?: number
	managed?: boolean
	version?: string
	tools?: number
	sessions?: number
	uptimeSec?: number
	configFile?: string
	ngrokDashboard?: string
	startedAt?: number
	/** Публичный URL реально доводит до NotCode (проверено /health). */
	tunnelOk?: boolean
	tunnelError?: string
	log: string
	logLines?: NotcodeLogLine[]
	error?: string
}

/** Одно окно лимита AI: rolling (6h) или биллинговый период. */
export type UsageWindow = {
	creditType: string
	scope: string
	window?: string
	cadence?: string
	used: number
	limit: number
	periodEndMs?: number
}

export type AIUsage = {
	spaceId: string
	status: string
	rolling?: UsageWindow
	monthly?: UsageWindow
	resetsInSeconds: number
	creditTier?: string
	premiumUsed: number
	premiumLimit: number
	creditBalance: number
	creditsInOverage: number
	servicePeriodStartMs?: number
	servicePeriodEndMs?: number
	basicSpaceUsed: number
	basicSpaceLimit: number
	basicUserUsed: number
	basicUserLimit: number
	/** Счётчики за всё время — показываем отдельно, лимитом не считаются. */
	lifetimeSpaceUsed?: number
	lifetimeUserUsed?: number
	/** Воркспейс в лимитах — красная индикация до resetAtMs. */
	limitReached: boolean
	resetAtMs?: number
	error?: string
}

export type Model = {
	id: string
	label: string
	provider: string
	group: string
	reasoningEfforts: string[]
	defaultReasoningEffort: string
}

export type WorkspaceModel = {
	id: string
	label: string
	provider: string
	group: string
	active: boolean
	disabledByProvider: boolean
}

export type WorkspaceModelPolicy = {
	spaceId: string
	mode: "legacy" | "v2" | "v3" | string
	policyKey: "custom_agent_model_policy" | "personal_agent_model_policy" | string
	models: WorkspaceModel[]
	disabledProviders: string[]
}

export type SessionProfile = {
	id: string
	userId: string
	captureMode: "legacy" | "v2" | "v3" | string
	updatedAt: number
	active: boolean
}

export type ConnectionState = {
	connected: boolean
	spaceId: string
	userId: string
	origin: string
	/** Протокол, из cURL которого восстановлена сохранённая browser-сессия. */
	captureMode: "legacy" | "v2" | "v3" | ""
}

export type DebugEntry = {
	id: string
	startedAt: string
	finishedAt?: string
	method: string
	path: string
	requestCurl: string
	status?: number
	durationMs?: number
	responsePreview: string
	responseBytes: number
	responseTruncated: boolean
	error?: string
}
