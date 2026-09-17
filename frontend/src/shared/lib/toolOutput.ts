export type ComputerFile = {
	id: string
	fileName: string
	fileUrl: string
	contentType: string
	sizeBytes?: number
	source?: string
}

export type ParsedToolResult = {
	labels: string[]
	files: ComputerFile[]
	text: string
	summaries: string[]
	statusCode?: number
	rest?: unknown
	raw: unknown
}

const SECRET_KEYS = /^(headers?|cookie|authorization|requestcurl|curl|token|auth(token)?|api[-_]?key|set-cookie|request|response|envelope|trace(id)?|session(id)?)$/i
const TRANSPORT_KEYS = new Set(["finished", "processId", "startedAt", "completedAt", "toolType", "toolName", "type", "id", "metadata"])

function asRecord(value: unknown): Record<string, unknown> | null {
	return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : null
}

function tryJson(text: string): unknown {
	const trimmed = text.trim()
	if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return null
	try { return JSON.parse(trimmed) } catch { return null }
}

function flattenLabels(value: unknown, out: string[] = []): string[] {
	if (typeof value === "string") { const t = value.trim(); if (t) out.push(t); return out }
	if (Array.isArray(value)) { value.forEach((item) => flattenLabels(item, out)); return out }
	const record = asRecord(value)
	if (record) for (const key of ["text", "label", "title", "plainText"]) if (typeof record[key] === "string") return flattenLabels(record[key], out)
	return out
}

function sanitize(value: unknown, depth = 0): unknown {
	if (depth > 5) return "…"
	if (Array.isArray(value)) return value.slice(0, 100).map((item) => sanitize(item, depth + 1))
	const record = asRecord(value)
	if (!record) return value
	const clean: Record<string, unknown> = {}
	for (const [key, item] of Object.entries(record)) {
		if (SECRET_KEYS.test(key) || TRANSPORT_KEYS.has(key) || item == null) continue
		clean[key] = sanitize(item, depth + 1)
	}
	return clean
}

function fileFrom(value: unknown, index: number): ComputerFile | null {
	const record = asRecord(value)
	if (!record) return null
	const fileUrl = typeof record.fileUrl === "string" ? record.fileUrl : typeof record.fileUri === "string" ? record.fileUri : ""
	const fileName = typeof record.fileName === "string" ? record.fileName : typeof record.name === "string" ? record.name : ""
	if (!fileName && !fileUrl) return null
	const metadata = asRecord(record.metadata) ?? {}
	return {
		id: typeof record.id === "string" ? record.id : `file-${index + 1}`,
		fileName: fileName || "file",
		fileUrl,
		contentType: typeof record.contentType === "string" ? record.contentType : "application/octet-stream",
		sizeBytes: typeof metadata.fileSize === "number" ? metadata.fileSize : undefined,
		source: typeof metadata.attachmentSource === "string" ? metadata.attachmentSource : undefined,
	}
}

function compactPaths(items: unknown[]): string[] {
	return items.map((item) => typeof item === "string" ? item : (asRecord(item)?.path as string) || (asRecord(item)?.fileName as string) || "").filter(Boolean)
}

function terminalText(record: Record<string, unknown>): string {
	const parts: string[] = []
	for (const key of ["output", "stdout", "stderr", "content", "text", "message", "error"]) {
		const value = record[key]
		if (typeof value === "string" && value.trim()) parts.push(value.replace(/\s+$/, ""))
	}
	return parts.join("\n")
}

export function parseToolResult(result: unknown): ParsedToolResult {
	const out: ParsedToolResult = { labels: [], files: [], text: "", summaries: [], raw: result }
	if (typeof result === "string") {
		const nested = tryJson(result)
		if (nested) return parseToolResult(nested)
		out.text = result
		return out
	}
	const record = asRecord(result)
	if (!record) return out
	// headerLabel is transient progress only. The UI never treats it as response data.
	out.labels = flattenLabels(record.headerLabel ?? record.headerLabels)
	for (const source of [record.computerFileSteps, record.attachments]) {
		if (!Array.isArray(source)) continue
		source.forEach((item, index) => { const file = fileFrom(item, out.files.length + index); if (file) out.files.push(file) })
	}
	if (typeof record.statusCode === "number") out.statusCode = record.statusCode

	for (const key of ["entries", "files", "paths", "results"]) {
		const value = record[key]
		if (!Array.isArray(value)) continue
		const paths = compactPaths(value)
		if (paths.length) out.text = paths.join("\n")
	}
	if (Array.isArray(record.tools)) {
		out.summaries.push(`${record.tools.length} инструментов доступно`)
		// Схемы сотен инструментов не выводим сырым JSON: это служебный каталог.
		out.rest = undefined
	}

	let text = terminalText(record)
	// MCP envelope: content: [{type:"text", text:"..."}].
	if (!text && Array.isArray(record.content)) {
		text = record.content.map(item => {
			const row = asRecord(item)
			return typeof row?.text === "string" ? row.text : ""
		}).filter(Boolean).join("\n")
	}
	const nested = text ? tryJson(text) : null
	if (nested) {
		const inner = parseToolResult(nested)
		out.files.push(...inner.files.filter((f) => !out.files.some((x) => x.fileUrl === f.fileUrl && x.fileName === f.fileName)))
		out.summaries.push(...inner.summaries)
		out.text = inner.text
		if (inner.rest) out.rest = inner.rest
		if (out.statusCode === undefined) out.statusCode = inner.statusCode
	} else out.text = text
	// Транспортные подтверждения без полезной нагрузки не показываем как
	// «Connected / пусто». Для пользователя это просто завершённый шаг.
	if (/^(connected|completed|done|success|ok)$/i.test(out.text.trim())) {
		out.text = ""
		if (!out.summaries.includes("Готово")) out.summaries.push("Готово")
	}

	if (!out.rest) {
		const known = new Set(["output", "stdout", "stderr", "content", "text", "message", "error", "headerLabel", "headerLabels", "computerFileSteps", "attachments", "entries", "files", "paths", "results", "tools", "structuredContent", "finished", "processId", "statusCode", "fileName", "fileUri", "fileUrl", "id", "type", "metadata", "contentType", "name", "moduleName"])
		const rest: Record<string, unknown> = {}
		for (const [key, value] of Object.entries(record)) if (!known.has(key) && !SECRET_KEYS.test(key)) rest[key] = sanitize(value)
		if (Object.keys(rest).length) out.rest = rest
	}
	return out
}

const ARG_LABELS: Record<string, string> = { files: "Файлы", entries: "Элементы", paths: "Пути", dir: "Папка", path: "Путь", url: "Адрес", query: "Запрос", command: "Команда", cwd: "Рабочая папка", toolName: "Инструмент", toolArguments: "Параметры" }

export function summarizeToolArgs(args: Record<string, unknown>): Array<{ key: string; summary: string; values?: string[] }> {
	return Object.entries(args).filter(([key]) => !SECRET_KEYS.test(key)).map(([key, value]) => {
		const label = ARG_LABELS[key] || key.replace(/([a-z])([A-Z])/g, "$1 $2")
		if (Array.isArray(value)) {
			const paths = compactPaths(value)
			return { key: label, summary: paths.length ? "" : `${value.length} элементов`, values: paths.length ? paths : undefined }
		}
		if (typeof value === "string") return { key: label, summary: value.length > 320 ? `${value.slice(0, 317)}…` : value }
		if (typeof value === "object" && value) {
			const clean = sanitize(value) as Record<string, unknown>
			const pairs = Object.entries(clean).map(([nestedKey, nestedValue]) => `${ARG_LABELS[nestedKey] || nestedKey}: ${String(nestedValue)}`)
			return { key: label, summary: pairs.length <= 2 ? pairs.join(" · ") : `${pairs.length} параметров`, values: pairs.length > 2 ? pairs : undefined }
		}
		return { key: label, summary: String(value) }
	})
}

function baseName(path: string) {
	const clean = path.replace(/[\\/]+$/, "")
	return clean.split(/[\\/]/).pop() || clean
}

function presentedTool(part: { name: string; server?: string; args?: Record<string, unknown>; done?: boolean }) {
	let name = part.name
	let args = part.args ?? {}
	// Обёртки бывают вложенными: callFunction -> mcpServer.runTool -> fs_read_file.
	// Разворачиваем до реального действия, а не останавливаемся на «runTool».
	for (let depth = 0; depth < 4; depth++) {
		if (/callFunction/i.test(name) && typeof args.function === "string") {
			name = args.function.split(".").pop() || name
			args = asRecord(args.args) ?? args
			continue
		}
		if (/runTool/i.test(name) && typeof args.toolName === "string") {
			name = args.toolName
			args = asRecord(args.toolArguments) ?? args
			continue
		}
		break
	}
	const normalized = name.replace(/^connections[._]/, "").replace(/[./]/g, "_").toLowerCase()
	const path = typeof args.path === "string" ? args.path : typeof args.file_path === "string" ? args.file_path : ""
	const dir = typeof args.dir === "string" ? args.dir : typeof args.cwd === "string" ? args.cwd : ""
	const files = Array.isArray(args.files) ? args.files.filter((x): x is string => typeof x === "string") : []
	const query = typeof args.query === "string" ? args.query : ""
	const done = part.done !== false
	let action = name.replace(/_/g, " ")
	if (/readfiles?$|fs_read_files?/.test(normalized)) action = files.length > 1 ? `${done ? "Прочитаны" : "Читаются"} файлы · ${files.length}` : `${done ? "Прочитан" : "Читается"} файл${files[0] ? ` · ${baseName(files[0])}` : path ? ` · ${baseName(path)}` : ""}`
	else if (/readdir$|listdir$|fs_list_dir/.test(normalized)) action = `${done ? "Просмотрена" : "Открывается"} папка${dir || path ? ` · ${baseName(dir || path)}` : ""}`
	else if (/search|grep/.test(normalized)) action = `${done ? "Выполнен" : "Выполняется"} поиск${query ? ` · ${query.slice(0, 48)}` : ""}`
	else if (/writefile$|fs_write_file/.test(normalized)) action = `${done ? "Записан" : "Записывается"} файл${path ? ` · ${baseName(path)}` : ""}`
	else if (/editfile$|patchfile$|applypatch|fs_patch_file/.test(normalized)) action = `${done ? "Изменён" : "Изменяется"} файл${path ? ` · ${baseName(path)}` : ""}`
	else if (/terminal|command|exec|run$/.test(normalized)) action = done ? "Выполнена команда" : "Выполняется команда"
	else if (/download/.test(normalized)) action = `Подготовлен файл${path ? ` · ${baseName(path)}` : ""}`
	else if (/upload/.test(normalized)) action = `Загружен файл${path ? ` · ${baseName(path)}` : ""}`
	else if (/loadpage/.test(normalized)) action = "Открыта страница"
	else if (/searchusers?/.test(normalized)) action = "Найдены пользователи"
	else if (/listtools/.test(normalized)) action = "Получен список инструментов"
	return { name, args, action }
}

export function toolIdentity(part: { name: string; server?: string; args?: Record<string, unknown>; done?: boolean }) {
	const display = presentedTool(part)
	const raw = `${part.server ?? ""} ${display.name} ${JSON.stringify(part.args ?? {})}`.toLowerCase()
	if (raw.includes("notcode") || /^(fs_|terminal_|git_|workspace_|notcode_)/.test(display.name)) return { source: "NotCode", mark: "N", ...display }
	if (raw.includes("computer") || /(^|[._/])(fs|computer)([._/]|$)/.test(raw)) return { source: "Computer", mark: "C", ...display }
	if (raw.includes("notion")) return { source: "Notion", mark: "N", ...display }
	if (raw.includes("system")) return { source: "System", mark: "S", ...display }
	return { source: part.server || "MCP", mark: "M", ...display }
}

export function fileSize(bytes?: number) { if (!bytes || bytes <= 0) return ""; if (bytes < 1024) return `${bytes} Б`; if (bytes < 1048576) return `${Math.round(bytes / 1024)} КБ`; return `${(bytes / 1048576).toFixed(1)} МБ` }
const PREVIEWABLE = /\.(html?|svg|md|txt|json|csv|ya?ml|js|jsx|ts|tsx|css|py|go|rs|sql|sh|xml)$/i
export function canPreview(file: ComputerFile) { return PREVIEWABLE.test(file.fileName) || file.contentType.startsWith("text/") || file.contentType === "image/svg+xml" || file.contentType.startsWith("image/") }
export function isImage(file: ComputerFile) { return file.contentType.startsWith("image/") || /\.(png|jpe?g|gif|webp|bmp|avif)$/i.test(file.fileName) }
