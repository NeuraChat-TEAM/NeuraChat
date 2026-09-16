import { useMemo, useState } from "react"
import {
	ChevronRight,
	Copy,
	FileArchive,
	FileCode2,
	FileText,
	PencilLine,
	Plus,
	ThumbsDown,
	ThumbsUp,
} from "lucide-react"
import { ASSISTANT_FACE } from "./TitleBar"
import type { Part, ToolPart, Turn } from "../lib/types"
import type { Artifact, Survey } from "../lib/artifacts"
import { artifactFile, splitArtifacts } from "../lib/artifacts"
import type { ComputerFile } from "../lib/toolOutput"
import { canPreview, fileSize, isImage, parseToolResult } from "../lib/toolOutput"
import { cn, formatTime } from "../lib/utils"
import { ArtifactRow } from "./ArtifactCard"
import SurveyCard from "./SurveyCard"
import JsonTree from "./JsonTree"
import Markdown from "./Markdown"
import { Badge, Button, ScrollArea, Tabs, TabsContent, TabsList, TabsTrigger, Tooltip } from "./ui"

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
	const [open, setOpen] = useState(false)
	const args = part.args ?? {}
	const hasArgs = Object.keys(args).length > 0
	// Результат разбираем: подписи шага, файлы, текстовый вывод.
	const parsed = useMemo(() => parseToolResult(part.result), [part.result])

	return (
		<div className="min-w-0">
			<button
				onClick={() => setOpen(!open)}
				aria-expanded={open}
				className="hover:bg-accent flex max-w-full cursor-pointer items-center gap-1.5 rounded-md px-1 py-0.5 text-sm"
			>
				<span className="bg-accent grid size-4 place-items-center rounded-[0.25em] text-[10px] lowercase">
					{(part.server || "m").charAt(0)}
				</span>
				<span className="text-muted-foreground">{part.server || "mcp"}</span>
				<span className="text-muted-foreground/60">/</span>
				<span className={cn("truncate font-medium", !part.done && "shimmer")}>{part.name}</span>
				{!part.done ? (
					<span className="tool-status" title="подключается…" aria-label="подключается">
						<span className="tool-status__ring" />
						<span className="tool-status__dot" />
					</span>
				) : null}
				<ChevronRight
					className={cn("text-muted-foreground size-3.5 transition-transform", open && "rotate-90")}
				/>
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
			) : open && parsed.labels.length > 0 ? (
				<div className="tool-live mt-0.5 flex flex-col gap-0.5 ps-1">
					{parsed.labels.map((label, i) => (
						<span
							key={`${label}-${i}`}
							className="text-muted-foreground truncate text-[13px]"
						>
							{label}
						</span>
					))}
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
				<div className="bg-card mt-2 rounded-[10px] border p-2 shadow-xs">
					<Tabs defaultValue={hasArgs ? "input" : "response"}>
						<TabsList>
							{hasArgs ? <TabsTrigger value="input">Input</TabsTrigger> : null}
							<TabsTrigger value="response">Response</TabsTrigger>
						</TabsList>

						{hasArgs ? (
							<TabsContent value="input" className="pt-3">
								<div className="grid grid-cols-[auto_1fr] items-baseline gap-x-4 gap-y-2">
									{Object.entries(args).map(([k, v]) => (
										<div key={k} className="contents">
											<div className="text-muted-foreground text-end text-[13px]">{k}</div>
											<div className="font-mono text-[13px] break-all whitespace-pre-wrap">
												{typeof v === "string" ? v : JSON.stringify(v)}
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
									{parsed.text ? (
										<pre className="bg-accent/40 min-w-0 overflow-x-auto rounded-md p-2 font-mono text-[12px] leading-[1.45] whitespace-pre-wrap">
											{parsed.text}
										</pre>
									) : null}
									{parsed.files.map((file) => (
										<FileChip key={`r-${file.id}`} file={file} onOpen={onOpenFile} />
									))}
									{parsed.rest ? <JsonTree data={parsed.rest} /> : null}
									{!parsed.text && !parsed.rest && parsed.files.length === 0 ? (
										<JsonTree data={part.result} />
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

function Thought({ text }: { text: string }) {
	const [open, setOpen] = useState(false)
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
				<ScrollArea className="mt-1 max-h-44">
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
	const [open, setOpen] = useState(true)

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
						<div className="mt-2.5 flex flex-col gap-3 ps-2.5">
							{steps.map((p, i) => (
								<div key={i} className="relative min-w-0 ps-4">
									<span className="bg-muted-foreground/40 absolute start-0 top-2 size-[5px] rounded-full" />
									{i < steps.length - 1 ? (
										<span className="bg-border absolute start-[2px] top-[17px] h-[calc(100%-2px)] w-px" />
									) : null}
									{p.kind === "thought" ? (
										<Thought text={p.text} />
									) : (
										<ToolCard part={p} onOpenFile={onOpenFile} activeFile={activeFile} />
									)}
								</div>
							))}
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
				<div className={cn("answer-reveal", turn.streaming && "answer-reveal--live")}>
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
		</div>
	)
}
