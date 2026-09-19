import { useMemo } from "react"
import { renderNotionMarkdown } from "../lib/notionMarkdown"
import { markTail } from "../lib/tailReveal"

// Стрим-безопасный рендер: парсится на каждой дельте, санитаизится перед
// вставкой. Понимает весь Notion-диалект: цвета, каллауты, тогглы,
// колонки, табы, упоминания, формулы и вложения.
//
// `live` включает побуквенный хвост: последние символы оборачиваются в
// span'ы с расстоянием до конца текста, и CSS делает из этого плавное
// проявление с блюром и затемнением.
export default function Markdown({ text, live }: { text: string; live?: boolean }) {
	const html = useMemo(() => (text ? renderNotionMarkdown(text) : ""), [text])
	const out = useMemo(() => (live ? markTail(html) : html), [html, live])

	return (
		<div className="answer prose-notion" dangerouslySetInnerHTML={{ __html: out }} />
	)
}
