// Рендер Notion-flavored Markdown.
//
// Обычный marked умеет только CommonMark + GFM, а ассистент присылает
// свои XML-теги: <callout>, <toggle>, <columns>, <tabs>, <span color>, <mention>,
// <equation>, <todo>, <image>, <pdf>, <bookmark> и так далее. Здесь они
// распарсены рекурсивно: внешняя оболочка становится HTML, а всё
// содержимое внутри прогоняется через тот же рендерер, чтобы внутри
// каллаутов и колонок работал обычный markdown.

import { marked } from "marked"
import DOMPurify from "dompurify"
import katex from "katex"
import "katex/dist/katex.min.css"

/**
 * Настоящая математика через KaTeX.
 * При ошибке в формуле (часто во время стрима, когда выражение ещё
 * не дописано) отдаём исходный текст, а не красную ошибку.
 */
export function renderTex(source: string, display: boolean) {
	const tex = source.trim()
	if (!tex) return ""
	try {
		return katex.renderToString(tex, {
			displayMode: display,
			throwOnError: false,
			errorColor: "currentColor",
			strict: false,
			trust: false,
			output: "html",
			macros: { "\\RR": "\\mathbb{R}", "\\NN": "\\mathbb{N}", "\\ZZ": "\\mathbb{Z}" },
		})
	} catch {
		return escapeHtml(tex)
	}
}

marked.setOptions({ gfm: true, breaks: true })

/** Блочные теги, у которых есть содержимое с вложенным markdown. */
const BLOCK_TAGS = [
	"callout",
	"quote",
	"toggle",
	"columns",
	"column",
	"tabs",
	"tab",
	"todo",
	"synced-block",
	"synced-block-reference",
	"meeting-notes",
	"notes",
	"summary",
	"mail",
	"equation",
	"page",
	"database",
	"folder",
	"image",
	"pdf",
	"file",
	"audio",
	"video",
	"bookmark",
	"embed",
	"external_object_instance",
]

/** Цвета Notion → CSS-классы из notion.css. */
export function colorClass(color?: string | null) {
	const value = (color ?? "").trim()
	if (!value) return ""
	if (value.endsWith("_bg")) return `n-bg-${value.slice(0, -3)}`
	return `n-fg-${value}`
}

function escapeHtml(text: string) {
	return text
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;")
		.replace(/"/g, "&quot;")
}

/** Разбор атрибутов вида color="blue" icon="💡". */
function parseAttrs(source: string) {
	const attrs: Record<string, string> = {}
	const pattern = /([\w:-]+)\s*=\s*"([^"]*)"/g
	let match: RegExpExecArray | null
	while ((match = pattern.exec(source))) attrs[match[1]] = match[2]
	return attrs
}

/**
 * Ищет первый блочный тег и возвращает его границы с учётом
 * одноимённых вложений (колонка в колонке, тоггл в тоггле).
 */
function findBlock(text: string) {
	const open = new RegExp(`<(${BLOCK_TAGS.join("|")})(\\s[^>]*?)?(/?)>`, "i")
	const found = open.exec(text)
	if (!found) return null

	const name = found[1].toLowerCase()
	const attrs = parseAttrs(found[2] ?? "")
	const start = found.index
	const afterOpen = start + found[0].length
	if (found[3] === "/") {
		return { name, attrs, inner: "", start, end: afterOpen }
	}

	// Ищем парный закрывающий тег, считая вложенные одноимённые.
	const scan = new RegExp(`<(/?)${name}(\\s[^>]*?)?(/?)>`, "gi")
	scan.lastIndex = afterOpen
	let depth = 1
	let step: RegExpExecArray | null
	while ((step = scan.exec(text))) {
		if (step[3] === "/") continue
		depth += step[1] === "/" ? -1 : 1
		if (depth === 0) {
			return {
				name,
				attrs,
				inner: text.slice(afterOpen, step.index),
				start,
				end: step.index + step[0].length,
			}
		}
	}
	// Стриминг: закрывающий тег ещё не пришёл — рисуем то, что есть.
	return { name, attrs, inner: text.slice(afterOpen), start, end: text.length }
}

/** Отделяет «заголовок блока» от дочернего содержимого (пустая строка). */
function splitTitle(inner: string) {
	const trimmed = inner.replace(/^\n+/, "").replace(/\s+$/, "")
	const blank = trimmed.search(/\n[ \t]*\n/)
	if (blank < 0) return { title: trimmed, children: "" }
	return {
		title: trimmed.slice(0, blank).trim(),
		children: trimmed.slice(blank).replace(/^\s*\n/, ""),
	}
}

/** Короткое имя фаила/ссылки для подписи вложения. */
function shortName(url: string) {
	try {
		const parsed = new URL(url)
		const last = parsed.pathname.split("/").filter(Boolean).pop()
		return decodeURIComponent(last || parsed.host)
	} catch {
		return url.split(/[\\/]/).pop() || url
	}
}

/** Инлайновые теги → HTML до передачи в marked. */
function inlineTags(text: string) {
	return (
		text
			// <span color="blue">текст</span>
			.replace(
				/<span\s+([^>]*)>([\s\S]*?)<\/span>/gi,
				(_all, rawAttrs: string, body: string) => {
					const attrs = parseAttrs(rawAttrs)
					return `<span class="${colorClass(attrs.color)}">${body}</span>`
				},
			)
			// Упоминания страниц, баз, людей и агентов.
			.replace(
				/<mention\s+([^>]*?)\/>|<mention\s+([^>]*?)>([\s\S]*?)<\/mention>/gi,
				(_all, selfAttrs: string, pairAttrs: string, label: string) => {
					const attrs = parseAttrs(selfAttrs ?? pairAttrs ?? "")
					const url = attrs.url ?? ""
					const text2 = (label ?? "").trim() || shortName(url) || "Страница"
					const icon = attrs.type === "database" ? "🗂️" : "📄"
					if (/^https?:/i.test(url)) {
						return `<a class="n-mention" href="${escapeHtml(url)}" target="_blank" rel="noreferrer">${icon} ${text2}</a>`
					}
					return `<span class="n-mention">${icon} ${text2}</span>`
				},
			)
			// Даты: <date start="2026-01-01" start-time="10:00" />
			.replace(/<date\s+([^>]*?)\/?>/gi, (_all, rawAttrs: string) => {
				const attrs = parseAttrs(rawAttrs)
				const left = [attrs.start, attrs["start-time"]].filter(Boolean).join(" ")
				const right = [attrs.end, attrs["end-time"]].filter(Boolean).join(" ")
				const label = right ? `${left} \u2192 ${right}` : left
				return `<span class="n-date">@${escapeHtml(label)}</span>`
			})
			// Подчёркивание — единственный тег, который в markdown невыразим.
			.replace(/<u>/gi, '<u class="n-u">')
			// Пустой абзац и содержательные <p>.
			.replace(/<p\s*\/>/gi, '<p class="n-empty">&nbsp;</p>')
			// Инлайновая математика $...$ без KaTeX — моноширинно.
			.replace(
				/(^|[^$\\])\$([^$\n]+)\$(?!\$)/g,
				(_all, before: string, body: string) =>
					`${before}<span class="n-math">${renderTex(body, false)}</span>`,
			)
	)
}

/** Блочная математика: строки с $$ до/после выражения. */
function displayMath(text: string) {
	return text.replace(
		/^\$\$[ \t]*\n([\s\S]*?)\n\$\$[ \t]*$/gm,
		(_all, body: string) => `<div class="n-math n-math--block">${renderTex(body, true)}</div>`,
	)
}

/** Список дел с цветом: <todo checked="true">. */
function renderTodo(attrs: Record<string, string>, inner: string) {
	const checked = attrs.checked === "true"
	return (
		`<div class="n-todo ${colorClass(attrs.color)}">` +
		`<input type="checkbox" disabled${checked ? " checked" : ""} />` +
		`<div class="n-todo__body${checked ? " n-todo__body--done" : ""}">${render(inner)}</div>` +
		"</div>"
	)
}

/** Обёртка для файловых блоков: pdf/file/audio/video/bookmark/embed. */
function renderMedia(
	name: string,
	attrs: Record<string, string>,
	caption: string,
): string {
	const source = attrs.source ?? attrs.url ?? ""
	const title = caption.trim() || shortName(source)
	const icons: Record<string, string> = {
		pdf: "📕",
		file: "📎",
		audio: "🎧",
		video: "🎬",
		bookmark: "🔗",
		embed: "🧱",
		external_object_instance: "🔌",
	}
	if (name === "image") {
		return (
			`<figure class="n-image"><img src="${escapeHtml(source)}" alt="${escapeHtml(title)}" />` +
			(caption.trim() ? `<figcaption>${render(caption)}</figcaption>` : "") +
			"</figure>"
		)
	}
	if (name === "video" && source) {
		return `<video class="n-media__player" controls src="${escapeHtml(source)}"></video>`
	}
	if (name === "audio" && source) {
		return `<audio class="n-media__player" controls src="${escapeHtml(source)}"></audio>`
	}
	const icon = icons[name] ?? "📄"
	const href = /^https?:/i.test(source) ? source : ""
	const body =
		`<span class="n-media__icon">${icon}</span>` +
		`<span class="n-media__name">${escapeHtml(title)}</span>` +
		`<span class="n-media__kind">${name.replace(/_/g, " ")}</span>`
	return href
		? `<a class="n-media" href="${escapeHtml(href)}" target="_blank" rel="noreferrer">${body}</a>`
		: `<div class="n-media">${body}</div>`
}

/** Ссылки на страницу/базу/папку как строка-карточка. */
function renderEntity(name: string, attrs: Record<string, string>, inner: string) {
	const icons: Record<string, string> = { page: "📄", database: "🗂️", folder: "📁" }
	const icon = attrs.icon || icons[name] || "📄"
	const label = inner.trim() || "Без названия"
	const href = /^https?:/i.test(attrs.url ?? "") ? attrs.url : ""
	const body = `<span class="n-entity__icon">${icon}</span><span>${escapeHtml(label)}</span>`
	return href
		? `<a class="n-entity" href="${escapeHtml(href)}" target="_blank" rel="noreferrer">${body}</a>`
		: `<div class="n-entity">${body}</div>`
}

/** Один блочный тег → HTML. */
function renderBlock(
	name: string,
	attrs: Record<string, string>,
	inner: string,
): string {
	const color = colorClass(attrs.color)
	switch (name) {
		case "callout": {
			const { title, children } = splitTitle(inner)
			return (
				`<div class="n-callout ${color}">` +
				`<div class="n-callout__icon">${attrs.icon || "💡"}</div>` +
				`<div class="n-callout__body">${render(title)}${render(children)}</div>` +
				"</div>"
			)
		}
		case "quote": {
			const { title, children } = splitTitle(inner)
			return `<blockquote class="n-quote ${color}">${render(title)}${render(children)}</blockquote>`
		}
		case "toggle": {
			const { title, children } = splitTitle(inner)
			const heading = attrs.heading
			const label = heading
				? `<${heading} class="n-toggle__heading">${render(title)}</${heading}>`
				: render(title)
			return (
				`<details class="n-toggle ${color}"><summary>${label}</summary>` +
				`<div class="n-toggle__body">${render(children)}</div></details>`
			)
		}
		case "columns":
			return `<div class="n-columns">${render(inner)}</div>`
		case "column": {
			const ratio = Number(attrs.ratio)
			const style = ratio > 0 ? ` style="flex:${ratio}"` : ""
			return `<div class="n-column"${style}>${render(inner)}</div>`
		}
		case "tabs":
			return `<div class="n-tabs">${render(inner)}</div>`
		case "tab": {
			const { title, children } = splitTitle(inner)
			return (
				`<details class="n-tab" open><summary>${attrs.icon ?? ""} ${escapeHtml(
					title,
				)}</summary><div class="n-tab__body">${render(children)}</div></details>`
			)
		}
		case "todo":
			return renderTodo(attrs, inner)
		case "equation":
			return `<div class="n-math n-math--block ${color}">${renderTex(inner, true)}</div>`
		case "synced-block":
		case "synced-block-reference":
			return (
				`<div class="n-synced"><div class="n-synced__label">${
					attrs.notice || "Синхронизированный блок"
				}</div>${render(inner)}</div>`
			)
		case "meeting-notes": {
			const { title, children } = splitTitle(inner)
			return (
				`<div class="n-meeting"><div class="n-meeting__title">🎙️ ${escapeHtml(
					title,
				)}</div>${render(children)}</div>`
			)
		}
		case "notes":
		case "summary":
			return (
				`<div class="n-meeting__part"><div class="n-meeting__part-label">${
					name === "notes" ? "Заметки" : "Итоги"
				}</div>${render(inner)}</div>`
			)
		case "mail": {
			const rows = [
				["Кому", attrs.to],
				["Копия", attrs.cc],
				["Скрытая", attrs.bcc],
				["Тема", attrs.subject],
			]
				.filter(([, value]) => value)
				.map(
					([label, value]) =>
						`<div class="n-mail__row"><span>${label}</span>${escapeHtml(value ?? "")}</div>`,
				)
				.join("")
			return `<div class="n-mail">${rows}<div class="n-mail__body">${render(inner)}</div></div>`
		}
		case "page":
		case "database":
		case "folder":
			return renderEntity(name, attrs, inner)
		default:
			return renderMedia(name, attrs, inner)
	}
}

/** Обычный markdown-фрагмент (без наших блочных тегов). */
function renderMarkdown(text: string) {
	if (!text.trim()) return ""
	const prepared = inlineTags(displayMath(text))
	return marked.parse(prepared, { async: false }) as string
}

/** Рекурсивный рендер: markdown + блочные теги Notion. */
export function render(text: string): string {
	if (!text) return ""

	// Код прячем целиком: внутри фенсов теги — это текст, а не разметка.
	const fences: string[] = []
	const masked = text.replace(/(^|\n)(`{3,}|~{3,})[\s\S]*?\n\2[ \t]*(?=\n|$)/g, (block) => {
		fences.push(block)
		return `\n\u0000FENCE${fences.length - 1}\u0000\n`
	})

	let rest = masked
	let html = ""
	for (let guard = 0; guard < 400; guard++) {
		const block = findBlock(rest)
		if (!block) break
		html += renderMarkdown(rest.slice(0, block.start))
		html += renderBlock(block.name, block.attrs, block.inner)
		rest = rest.slice(block.end)
	}
	html += renderMarkdown(rest)

	// Возвращаем код обратно уже как HTML.
	return html.replace(/\u0000FENCE(\d+)\u0000/g, (_all, index: string) =>
		(marked.parse(fences[Number(index)] ?? "", { async: false }) as string),
	)
}

/** Готовый безопасный HTML для вставки в страницу. */
export function renderNotionMarkdown(text: string) {
	// KaTeX отдаёт вложенный MathML и SVG, поэтому оба профиля включены:
	// без них формулы вырезались санитайзером до голого текста.
	return DOMPurify.sanitize(render(text), {
		ADD_TAGS: ["details", "summary", "figure", "figcaption", "video", "audio", "u"],
		ADD_ATTR: ["controls", "open", "target", "style", "checked", "disabled"],
		USE_PROFILES: { html: true, mathMl: true, svg: true },
	})
}
