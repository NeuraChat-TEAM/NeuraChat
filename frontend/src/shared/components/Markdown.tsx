import { useCallback, useMemo, type MouseEvent } from "react"
import { renderNotionMarkdown } from "../lib/notionMarkdown"
import { markTail } from "../lib/tailReveal"
import { api } from "../api/api"
import { toast } from "../lib/toast"

// Стрим-безопасный рендер: парсится на каждой дельте, санитаизится перед
// вставкой. Понимает весь Notion-диалект: цвета, каллауты, тогглы,
// колонки, табы, упоминания, формулы и вложения.
//
// `live` включает побуквенный хвост: последние символы оборачиваются в
// span'ы с расстоянием до конца текста, и CSS делает из этого плавное
// проявление с блюром и затемнением.

/** Копирование без зависимости от разрешений clipboard в WebView. */
async function copyText(text: string) {
	try {
		await navigator.clipboard.writeText(text)
		return true
	} catch {
		try {
			const field = document.createElement("textarea")
			field.value = text
			field.style.position = "fixed"
			field.style.opacity = "0"
			document.body.appendChild(field)
			field.select()
			const ok = document.execCommand("copy")
			field.remove()
			return ok
		} catch {
			return false
		}
	}
}

/** Адрес источника из иконки-сноски под курсором. */
function citeUrl(target: EventTarget | null) {
	if (!(target instanceof HTMLElement)) return ""
	const chip = target.closest("[data-cite-url]")
	return chip?.getAttribute("data-cite-url") ?? ""
}

export default function Markdown({ text, live }: { text: string; live?: boolean }) {
	const html = useMemo(() => (text ? renderNotionMarkdown(text) : ""), [text])
	const out = useMemo(() => (live ? markTail(html) : html), [html, live])

	// ЛКМ по иконке-сноске — открыть источник во внешнем браузере.
	const onClick = useCallback((event: MouseEvent<HTMLDivElement>) => {
		const url = citeUrl(event.target)
		if (!url) return
		event.preventDefault()
		api.openURL(url).catch(() => {
			window.open(url, "_blank", "noreferrer")
		})
	}, [])

	// ПКМ — скопировать адрес и показать тост вместо системного меню.
	const onContextMenu = useCallback((event: MouseEvent<HTMLDivElement>) => {
		const url = citeUrl(event.target)
		if (!url) return
		event.preventDefault()
		void copyText(url).then((ok) => {
			if (ok) toast("Ссылка скопирована")
			else toast("Не удалось скопировать ссылку", true)
		})
	}, [])

	return (
		<div
			className="answer prose-notion"
			onClick={onClick}
			onContextMenu={onContextMenu}
			dangerouslySetInnerHTML={{ __html: out }}
		/>
	)
}
