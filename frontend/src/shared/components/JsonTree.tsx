import { useMemo, useState } from "react"
import { ChevronRight, Copy } from "lucide-react"
import { cn } from "../lib/utils"

/**
 * Вывод ответа инструмента по-человечески.
 *
 * Раньше здесь была сырая структура вида «▶headerLabel:[] 1 items».
 * Теперь:
 *  — если ответ — это текст (или MCP-контент с текстом), показываем текст;
 *  — простые поля рисуются табличкой «ключ — значение»;
 *  — вложенные объекты складываются аккуратными секциями с понятными подписями;
 *  — всё можно скопировать или посмотреть сырым JSON.
 */

type Json = unknown

function isRecord(v: Json): v is Record<string, Json> {
	return typeof v === "object" && v !== null && !Array.isArray(v)
}

/** Вытаскивает текст из типового MCP-ответа { content: [{type:"text", text}] }. */
function mcpText(data: Json): string | null {
	if (typeof data === "string") return data
	if (!isRecord(data)) return null
	const content = data.content
	if (!Array.isArray(content)) return null
	const chunks = content
		.map((item) => (isRecord(item) && typeof item.text === "string" ? item.text : ""))
		.filter(Boolean)
	return chunks.length > 0 ? chunks.join("\n\n") : null
}

function humanKey(key: string) {
	const spaced = key
		.replace(/[_-]+/g, " ")
		.replace(/([a-z\d])([A-Z])/g, "$1 $2")
		.trim()
	return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

function Scalar({ value }: { value: Json }) {
	if (value === null || value === undefined)
		return <span className="text-muted-foreground">—</span>
	if (typeof value === "boolean")
		return (
			<span className={value ? "text-emerald-500" : "text-muted-foreground"}>
				{value ? "да" : "нет"}
			</span>
		)
	if (typeof value === "number") return <span className="text-brand tabular-nums">{value}</span>
	const text = String(value)
	const isUrl = /^https?:\/\//.test(text)
	return (
		<span className={cn("break-words whitespace-pre-wrap", isUrl && "text-brand underline")}>
			{text}
		</span>
	)
}

function Section({ label, value, depth }: { label: string; value: Json; depth: number }) {
	const [open, setOpen] = useState(depth < 1)
	const isArray = Array.isArray(value)
	const entries = isArray
		? (value as Json[]).map((v, i) => [String(i + 1), v] as const)
		: Object.entries(value as Record<string, Json>)
	const summary = isArray
		? `${entries.length} элем.`
		: `${entries.length} пол${entries.length === 1 ? "е" : entries.length < 5 ? "я" : "ей"}`

	if (entries.length === 0) {
		return (
			<Row label={label}>
				<span className="text-muted-foreground">пусто</span>
			</Row>
		)
	}

	return (
		<div className="min-w-0">
			<button
				type="button"
				onClick={() => setOpen((v) => !v)}
				aria-expanded={open}
				className="hover:bg-accent flex w-full items-center gap-1.5 rounded-md px-1 py-1 text-left text-[13px] transition-colors"
			>
				<ChevronRight
					className={cn(
						"text-muted-foreground size-3.5 shrink-0 transition-transform",
						open && "rotate-90",
					)}
				/>
				<span className="min-w-0 truncate font-medium">{humanKey(label)}</span>
				<span className="text-muted-foreground shrink-0 text-[12px]">{summary}</span>
			</button>
			{open ? (
				<div className="border-border ms-[7px] border-s ps-3">
					<Body data={value} depth={depth + 1} />
				</div>
			) : null}
		</div>
	)
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<div className="grid grid-cols-[minmax(80px,auto)_1fr] items-baseline gap-x-3 gap-y-1 px-1 py-1">
			<div className="text-muted-foreground truncate text-[12px]">{humanKey(label)}</div>
			<div className="min-w-0 text-[13px]">{children}</div>
		</div>
	)
}

function Body({ data, depth }: { data: Json; depth: number }) {
	const text = mcpText(data)
	if (text !== null) {
		return (
			<pre className="max-h-[320px] overflow-auto px-1 py-1 font-mono text-[12.5px] leading-5 break-words whitespace-pre-wrap">
				{text}
			</pre>
		)
	}
	if (Array.isArray(data)) {
		return (
			<div className="flex min-w-0 flex-col">
				{data.map((item, i) =>
					typeof item === "object" && item !== null ? (
						<Section key={i} label={`#${i + 1}`} value={item} depth={depth} />
					) : (
						<Row key={i} label={`#${i + 1}`}>
							<Scalar value={item} />
						</Row>
					),
				)}
			</div>
		)
	}
	if (isRecord(data)) {
		const entries = Object.entries(data)
		return (
			<div className="flex min-w-0 flex-col">
				{entries.map(([key, value]) =>
					typeof value === "object" && value !== null ? (
						<Section key={key} label={key} value={value} depth={depth} />
					) : (
						<Row key={key} label={key}>
							<Scalar value={value} />
						</Row>
					),
				)}
			</div>
		)
	}
	return (
		<div className="px-1 py-1 text-[13px]">
			<Scalar value={data} />
		</div>
	)
}

export default function JsonTree({ data }: { data: unknown }) {
	const [raw, setRaw] = useState(false)
	const pretty = useMemo(() => {
		try {
			return JSON.stringify(data, null, 2)
		} catch {
			return String(data)
		}
	}, [data])

	if (data === undefined) return null

	return (
		<div className="min-w-0">
			<div className="mb-1 flex items-center justify-end gap-1">
				<button
					type="button"
					onClick={() => setRaw((v) => !v)}
					className="text-muted-foreground hover:bg-accent hover:text-foreground rounded-md px-1.5 py-0.5 text-[11px] transition-colors"
				>
					{raw ? "Понятный вид" : "Сырой JSON"}
				</button>
				<button
					type="button"
					aria-label="Копировать ответ"
					onClick={() => void navigator.clipboard.writeText(pretty)}
					className="text-muted-foreground hover:bg-accent hover:text-foreground grid size-6 place-items-center rounded-md transition-colors"
				>
					<Copy className="size-3" />
				</button>
			</div>

			{raw ? (
				<pre className="bg-muted/50 max-h-[320px] overflow-auto rounded-md p-2 font-mono text-[12px] leading-5 break-words whitespace-pre-wrap">
					{pretty}
				</pre>
			) : (
				<Body data={data} depth={0} />
			)}
		</div>
	)
}
