// Разбор результата шага (tool-result) от Notion.
//
// Notion присылает в шаге сразу несколько вещей вперемешку:
//   { output: "<строка, часто ещё один JSON>",
//     headerLabel: [["Shared file"]],
//     computerFileSteps: [{ fileName, fileUrl, contentType, ... }] }
// Раньше это всё валилось в чат «сырым JSON». Здесь мы вытаскиваем:
//   • подписи шага (headerLabel) — «Writing index.html», «Shape rotating»…
//   • файлы компьютера (computerFileSteps) — карточки-артефакты;
//   • человекочитаемый вывод (stdout/stderr, текст, финальный JSON).

export type ComputerFile = {
	id: string
	fileName: string
	/** attachment:<uuid>:<name> — по нему бэкенд достаёт подписанную ссылку. */
	fileUrl: string
	contentType: string
	sizeBytes?: number
	source?: string
}

export type ParsedToolResult = {
	/** Подписи шага в порядке появления. */
	labels: string[]
	/** Файлы, которые шаг создал или показал. */
	files: ComputerFile[]
	/** Текстовый вывод: stdout, stderr, сообщение об ошибке. */
	text: string
	/** Код возврата терминала, если шаг был терминальным. */
	statusCode?: number
	/** Остаток, который не удалось разобрать — показываем деревом. */
	rest?: unknown
	/** Ничего осмысленного не нашли — рисуем как раньше. */
	raw: unknown
}

function asRecord(value: unknown): Record<string, unknown> | null {
	return value && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null
}

/** Строки вида "{\"fileName\":…}" — это JSON внутри JSON. */
function tryJson(text: string): unknown {
	const trimmed = text.trim()
	if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return null
	try {
		return JSON.parse(trimmed)
	} catch {
		return null
	}
}

/** headerLabel приходит массивом массивов строк (иногда с объектами). */
function flattenLabels(value: unknown, out: string[] = []): string[] {
	if (typeof value === "string") {
		const text = value.trim()
		if (text) out.push(text)
		return out
	}
	if (Array.isArray(value)) {
		for (const item of value) flattenLabels(item, out)
		return out
	}
	const record = asRecord(value)
	if (record) {
		for (const key of ["text", "label", "title", "plainText"]) {
			if (typeof record[key] === "string") return flattenLabels(record[key], out)
		}
	}
	return out
}

function fileFrom(value: unknown, index: number): ComputerFile | null {
	const record = asRecord(value)
	if (!record) return null
	const fileUrl = typeof record.fileUrl === "string" ? record.fileUrl : ""
	const fromUri = typeof record.fileUri === "string" ? record.fileUri : ""
	const name =
		(typeof record.fileName === "string" && record.fileName) ||
		(typeof record.name === "string" && record.name) ||
		""
	if (!name && !fileUrl) return null
	const metadata = asRecord(record.metadata) ?? {}
	const size = typeof metadata.fileSize === "number" ? metadata.fileSize : undefined
	return {
		id: typeof record.id === "string" ? record.id : `file-${index + 1}`,
		fileName: name || "file",
		fileUrl: fileUrl || fromUri,
		contentType:
			typeof record.contentType === "string" ? record.contentType : "application/octet-stream",
		sizeBytes: size,
		source: typeof metadata.attachmentSource === "string" ? metadata.attachmentSource : undefined,
	}
}

/** Терминальный ответ: { output, finished, processId, statusCode }. */
function terminalText(record: Record<string, unknown>): string {
	const parts: string[] = []
	for (const key of ["output", "stdout", "stderr", "content", "text", "message", "error"]) {
		const value = record[key]
		if (typeof value === "string" && value.trim()) parts.push(value.replace(/\s+$/, ""))
	}
	return parts.join("\n")
}

export function parseToolResult(result: unknown): ParsedToolResult {
	const out: ParsedToolResult = { labels: [], files: [], text: "", raw: result }

	if (typeof result === "string") {
		const nested = tryJson(result)
		if (nested) return parseToolResult(nested)
		out.text = result
		return out
	}

	const record = asRecord(result)
	if (!record) return out

	out.labels = flattenLabels(record.headerLabel ?? record.headerLabels)

	const fileSources = [record.computerFileSteps, record.files, record.attachments]
	for (const source of fileSources) {
		if (!Array.isArray(source)) continue
		source.forEach((item, index) => {
			const file = fileFrom(item, out.files.length + index)
			if (file) out.files.push(file)
		})
	}

	if (typeof record.statusCode === "number") out.statusCode = record.statusCode

	// output часто содержит вложенный JSON с именем файла — он уже показан карточкой.
	const text = terminalText(record)
	const nested = text ? tryJson(text) : null
	if (nested) {
		const inner = parseToolResult(nested)
		out.files.push(...inner.files.filter((f) => !out.files.some((x) => x.fileName === f.fileName)))
		out.labels.push(...inner.labels.filter((l) => !out.labels.includes(l)))
		out.text = inner.text
		if (out.statusCode === undefined) out.statusCode = inner.statusCode
	} else {
		out.text = text
	}

	// Всё, что не разобрали, оставляем деревом — но без шума известных полей.
	const known = new Set([
		"output",
		"stdout",
		"stderr",
		"content",
		"text",
		"message",
		"error",
		"headerLabel",
		"headerLabels",
		"computerFileSteps",
		"files",
		"attachments",
		"finished",
		"processId",
		"statusCode",
		"fileName",
		"fileUri",
		"fileUrl",
		"id",
		"type",
		"metadata",
		"contentType",
	])
	const rest: Record<string, unknown> = {}
	for (const [key, value] of Object.entries(record)) {
		if (!known.has(key)) rest[key] = value
	}
	if (Object.keys(rest).length > 0) out.rest = rest
	return out
}

/** Человеческий размер файла. */
export function fileSize(bytes?: number) {
	if (!bytes || bytes <= 0) return ""
	if (bytes < 1024) return `${bytes} Б`
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} КБ`
	return `${(bytes / 1024 / 1024).toFixed(1)} МБ`
}

const PREVIEWABLE = /\.(html?|svg|md|txt|json|csv|ya?ml|js|jsx|ts|tsx|css|py|go|rs|sql|sh|xml)$/i

/** Файл, который можно показать в правой панели-браузере. */
export function canPreview(file: ComputerFile) {
	return (
		PREVIEWABLE.test(file.fileName) ||
		file.contentType.startsWith("text/") ||
		file.contentType === "image/svg+xml"
	)
}

/** Картинка — показываем сразу превью в шаге. */
export function isImage(file: ComputerFile) {
	return (
		file.contentType.startsWith("image/") || /\.(png|jpe?g|gif|webp|bmp|avif)$/i.test(file.fileName)
	)
}
