import { useEffect, useRef, useState } from "react"
import { Check } from "lucide-react"
import type { Survey } from "../../artifacts/model/artifacts"
import { cn } from "../../../shared/lib/utils"
import { Button } from "../../../shared/ui"

/**
 * Опросник в стиле Notion: чекбоксы с горячими клавишами 1–6, свободное поле
 * «Или опишите свой вариант…», кнопки Пропустить / Отправить и появление
 * вариантов лестницей (500 ms с шагом 100 ms).
 */
export default function SurveyCard({
	survey,
	disabled,
	onSubmit,
}: {
	survey: Survey
	disabled?: boolean
	onSubmit: (answer: string, content?: Record<string, unknown>) => void
}) {
	const [picked, setPicked] = useState<string[]>([])
	const [other, setOther] = useState("")
	const [sent, setSent] = useState(false)
	const boxRef = useRef<HTMLDivElement>(null)

	function toggle(id: string) {
		setPicked((prev) =>
			survey.allowMultiple
				? prev.includes(id)
					? prev.filter((x) => x !== id)
					: [...prev, id]
				: prev.includes(id)
					? []
					: [id],
		)
	}

	// Горячие клавиши 1–6, пока фокус внутри карточки.
	useEffect(() => {
		const el = boxRef.current
		if (!el || sent || disabled) return
		function onKey(e: KeyboardEvent) {
			if (e.target instanceof HTMLTextAreaElement) return
			const n = Number(e.key)
			if (!n || n < 1 || n > survey.options.length) return
			e.preventDefault()
			toggle(survey.options[n - 1].id)
		}
		el.addEventListener("keydown", onKey)
		return () => el.removeEventListener("keydown", onKey)
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [survey, sent, disabled])

	// content — тот же формат, что шлёт веб-клиент Notion в user.input_response:
	// {"<questionId>": "<optionId>" | ["<optionId>"], "other:<questionId>": "текст"}.
	function answerContent(ids: string[], free: string): Record<string, unknown> {
		const content: Record<string, unknown> = {}
		const values = free ? [...ids, "__other__"] : ids
		if (values.length > 0) {
			content[survey.id] = survey.allowMultiple ? values : values[values.length - 1]
		}
		if (free) content[`other:${survey.id}`] = free
		return content
	}

	function send() {
		const labels = survey.options.filter((o) => picked.includes(o.id)).map((o) => o.label)
		const free = other.trim()
		if (free) labels.push(free)
		if (labels.length === 0) return
		setSent(true)
		onSubmit(labels.join(", "), answerContent(picked, free))
	}

	const canSend = picked.length > 0 || other.trim().length > 0

	return (
		<div
			ref={boxRef}
			tabIndex={-1}
			className="bg-card flex min-w-0 flex-col gap-3 rounded-2xl border px-4 pt-3 pb-4 shadow-xs outline-none"
		>
			<div className="text-base font-semibold">{survey.question}</div>

			<div className="flex max-h-80 flex-col overflow-y-auto" role="listbox">
				{survey.options.map((o, i) => {
					const on = picked.includes(o.id)
					return (
						<div
							key={o.id}
							style={{ animationDelay: `${(i + 1) * 100}ms` }}
							className="survey-option-in"
						>
							<div
								role="option"
								aria-selected={on}
								aria-keyshortcuts={String(i + 1)}
								onClick={() => !sent && !disabled && toggle(o.id)}
								className={cn(
									"flex cursor-pointer items-start gap-2.5 rounded-lg px-1.5 py-2 transition-colors",
									on ? "bg-accent" : "hover:bg-accent/60",
									(sent || disabled) && "pointer-events-none opacity-70",
								)}
							>
								<span
									className={cn(
										"mt-1 grid size-4 shrink-0 place-items-center rounded-[3px] border transition-colors",
										on && "bg-brand border-brand text-white",
									)}
								>
									{on ? <Check className="size-3" /> : null}
								</span>
								<span className="min-w-0 flex-1 text-base leading-6">{o.label}</span>
								<span className="text-muted-foreground bg-card grid size-5 shrink-0 place-items-center rounded border text-[11px] font-medium">
									{i + 1}
								</span>
							</div>
						</div>
					)
				})}

				{survey.allowOther ? (
					<div
						style={{ animationDelay: `${(survey.options.length + 1) * 100}ms` }}
						className="survey-option-in"
					>
						<div className="flex items-start gap-2.5 rounded-lg px-1.5 py-2">
							<span
								className={cn(
									"mt-1 grid size-4 shrink-0 place-items-center rounded-[3px] border",
									other.trim() && "bg-brand border-brand text-white",
								)}
							>
								{other.trim() ? <Check className="size-3" /> : null}
							</span>
							<textarea
								value={other}
								disabled={sent || disabled}
								onChange={(e) => setOther(e.target.value)}
								onKeyDown={(e) => {
									if (e.key === "Enter" && !e.shiftKey) {
										e.preventDefault()
										send()
									}
								}}
								rows={1}
								placeholder="Или опишите свой вариант…"
								className="placeholder:text-muted-foreground min-h-6 w-full flex-1 resize-none bg-transparent text-base leading-6 outline-none"
							/>
							<span className="text-muted-foreground bg-card grid size-5 shrink-0 place-items-center rounded border text-[11px] font-medium">
								{survey.options.length + 1}
							</span>
						</div>
					</div>
				) : null}
			</div>

			<div className="flex items-center justify-end gap-1.5">
				<Button
					variant="subtle"
					size="sm"
					disabled={sent || disabled}
					onClick={() => {
						setSent(true)
						onSubmit("Пропустить", {})
					}}
				>
					Пропустить
				</Button>
				<Button
					variant="brand"
					size="sm"
					disabled={!canSend || sent || disabled}
					onClick={send}
				>
					Отправить
				</Button>
			</div>
		</div>
	)
}
