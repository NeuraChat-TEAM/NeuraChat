import { useEffect, useRef, useState } from "react"
import { ArrowUp, ChevronDown, FileText, Paperclip, Square, X } from "lucide-react"
import type { Model, Settings } from "../../../shared/model/types"
import type { Survey } from "../../artifacts/model/artifacts"
import SurveyComposer, { type SurveyResult } from "./SurveyComposer"
import { cn } from "../../../shared/lib/utils"
import {
	Button,
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "../../../shared/ui"

export type Attachment = {
	name: string
	size: number
	contentType: string
	/** Содержимое для реальной загрузки в Notion (S3). */
	dataBase64?: string
	/** Текст для небольших текстовых файлов — его можно вшить в сообщение. */
	text?: string
	error?: string
}

const MAX_TEXT_BYTES = 400 * 1024
// Политика S3 у Notion разрешает до 20 MiB на файл.
const MAX_UPLOAD_BYTES = 20 * 1024 * 1024

/** Подписи для reasoning effort в инпуте. */
const EFFORT_LABELS: Record<string, string> = {
	minimal: "минимум",
	low: "низкий",
	medium: "средний",
	high: "высокий",
}

const DEFAULT_EFFORTS = ["minimal", "low", "medium", "high"]

const TEXTUAL = new RegExp(
	"\\.(txt|md|markdown|json|jsonc|ya?ml|toml|ini|cfg|conf|env|log|csv|tsv|xml|html?|css|scss|" +
		"js|mjs|cjs|jsx|ts|tsx|go|rs|py|rb|php|java|kt|swift|c|h|cc|cpp|hpp|cs|sh|bash|zsh|ps1|" +
		"bat|cmd|sql|graphql|gradle|dockerfile|gitignore|lock|patch|diff)$",
	"i",
)

function isTextual(file: File) {
	if (file.type.startsWith("text/")) return true
	if (/json|xml|yaml|javascript|typescript|csv|sql/i.test(file.type)) return true
	return TEXTUAL.test(file.name)
}

function prettySize(bytes: number) {
	if (bytes < 1024) return `${bytes} B`
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
	return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/** ArrayBuffer → base64 без переполнения стека на больших файлах. */
function toBase64(buffer: ArrayBuffer) {
	const bytes = new Uint8Array(buffer)
	let binary = ""
	const chunk = 0x8000
	for (let i = 0; i < bytes.length; i += chunk) {
		binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
	}
	return btoa(binary)
}

async function readAttachment(file: File): Promise<Attachment> {
	const base = {
		name: file.name,
		size: file.size,
		contentType: file.type || "application/octet-stream",
	}
	if (file.size > MAX_UPLOAD_BYTES) {
		return { ...base, error: `слишком большой (> ${prettySize(MAX_UPLOAD_BYTES)})` }
	}
	try {
		// Файл всегда грузится в Notion по-настоящему; текст дополнительно
		// вшивается в сообщение, если файл небольшой и текстовый.
		const dataBase64 = toBase64(await file.arrayBuffer())
		if (isTextual(file) && file.size <= MAX_TEXT_BYTES) {
			return { ...base, dataBase64, text: await file.text() }
		}
		return { ...base, dataBase64 }
	} catch {
		return { ...base, error: "не удалось прочитать" }
	}
}

/** Композер: скрепка (файлы), выбор модели и круглая кнопка отправки. */
export default function Composer({
	value,
	onChange,
	onSend,
	onStop,
	busy,
	settings,
	models,
	onPatchSettings,
	showScrollDown,
	onScrollDown,
	surveys = [],
	onSurveyAnswer,
	onSurveySkip,
}: {
	value: string
	onChange: (v: string) => void
	onSend: (attachments: Attachment[]) => void
	onStop: () => void
	busy: boolean
	settings: Settings
	models: Model[]
	onPatchSettings: (patch: Partial<Settings>) => void
	showScrollDown: boolean
	onScrollDown: () => void
	/** Активный опросник — мастер рисуется прямо в инпуте. */
	surveys?: Survey[]
	onSurveyAnswer?: (result: SurveyResult) => void
	onSurveySkip?: () => void
}) {
	const ref = useRef<HTMLTextAreaElement>(null)
	const fileRef = useRef<HTMLInputElement>(null)
	const [files, setFiles] = useState<Attachment[]>([])
	const [dragging, setDragging] = useState(false)

	// Авторост от 76 до 240 px.
	useEffect(() => {
		const el = ref.current
		if (!el) return
		el.style.height = "auto"
		el.style.height = `${Math.min(240, Math.max(76, el.scrollHeight))}px`
	}, [value])

	async function addFiles(list: FileList | null) {
		if (!list?.length) return
		const read = await Promise.all(Array.from(list).map(readAttachment))
		setFiles((prev) => [...prev, ...read])
	}

	function submit() {
		if (busy) return
		if (!value.trim() && files.length === 0) return
		onSend(files)
		setFiles([])
	}

	function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
		if (e.key !== "Enter") return
		const wantsSend = settings.sendWithEnter ? !e.shiftKey : e.ctrlKey || e.metaKey
		if (wantsSend) {
			e.preventDefault()
			submit()
		}
	}

	const canSend = !!value.trim() || files.length > 0

	// Набор effort зависит от выбранной модели, иначе показываем стандартный.
	const active = models.find((m) => m.id === settings.model)
	const efforts = active?.reasoningEfforts?.length ? active.reasoningEfforts : DEFAULT_EFFORTS

	return (
		<div className="relative flex shrink-0 justify-center px-4 pb-5">
			{showScrollDown ? (
				<Button
					variant="secondary"
					size="icon"
					round
					className="absolute -top-10 left-1/2 -translate-x-1/2 shadow-sheet"
					aria-label="Вниз"
					onClick={onScrollDown}
				>
					<ChevronDown />
				</Button>
			) : null}

			<div
				onDragOver={(e) => {
					e.preventDefault()
					setDragging(true)
				}}
				onDragLeave={() => setDragging(false)}
				onDrop={(e) => {
					e.preventDefault()
					setDragging(false)
					void addFiles(e.dataTransfer.files)
				}}
				className={cn(
					"bg-card w-full max-w-[740px] rounded-[14px] border px-1.5 pt-0.5 pb-1.5 transition-colors",
					dragging && "border-[var(--blue-accent)]",
				)}
			>
				<input
					ref={fileRef}
					type="file"
					multiple
					className="hidden"
					onChange={(e) => {
						void addFiles(e.target.files)
						e.target.value = ""
					}}
				/>

				{surveys.length > 0 && onSurveyAnswer ? (
					<SurveyComposer
						surveys={surveys}
						busy={busy}
						onSubmit={onSurveyAnswer}
						onSkip={() => onSurveySkip?.()}
					/>
				) : null}

				{files.length > 0 ? (
					<div className="flex flex-wrap gap-1.5 px-1.5 pt-2">
						{files.map((f, i) => (
							<span
								key={`${f.name}-${i}`}
								className="bg-accent flex max-w-[260px] items-center gap-1.5 rounded-md px-2 py-1 text-[12px]"
								title={f.error ? `${f.name} — ${f.error}` : f.name}
							>
								<FileText className="size-3.5 shrink-0" />
								<span className="truncate">{f.name}</span>
								<span
									className={cn(
										"shrink-0",
										f.error ? "text-[var(--destructive)]" : "text-muted-foreground",
									)}
								>
									{f.error ? f.error : prettySize(f.size)}
								</span>
								<button
									type="button"
									aria-label="Убрать файл"
									className="text-muted-foreground hover:text-foreground shrink-0"
									onClick={() => setFiles((prev) => prev.filter((_, j) => j !== i))}
								>
									<X className="size-3" />
								</button>
							</span>
						))}
					</div>
				) : null}

				<textarea
					ref={ref}
					value={value}
					onChange={(e) => onChange(e.target.value)}
					onKeyDown={onKeyDown}
					onPaste={(e) => {
						if (e.clipboardData.files.length > 0) {
							e.preventDefault()
							void addFiles(e.clipboardData.files)
						}
					}}
					placeholder="Сообщение Neura"
					className="placeholder:text-muted-foreground max-h-60 min-h-[76px] w-full resize-none bg-transparent px-2.5 pt-3 text-[15px] leading-6 outline-none"
				/>

				<div className="flex items-center px-0.5">
					<button
						type="button"
						aria-label="Прикрепить файлы"
						onClick={() => fileRef.current?.click()}
						className="text-muted-foreground hover:bg-accent hover:text-foreground grid size-7 place-items-center rounded-md transition-colors"
					>
						<Paperclip className="size-4" />
					</button>

					<div className="flex-1" />

					<Select
						value={settings.model || "__auto__"}
						onValueChange={(id) => {
							const picked = models.find((m) => m.id === id)
							onPatchSettings({
								model: id === "__auto__" ? "" : id,
								reasoningEffort: picked?.defaultReasoningEffort ?? settings.reasoningEffort,
							})
						}}
					>
						<SelectTrigger className="text-muted-foreground hover:text-foreground h-7 w-auto gap-1 border-0 bg-transparent px-2 text-[13px] shadow-none">
							<SelectValue placeholder="Авто" />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="__auto__">Авто</SelectItem>
							{models.map((m) => (
								<SelectItem key={m.id} value={m.id}>
									{m.label || m.id}
								</SelectItem>
							))}
						</SelectContent>
					</Select>

					{/* Глубина рассуждений (effort) — прямо в инпуте, рядом с моделью. */}
					{efforts.length > 0 ? (
						<Select
							value={settings.reasoningEffort || "__auto__"}
							onValueChange={(v) =>
								onPatchSettings({ reasoningEffort: v === "__auto__" ? "" : v })
							}
						>
							<SelectTrigger
								title="Глубина рассуждений"
								className="text-muted-foreground hover:text-foreground h-7 w-auto gap-1 border-0 bg-transparent px-2 text-[13px] shadow-none"
							>
								<SelectValue placeholder="effort" />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="__auto__">effort: авто</SelectItem>
								{efforts.map((e) => (
									<SelectItem key={e} value={e}>
										{EFFORT_LABELS[e] ?? e}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					) : null}

					{busy ? (
						<button
							type="button"
							aria-label="Остановить"
							onClick={onStop}
							className="bg-accent text-foreground hover:bg-accent/80 grid size-7 place-items-center rounded-full transition-colors"
						>
							<Square className="size-3 fill-current" />
						</button>
					) : (
						<button
							type="button"
							aria-label="Отправить"
							disabled={!canSend}
							onClick={submit}
							className={cn(
								"grid size-7 place-items-center rounded-full bg-[#5a5a5a] text-[#f5f5f5] transition-colors",
								canSend ? "hover:bg-[#6a6a6a]" : "opacity-45",
							)}
						>
							<ArrowUp className="size-4" />
						</button>
					)}
				</div>
			</div>
		</div>
	)
}
