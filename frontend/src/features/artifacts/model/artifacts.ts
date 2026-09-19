// Артефакты — файлы, которые модель «собрала» прямо в ответе.
//
// Из потока ответа мы вытаскиваем код-блоки, которые выглядят как готовый
// файл (есть имя файла или блок достаточно большой), и показываем их
// карточками, как это делает Notion, а не «простыней» кода в тексте.

export type ArtifactKind = "web" | "code" | "data" | "doc"

export type Artifact = {
	id: string
	/** Имя без расширения: `calculator`. */
	name: string
	/** Расширение с точкой: `.html`. */
	ext: string
	lang: string
	code: string
	kind: ArtifactKind
	mime?: string
	dataBase64?: string
	/** Файл ещё тянется из Notion: панель уже открыта и показывает загрузку. */
	loading?: boolean
	/** Загрузка упала — текст ошибки для панели. */
	error?: string
}

export type SurveyOption = { id: string; label: string }

export type Survey = {
	id: string
	question: string
	options: SurveyOption[]
	allowMultiple: boolean
	allowOther: boolean
}

const LANG_EXT: Record<string, string> = {
	html: ".html",
	htm: ".html",
	svg: ".svg",
	xml: ".xml",
	markdown: ".md",
	md: ".md",
	javascript: ".js",
	js: ".js",
	jsx: ".jsx",
	typescript: ".ts",
	ts: ".ts",
	tsx: ".tsx",
	python: ".py",
	py: ".py",
	go: ".go",
	rust: ".rs",
	rs: ".rs",
	json: ".json",
	yaml: ".yaml",
	yml: ".yml",
	css: ".css",
	sql: ".sql",
	sh: ".sh",
	bash: ".sh",
	csv: ".csv",
}

const WEB = new Set([".html", ".htm", ".svg"])
const DATA = new Set([".json", ".csv", ".yaml", ".yml", ".xml", ".sql"])
const DOC = new Set([".md", ".txt"])

/** Мелкие сниппеты остаются обычным кодом внутри ответа. */
const MIN_LINES = 8

export function artifactKind(ext: string): ArtifactKind {
	if (WEB.has(ext)) return "web"
	if (DATA.has(ext)) return "data"
	if (DOC.has(ext)) return "doc"
	return "code"
}

export function artifactFile(a: Artifact) {
	return `${a.name}${a.ext}`
}

/** Документ для предпросмотра в правой панели-браузере. */
export function previewHtml(a: Artifact): string {
	if (a.dataBase64 && a.mime?.startsWith("image/")) {
		return `<!doctype html><meta charset="utf-8"><style>html,body{height:100%;margin:0;background:#111}body{display:grid;place-items:center}img{max-width:100%;max-height:100%;object-fit:contain}</style><img alt="${escapeHtml(a.name)}" src="data:${a.mime};base64,${a.dataBase64}">`
	}
	if (a.ext === ".svg") {
		return `<!doctype html><meta charset="utf-8"><body style="margin:0;display:grid;place-items:center;min-height:100vh;background:#fff">${a.code}</body>`
	}
	if (a.kind === "web") return a.code
	return `<!doctype html><meta charset="utf-8"><body style="margin:0;padding:16px;font:13px/1.5 ui-monospace,monospace;white-space:pre-wrap">${escapeHtml(
		a.code,
	)}</body>`
}

export function escapeHtml(s: string) {
	return s
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;")
		.replace(/"/g, "&quot;")
}

const FENCE = /```([^\n`]*)\n([\s\S]*?)```/g
const FILE_TAIL = /(?:^|[\s(«"'`*])([\w.\-/]+\.[A-Za-z0-9]{1,6})[\s)»"'`*:—-]*$/

function fileToken(info: string) {
	const tokens = info.split(/[\s,]+/).filter(Boolean)
	const lang = tokens[0] && !tokens[0].includes(".") ? tokens[0].toLowerCase() : ""
	const file = tokens.find((t) => /^[\w.\-/]+\.[A-Za-z0-9]{1,6}$/.test(t) && !t.startsWith("."))
	return { lang, file }
}

/**
 * Делит ответ на текст и артефакты. Сами код-блоки-файлы вырезаются из
 * текста: вместо них в чате появляются карточки.
 */
export function splitArtifacts(text: string): {
	body: string
	artifacts: Artifact[]
	surveys: Survey[]
} {
	if (!text || !text.includes("```")) return { body: text, artifacts: [], surveys: [] }

	const artifacts: Artifact[] = []
	const surveys: Survey[] = []
	const re = new RegExp(FENCE.source, "g")
	let body = ""
	let last = 0
	let m: RegExpExecArray | null

	while ((m = re.exec(text)) !== null) {
		const info = (m[1] ?? "").trim()
		const code = (m[2] ?? "").replace(/\s+$/, "")
		let before = text.slice(last, m.index)

		// ```survey — интерактивный опросник в чате.
		if (/^survey\b/i.test(info)) {
			const survey = parseSurvey(code, surveys.length)
			if (survey) {
				surveys.push(survey)
				body += before
				last = m.index + m[0].length
				continue
			}
		}

		const { lang, file: infoFile } = fileToken(info)
		let file = infoFile
		let stripLine = false

		if (!file) {
			const tailLine = before.replace(/\s+$/, "").split("\n").pop() ?? ""
			const hit = tailLine.match(FILE_TAIL)
			if (hit && tailLine.trim().length <= 80) {
				file = hit[1]
				stripLine = true
			}
		}

		const ext = file ? file.slice(file.lastIndexOf(".")).toLowerCase() : (LANG_EXT[lang] ?? "")
		const lines = code ? code.split("\n").length : 0
		const worthy = !!file || (WEB.has(ext) && lines >= 3) || (!!ext && lines >= MIN_LINES)

		if (!worthy || !code) continue

		if (stripLine) {
			const keep = before.replace(/\s+$/, "").split("\n")
			keep.pop()
			before = keep.join("\n")
		}

		const index = artifacts.length
		const base = file
			? (file.slice(0, file.length - ext.length).split("/").pop() || `artifact-${index + 1}`)
			: `artifact-${index + 1}`

		artifacts.push({
			id: `art-${index + 1}`,
			name: base,
			ext: ext || ".txt",
			lang: lang || ext.replace(".", ""),
			code,
			kind: artifactKind(ext),
		})

		body += before
		last = m.index + m[0].length
	}

	if (artifacts.length === 0 && surveys.length === 0) {
		return { body: text, artifacts: [], surveys: [] }
	}

	body += text.slice(last)
	return { body: body.replace(/\n{3,}/g, "\n\n").trim(), artifacts, surveys }
}

function parseSurvey(code: string, index: number): Survey | null {
	try {
		const raw = JSON.parse(code) as {
			question?: string
			prompt?: string
			options?: Array<string | { id?: string; label?: string }>
			allowMultiple?: boolean
			allowOther?: boolean
		}
		const question = (raw.question ?? raw.prompt ?? "").trim()
		const options = (raw.options ?? [])
			.map((o, i) =>
				typeof o === "string"
					? { id: `o${i + 1}`, label: o }
					: { id: o.id || `o${i + 1}`, label: (o.label ?? "").trim() },
			)
			.filter((o) => o.label !== "")
			.slice(0, 6)
		if (!question || options.length === 0) return null
		return {
			id: `survey-${index + 1}`,
			question,
			options,
			allowMultiple: raw.allowMultiple !== false,
			allowOther: raw.allowOther !== false,
		}
	} catch {
		return null
	}
}
