import { useMemo, useState } from "react"

// Состояние раскрытия живёт вне карточек: потоковые обновления не должны
// сворачивать уже открытый пользователем tool/thought или всю цепочку.
const expandedTools = new Set<string>()
const expandedThoughts = new Set<string>()
const expandedGroups = new Set<string>()
const animatedSteps = new Set<string>()
import {
	CheckCircle2,
	ChevronRight,
	Circle,
	Copy,
	FileArchive,
	FileCode2,
	FileText,
	Loader2,
	PencilLine,
	Plus,
	ThumbsDown,
	ThumbsUp,
	XCircle,
} from "lucide-react"
import { ASSISTANT_FACE } from "../../navigation/components/TitleBar"
import type { Part, ToolPart, Turn } from "../../../shared/model/types"
import type { Artifact, Survey } from "../../artifacts/model/artifacts"
import { artifactFile, splitArtifacts } from "../../artifacts/model/artifacts"
import type { ComputerFile } from "../../../shared/lib/toolOutput"
import { canPreview, fileSize, isImage, parseToolResult, summarizeToolArgs, toolIdentity } from "../../../shared/lib/toolOutput"
import { cn, formatTime } from "../../../shared/lib/utils"
import { ArtifactRow } from "../../artifacts/components/ArtifactCard"
import SurveyCard from "./SurveyCard"
import JsonTree from "../../../shared/components/JsonTree"
import Markdown from "../../../shared/components/Markdown"
import { Badge, Button, ScrollArea, Tabs, TabsContent, TabsList, TabsTrigger, Tooltip } from "../../../shared/ui"

const copy = (text: string) => void navigator.clipboard.writeText(text)

/** Карточка файла как в Notion: 32px иконка, имя, расширение, размер. */
export function FileChip({
	file,
	active,
	onOpen,
}: {
	file: ComputerFile
	active?: boolean
	onOpen?: (file: ComputerFile) => void
}) {
	const dot = file.fileName.lastIndexOf(".")
	const base = dot > 0 ? file.fileName.slice(0, dot) : file.fileName
	const ext = dot > 0 ? file.fileName.slice(dot) : ""
	const Icon = /\.(zip|tar|gz|7z|rar)$/i.test(file.fileName)
		? FileArchive
		: canPreview(file)
			? FileCode2
			: FileText
	const clickable = !!onOpen
	return (
		<button
			type="button"
			disabled={!clickable}
			onClick={() => onOpen?.(file)}
			title={clickable ? `Открыть ${file.fileName}` : file.fileName}
			className={cn(
				"artifact-in bg-accent/60 hover:bg-accent flex w-[min(300px,100%)] min-w-0 items-center gap-2 rounded-xl border px-3 py-1.5 text-left transition-colors",
				active && "ring-brand/60 ring-1",
				clickable ? "cursor-pointer" : "cursor-default",
			)}
		>
			<span className="bg-card grid size-8 shrink-0 place-items-center rounded-md border">
				<Icon className="text-brand size-[18px]" />
			</span>
			<span className="flex min-w-0 flex-1 flex-col">
				<span className="flex min-w-0 text-[12px] font-medium">
					<span className="truncate">{base}</span>
					<span className="shrink-0">{ext}</span>
				</span>
				<span className="text-muted-foreground truncate text-[11px]">
					{[fileSize(file.sizeBytes), canPreview(file) ? "предпросмотр" : "файл"]
						.filter(Boolean)
						.join(" · ")}
				</span>
			</span>
		</button>
	)
}

export function UserMessage({ turn, onEdit }: { turn: Turn; onEdit: (turn: Turn) => void }) {
	return (
		<div className="group flex flex-col items-end gap-1">
			<div className="bg-accent ms-[70px] max-w-[calc(95%-40px)] rounded-2xl px-3.5 py-1.5 text-base leading-6 whitespace-pre-wrap">
				{turn.content}
			</div>
			<div className="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
				<span className="text-muted-foreground text-xs">{formatTime(turn.createdAt)}</span>
				<Tooltip label="Изменить">
					<Button variant="subtle" size="icon-sm" onClick={() => onEdit(turn)}>
						<PencilLine className="size-3.5" />
					</Button>
				</Tooltip>
				<Tooltip label="Копировать">
					<Button variant="subtle" size="icon-sm" onClick={() => copy(turn.content)}>
						<Copy className="size-3.5" />
					</Button>
				</Tooltip>
			</div>
		</div>
	)
}

function ToolCard({
	part,
	onOpenFile,
	activeFile,
}: {
	part: ToolPart
	onOpenFile?: (file: ComputerFile) => void
	activeFile?: string
}) {
	const [open, setOpenState] = useState(() => expandedTools.has(part.id))
	const setOpen = (next: boolean) => {
		setOpenState(next)
		if (next) expandedTools.add(part.id)
		else expandedTools.delete(part.id)
	}
	const identity = toolIdentity(part)
	const args = identity.args
	const argRows = summarizeToolArgs(args)
	const hasArgs = argRows.length > 0
	// Результат разбираем: подписи шага, файлы, текстовый вывод.
	const parsed = useMemo(() => parseToolResult(part.result), [part.result])

	return (
		<div className="min-w-0">
			<button
				onClick={() => part.done && setOpen(!open)}
				disabled={!part.done}
				aria-expanded={part.done ? open : undefined}
				className={cn("flex max-w-full items-center gap-1.5 rounded-md px-1 py-0.5 text-sm", part.done ? "hover:bg-accent cursor-pointer" : "cursor-default")} 
			>
				<span className="bg-accent grid size-4 place-items-center rounded-[0.25em] text-[10px] lowercase">
					{identity.mark}
				</span>
				<span className={cn("min-w-0 truncate font-medium", !part.done && "shimmer")}>
					{identity.source} / {identity.name}
				</span>
				<span className="text-muted-foreground hidden truncate text-xs sm:inline">· {identity.action}</span>
				{!part.done ? (
					<span className="tool-status" title="подключается…" aria-label="подключается">
						<span className="tool-status__ring" />
						<span className="tool-status__dot" />
					</span>
				) : null}
				{part.done ? <ChevronRight
					className={cn("text-muted-foreground size-3.5 transition-transform", open && "rotate-90")}
				/> : null}
			</button>

			{/* Пока шаг выполняется — показываем один живой статус.
			    Готовые подписи («Loaded user», «Listed directory») больше не
			    висят под свёрнутой функцией: они видны только при раскрытии. */}
			{!part.done ? (
				<div className="tool-live mt-0.5 flex flex-col gap-0.5 ps-1">
					<span className="text-muted-foreground shimmer truncate text-[13px]">
						{parsed.labels[parsed.labels.length - 1] ?? "Подключается…"}
					</span>
				</div>
			) : null}

			{open && parsed.files.length > 0 ? (
				<div className="mt-1.5 flex flex-col gap-1.5">
					{parsed.files.map((file) =>
						isImage(file) ? (
							<FileChip
								key={file.id}
								file={file}
								active={activeFile === file.fileUrl}
								onOpen={onOpenFile}
							/>
						) : (
							<FileChip
								key={file.id}
								file={file}
								active={activeFile === file.fileUrl}
								onOpen={onOpenFile}
							/>
						),
					)}
				</div>
			) : null}

			{open ? (
				<div className="tool-expand bg-card mt-2 rounded-[10px] border p-2 shadow-xs">
					<Tabs defaultValue={hasArgs ? "input" : "response"}>
						<TabsList>
							{hasArgs ? <TabsTrigger value="input">Input</TabsTrigger> : null}
							<TabsTrigger value="response">Response</TabsTrigger>
						</TabsList>

						{hasArgs ? (
							<TabsContent value="input" className="pt-3">
								<div className="grid grid-cols-[auto_1fr] items-baseline gap-x-4 gap-y-2">
									{argRows.map((row) => (
										<div key={row.key} className="contents">
											<div className="text-muted-foreground text-end text-[13px]">{row.key}</div>
											<div className="min-w-0 text-[13px]">
												{row.summary ? <div className="break-words font-medium">{row.summary}</div> : null}
												{row.values ? <div className="text-muted-foreground max-h-52 min-w-0 overflow-y-auto font-mono text-[11px] leading-5">{row.values.map((v, index) => <div key={`${index}:${v}`} className="break-all">{v}</div>)}</div> : null}
											</div>
										</div>
									))}
								</div>
							</TabsContent>
						) : null}

						<TabsContent value="response" className="pt-3">
							{!part.done ? (
								<p className="text-muted-foreground text-[13px]">Выполняется…</p>
							) : (
								// Собственный скролл: раньше длинный ответ нельзя было пролистать.
								<div className="flex max-h-[320px] min-w-0 flex-col gap-2 overflow-y-auto overscroll-contain pe-1">
									{parsed.statusCode !== undefined ? (
										<Badge tone={parsed.statusCode === 0 ? "ok" : "bad"}>
											exit {parsed.statusCode}
										</Badge>
									) : null}
									{parsed.summaries.map((summary) => <p key={summary} className="text-[13px] font-medium">{summary}</p>)}
									{parsed.text ? (
										<pre className="bg-accent/40 min-w-0 overflow-x-auto rounded-md p-2 font-mono text-[12px] leading-[1.45] whitespace-pre-wrap">
											{parsed.text}
										</pre>
									) : null}
									{parsed.files.map((file) => (
										<FileChip key={`r-${file.id}`} file={file} onOpen={onOpenFile} />
									))}
									{parsed.rest ? <JsonTree data={parsed.rest} /> : null}
									{!parsed.text && !parsed.rest && parsed.files.length === 0 && parsed.summaries.length === 0 ? (
										<p className="text-muted-foreground text-[13px]">Готово</p>
									) : null}
								</div>
							)}
						</TabsContent>
					</Tabs>
				</div>
			) : null}
		</div>
	)
}

type TodoItem = { id?: string; text: string; status?: "pending" | "in_progress" | "done" | "failed" }

function todosFromParts(parts: Part[]): TodoItem[] {
	let latest: TodoItem[] = []
	for (const part of parts) {
		if (part.kind !== "tool" || !/update[_-]?todos/i.test(toolIdentity(part).name)) continue
		for (const candidate of [part.result, part.args, parseToolResult(part.result).text]) {
			let value: unknown = candidate
			if (typeof value === "string") { try { value = JSON.parse(value) } catch { continue } }
			if (!value || typeof value !== "object" || Array.isArray(value)) continue
			const list = (value as { todos?: unknown }).todos
			if (!Array.isArray(list)) continue
			const parsed = list.flatMap((item): TodoItem[] => {
				if (!item || typeof item !== "object") return []
				const row = item as Record<string, unknown>
				if (typeof row.text !== "string" || !row.text.trim()) return []
				return [{ id: typeof row.id === "string" ? row.id : undefined, text: row.text, status: typeof row.status === "string" ? row.status as TodoItem["status"] : "pending" }]
			})
			if (parsed.length) latest = parsed
		}
	}
	return latest
}

function TodoArtifact({ todos }: { todos: TodoItem[] }) {
	const done = todos.filter(todo => todo.status === "done").length
	return <div className="bg-card mt-2 rounded-xl border p-3 shadow-xs">
		<div className="mb-2 flex items-center gap-2"><CheckCircle2 className="text-brand size-4" /><span className="text-sm font-semibold">План работы</span><Badge className="ml-auto" tone={done === todos.length ? "ok" : "muted"}>{done}/{todos.length}</Badge></div>
		<div className="space-y-1.5">{todos.map((todo, index) => {
			const Icon = todo.status === "done" ? CheckCircle2 : todo.status === "in_progress" ? Loader2 : todo.status === "failed" ? XCircle : Circle
			return <div key={todo.id || `${index}:${todo.text}`} className="flex items-start gap-2 text-[13px] leading-5"><Icon className={cn("mt-0.5 size-3.5 shrink-0", todo.status === "done" && "text-emerald-500", todo.status === "in_progress" && "text-brand animate-spin", todo.status === "failed" && "text-destructive", todo.status === "pending" && "text-muted-foreground")} /><span className={cn(todo.status === "done" && "text-muted-foreground line-through")}>{todo.text}</span></div>
		})}</div>
	</div>
}

function Thought({ id, text }: { id: string; text: string }) {
	const [open, setOpenState] = useState(() => expandedThoughts.has(id))
	const setOpen = (next: boolean) => {
		setOpenState(next)
		if (next) expandedThoughts.add(id)
		else expandedThoughts.delete(id)
	}
	return (
		<div className="min-w-0">
			<button
				onClick={() => setOpen(!open)}
				aria-expanded={open}
				className="text-muted-foreground hover:bg-accent flex items-center gap-1 rounded-md px-1 py-0.5 text-sm"
			>
				Thought
				<ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
			</button>
			{open ? (
				<ScrollArea className="tool-expand mt-1 max-h-44">
					<p className="text-muted-foreground pe-2 text-[14px] leading-5 whitespace-pre-wrap">
						{text}
					</p>
				</ScrollArea>
			) : null}
		</div>
	)
}

/** Опросники приходят шагом-инструментом ask-survey, а не текстом. */
function surveysFromTools(parts: Part[]) {
	const out: Survey[] = []
	parts.forEach((part, index) => {
		if (part.kind !== "tool") return
		if (!/survey/i.test(part.name)) return
		const args = (part.args ?? {}) as {
			questions?: Array<{
				id?: string
				prompt?: string
				question?: string
				allowMultiple?: boolean
				allowOther?: boolean
				options?: Array<{ id?: string; label?: string } | string>
			}>
		}
		const questions = Array.isArray(args.questions) ? args.questions : []
		questions.forEach((q, qi) => {
			const question = (q.prompt ?? q.question ?? "").trim()
			const options = (q.options ?? [])
				.map((o, oi) =>
					typeof o === "string"
						? { id: `o${oi + 1}`, label: o }
						: { id: o.id || `o${oi + 1}`, label: (o.label ?? "").trim() },
				)
				.filter((o) => o.label !== "")
				.slice(0, 6)
			if (!question) return
			out.push({
				id: q.id || `tool-survey-${index}-${qi}`,
				question,
				options,
				allowMultiple: q.allowMultiple === true,
				allowOther: q.allowOther !== false,
			})
		})
	})
	return out
}

export function AssistantMessage({
	turn,
	activeArtifactId,
	activeFile,
	onOpenArtifact,
	onOpenFile,
	onSurveyAnswer,
}: {
	turn: Turn
	activeArtifactId?: string
	activeFile?: string
	onOpenArtifact?: (a: Artifact) => void
	onOpenFile?: (file: ComputerFile) => void
	onSurveyAnswer?: (answer: string) => void
}) {
	const steps = turn.parts.filter((p) => p.kind !== "text") as Exclude<Part, { kind: "text" }>[]
	const raw = turn.parts
		.filter((p): p is { kind: "text"; text: string } => p.kind === "text")
		.map((p) => p.text)
		.join("")
	// Файлы из ответа превращаются в карточки-артефакты, а ```survey — в опросник.
	// Пока ответ стримится, незакрытые блоки остаются текстом.
	const { body: answer, artifacts, surveys } = useMemo(() => splitArtifacts(raw), [raw])
	// Файлы из шагов (computerFileSteps) собираем в ряд карточек над ответом.
	const stepFiles = useMemo(() => {
		const seen = new Map<string, ComputerFile>()
		for (const part of turn.parts) {
			if (part.kind !== "tool" || !part.done) continue
			for (const file of parseToolResult(part.result).files) {
				seen.set(file.fileUrl || file.fileName, file)
			}
		}
		return [...seen.values()]
	}, [turn.parts])
	const toolSurveys = useMemo(() => surveysFromTools(turn.parts), [turn.parts])
	const allSurveys = [...surveys, ...toolSurveys]
	const todos = useMemo(() => todosFromParts(turn.parts), [turn.parts])
	const [open, setOpenState] = useState(() => expandedGroups.has(turn.id))
	const setOpen = (next: boolean) => {
		setOpenState(next)
		if (next) expandedGroups.add(turn.id)
		else expandedGroups.delete(turn.id)
	}

	return (
		<div className="relative flex flex-col gap-2">
			<div className="bg-card absolute -start-10 top-0 grid size-8 place-items-center overflow-hidden rounded-full border shadow-xs">
				<img
					src={ASSISTANT_FACE}
					alt=""
					draggable={false}
					className={cn("size-8 object-cover", turn.streaming && "assistant-face--busy")}
				/>
			</div>

			{steps.length > 0 ? (
				<div>
					<button
						onClick={() => setOpen(!open)}
						aria-expanded={open}
						className="text-muted-foreground hover:bg-accent flex items-center gap-1 rounded-md px-1 py-0.5 text-[14px] leading-5"
					>
						{turn.streaming ? (
							// Как в Notion: пока шаги идут — shimmer-надпись, потом сводка.
							<span className="shimmer" role="status" aria-live="polite">
								Vibing
							</span>
						) : (
							<span>
								{steps.length} {steps.length === 1 ? "шаг" : steps.length < 5 ? "шага" : "шагов"}
							</span>
						)}
						<ChevronRight
							className={cn("text-muted-foreground size-3.5 transition-transform", open && "rotate-90")}
						/>
					</button>

					{open ? (
						// Геометрия списка шагов совпадает с Notion: отступ 10px, гап 12px,
						// точка 5px на top 8px и вертикальная линия с top 17px.
						<div className="tool-expand mt-2.5 flex flex-col gap-3 ps-2.5">
							{steps.map((p, i) => {
								const stepId = p.kind === "tool" ? p.id : `${turn.id}:thought:${i}`
								const firstPaint = !animatedSteps.has(stepId)
								animatedSteps.add(stepId)
								return <div key={stepId} className={cn("relative min-w-0 ps-4", firstPaint && "tool-chain-item--new")}>
									<span className="bg-muted-foreground/40 absolute start-0 top-2 size-[5px] rounded-full" />
									{i < steps.length - 1 ? (
										<span className="tool-chain-line bg-border absolute start-[2px] top-[17px] h-[calc(100%-2px)] w-px" />
									) : null}
									{p.kind === "thought" ? (
										<Thought id={stepId} text={p.text} />
									) : (
										<ToolCard part={p} onOpenFile={onOpenFile} activeFile={activeFile} />
									)}
								</div>
							})}
						</div>
					) : null}
				</div>
			) : null}

			{artifacts.length > 0 && onOpenArtifact ? (
				<ArtifactRow
					artifacts={artifacts}
					activeId={activeArtifactId}
					onOpen={onOpenArtifact}
					onSave={(a) => {
						const blob = new Blob([a.code], { type: "text/plain;charset=utf-8" })
						const url = URL.createObjectURL(blob)
						const link = document.createElement("a")
						link.href = url
						link.download = artifactFile(a)
						link.click()
						setTimeout(() => URL.revokeObjectURL(url), 1000)
					}}
				/>
			) : null}

			{stepFiles.length > 0 ? (
				<div className="flex min-w-0 gap-2 overflow-x-auto pb-0.5">
					{stepFiles.map((file) => (
						<FileChip
							key={`top-${file.id}`}
							file={file}
							active={activeFile === file.fileUrl}
							onOpen={onOpenFile}
						/>
					))}
				</div>
			) : null}

			{answer ? (
				// Во время стрима меняется только прозрачный хвост. Entrance-анимация
				// снята: при переразборе Markdown она заставляла весь ответ мерцать заново.
				<div className={cn(turn.streaming && "answer-reveal--live")}>
					<Markdown text={answer} />
				</div>
			) : null}

			{allSurveys.map((s) => (
				<SurveyCard
					key={s.id}
					survey={s}
					disabled={turn.streaming || !onSurveyAnswer}
					onSubmit={(answer) => onSurveyAnswer?.(answer)}
				/>
			))}

			{!turn.streaming && answer ? (
				<div className="flex items-center gap-1">
					<Tooltip label="Копировать ответ">
						<Button variant="subtle" size="icon-sm" onClick={() => copy(answer)}>
							<Copy className="size-3.5" />
						</Button>
					</Tooltip>
					<Tooltip label="Сохранить">
						<Button variant="subtle" size="icon-sm" onClick={() => copy(answer)}>
							<Plus className="size-3.5" />
						</Button>
					</Tooltip>
					<Button variant="subtle" size="icon-sm">
						<ThumbsUp className="size-3.5" />
					</Button>
					<Button variant="subtle" size="icon-sm">
						<ThumbsDown className="size-3.5" />
					</Button>
				</div>
			) : null}

			{/* Последнее состояние update-todos всегда в самом конце сообщения. */}
			{todos.length > 0 ? <TodoArtifact todos={todos} /> : null}
		</div>
	)
}
