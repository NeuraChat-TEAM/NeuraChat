import { useMemo } from "react"
import { renderNotionMarkdown } from "../lib/notionMarkdown"

// Стрим-безопасный рендер: парсится на каждой дельте, санитаизится перед
// вставкой. Понимает весь Notion-диалект: цвета, каллауты, тогглы,
// колонки, табы, упоминания, формулы и вложения.
export default function Markdown({ text }: { text: string }) {
	const html = useMemo(() => (text ? renderNotionMarkdown(text) : ""), [text])

	return (
		<div className="answer prose-notion" dangerouslySetInnerHTML={{ __html: html }} />
	)
}
