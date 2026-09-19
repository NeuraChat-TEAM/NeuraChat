// Побуквенное проявление хвоста ответа.
//
// Модель присылает текст огромными кусками, поэтому "живой" ответ рос
// рывками: прилетел блок — и он целиком уже виден. Очередь проявления в
// App.tsx выдаёт символы порциями по кадрам, а этот модуль делает так,
// чтобы у самых свежих символов был градиент: последняя буква полностью
// прозрачная и размытая, дальше по нарастающей до обычного текста.
//
// Почему постобработка HTML, а не React-компонент: Markdown рендерится
// через dangerouslySetInnerHTML целиком на каждой дельте, и любая
// CSS-анимация внутри переигрывалась бы заново. Здесь анимации нет —
// прозрачность и блюр считаются из расстояния символа до конца текста,
// поэтому при росте текста градиент просто "едет" вперёд.

/** Сколько последних символов попадает в градиент. */
export const TAIL_CHARS = 42

/** Не трогаем блоки, где посимвольная разбивка сломала бы вёрстку. */
const SKIP = "pre, code, script, style, .katex, .katex-display, table, img, svg"

/** Слишком длинный ответ не перепарсиваем: экономим кадры. */
const MAX_HTML = 400_000

/**
 * Оборачивает последние символы отрендеренного HTML в `<span class="rv">`
 * с CSS-переменной `--d` — расстоянием до конца текста в символах.
 */
export function markTail(html: string, tail: number = TAIL_CHARS): string {
	if (!html || tail <= 0 || html.length > MAX_HTML) return html
	if (typeof DOMParser === "undefined") return html

	let doc: Document
	try {
		doc = new DOMParser().parseFromString(`<body>${html}</body>`, "text/html")
	} catch {
		return html
	}

	const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT)
	const nodes: Text[] = []
	while (walker.nextNode()) {
		const node = walker.currentNode as Text
		if (!node.nodeValue) continue
		const parent = node.parentElement
		if (parent && parent.closest(SKIP)) continue
		nodes.push(node)
	}
	if (nodes.length === 0) return html

	// Идём от конца документа к началу и "съедаем" нужное число символов.
	let consumed = 0
	for (let i = nodes.length - 1; i >= 0 && consumed < tail; i--) {
		const node = nodes[i]
		const chars = Array.from(node.nodeValue ?? "")
		if (chars.length === 0) continue
		const take = Math.min(tail - consumed, chars.length)
		const head = chars.slice(0, chars.length - take).join("")
		const tailChars = chars.slice(chars.length - take)

		const frag = doc.createDocumentFragment()
		if (head) frag.appendChild(doc.createTextNode(head))
		for (let k = 0; k < tailChars.length; k++) {
			const ch = tailChars[k]
			// Перевод строки оставляем как есть: span вокруг него не нужен.
			if (ch === "\n" || ch === "\r") {
				frag.appendChild(doc.createTextNode(ch))
				continue
			}
			// Пробел НИКОГДА не оборачиваем и не меняем на \u00A0: неразрывный
			// пробел запрещал перенос в этом месте, строка пересобиралась и
			// абзац на глазах «съезжал назад», когда хвост ехал дальше.
			// На невидимом символе градиент всё равно не виден.
			if (ch === " " || ch === "\t") {
				frag.appendChild(doc.createTextNode(ch))
				continue
			}
			const distance = consumed + (take - 1 - k)
			const span = doc.createElement("span")
			span.className = "rv"
			span.setAttribute("style", `--d:${distance}`)
			span.textContent = ch
			frag.appendChild(span)
		}
		node.parentNode?.replaceChild(frag, node)
		consumed += take
	}

	return doc.body.innerHTML
}
