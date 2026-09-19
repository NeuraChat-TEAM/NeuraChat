import { useEffect, useMemo, useRef, useState } from "react"
import { ArrowLeft, ArrowRight, Check } from "lucide-react"
import type { Survey } from "../../artifacts/model/artifacts"
import { cn } from "../../../shared/lib/utils"
import { Button } from "../../../shared/ui"

/** Ответ на один вопрос: выбранные id + свободный текст. */
type Answer = { picked: string[]; other: string }

export type SurveyResult = {
	/** Текст, который уедет в чат вместо ручного сообщения. */
	text: string
	/** content для user.input_response — формат веб-клиента Notion. */
	content: Record<string, unknown>
}

/** Человеческая подпись ответа: «Лендинг продукта, свой вариант». */
function answerLabel(survey: Survey, answer: Answer): string {
	const labels = survey.options
		.filter((o) => answer.picked.includes(o.id))
		.map((o) => o.label)
	const free = answer.other.trim()
	if (free) labels.push(free)
	return labels.join(", ")
}

/**
 * Опросник прямо в инпуте: один вопрос на экране, «Назад»/«Далее» и
 * «Отправить» на последнем шаге. После отправки в чат уходит сводка
 * «Ответил на вопросы: 1. … 2. …», а агенту — структурный input_response.
 */
export default function SurveyComposer({
	surveys,
	busy,
	onSubmit,
	onSkip,
}: {
	surveys: Survey[]
	busy?: boolean
	onSubmit: (result: SurveyResult) => void
	onSkip: () => void
}) {
	const [step, setStep] = useState(0)
	const [answers, setAnswers] = useState<Record<string, Answer>>({})
	const [sent, setSent] = useState(false)
	const boxRef = useRef<HTMLDivElement>(null)

	// Новый опросник — начинаем сначала.
	const key = useMemo(() => surveys.map((s) => s.id).join("|"), [surveys])
	useEffect(() => {
		setStep(0)
		setAnswers({})
		setSent(false)
	}, [key])

	const survey = surveys[Math.min(step, surveys.length - 1)]
	if (!survey) return null

	const answer = answers[survey.id] ?? { picked: [], other: "" }
	const last = step >= surveys.length - 1
	const filled = answer.picked.length > 0 || answer.other.trim().length > 0

	function patch(next: Partial<Answer>) {
		setAnswers((prev) => ({
			...prev,
			[survey.id]: { ...answer, ...next },
		}))
	}

	function toggle(id: string) {
		if (sent || busy) return
		const has = answer.picked.includes(id)
		patch({
			picked: survey.allowMultiple
				? has
					? answer.picked.filter((x) => x !== id)
					: [...answer.picked, id]
				: has
					? []
					: [id],
		})
	}

	function next() {
		if (!last) {
			setStep((s) => Math.min(s + 1, surveys.length - 1))
			return
		}
		submit()
	}

	function submit() {
		if (sent) return
		const content: Record<string, unknown> = {}
		const lines: string[] = []
		surveys.forEach((s, i) => {
			const a = answers[s.id] ?? { picked: [], other: "" }
			const free = a.other.trim()
			const ids = free ? [...a.picked, "__other__"] : a.picked
			if (ids.length > 0) {
				content[s.id] = s.allowMultiple ? ids : ids[ids.length - 1]
			}
			if (free) content[`other:${s.id}`] = free
			const label = answerLabel(s, a)
			lines.push(`${i + 1}. ${label || "— пропущено"}`)
		})
		if (lines.length === 0) return
		setSent(true)
		onSubmit({ text: `Ответил на вопросы:\n${lines.join("\n")}`, content })
	}

	// Цифры 1–6 выбирают вариант, пока фокус внутри карточки.
	useEffect(() => {
		const el = boxRef.current
		if (!el || sent || busy) return
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
	}, [survey, sent, busy, answer])

	return (
		<div
			ref={boxRef}
			tabIndex={-1}
			className="survey-inline mx-1 mt-1.5 mb-1 flex min-w-0 flex-col gap-2 rounded-xl border bg-[color-mix(in_srgb,var(--blue-accent)_6%,transparent)] px-3 pt-2.5 pb-2.5 outline-none"
		>
			<div className="flex items-start gap-2">
				<div className="min-w-0 flex-1 text-[14px] leading-5 font-semibold">
					{survey.question}
				</div>
				{surveys.length > 1 ? (
					<span className="text-muted-foreground shrink-0 text-[12px]">
						{step + 1} / {surveys.length}
					</span>
				) : null}
			</div>

			{/* key по шагу — варианты каждый раз появляются лесенкой заново. */}
			<div key={survey.id} className="flex max-h-56 flex-col overflow-y-auto" role="listbox">
				{survey.options.map((o, i) => {
					const on = answer.picked.includes(o.id)
					return (
						<div
							key={o.id}
							style={{ animationDelay: `${(i + 1) * 60}ms` }}
							className="survey-option-in"
						>
							<div
								role="option"
								aria-selected={on}
								onClick={() => toggle(o.id)}
								className={cn(
									"flex cursor-pointer items-start gap-2 rounded-lg px-1.5 py-1.5 transition-colors",
									on ? "bg-accent" : "hover:bg-accent/60",
									(sent || busy) && "pointer-events-none opacity-70",
								)}
							>
								<span
									className={cn(
										"mt-0.5 grid size-4 shrink-0 place-items-center rounded-[3px] border transition-colors",
										on && "bg-brand border-brand text-white",
									)}
								>
									{on ? <Check className="size-3" /> : null}
								</span>
								<span className="min-w-0 flex-1 text-[14px] leading-5">{o.label}</span>
								<span className="text-muted-foreground bg-card grid size-5 shrink-0 place-items-center rounded border text-[11px] font-medium">
									{i + 1}
								</span>
							</div>
						</div>
					)
				})}

				{survey.allowOther ? (
					<div
						style={{ animationDelay: `${(survey.options.length + 1) * 60}ms` }}
						className="survey-option-in"
					>
						<div className="flex items-start gap-2 rounded-lg px-1.5 py-1.5">
							<span
								className={cn(
									"mt-0.5 grid size-4 shrink-0 place-items-center rounded-[3px] border",
									answer.other.trim() && "bg-brand border-brand text-white",
								)}
							>
								{answer.other.trim() ? <Check className="size-3" /> : null}
							</span>
							<textarea
								value={answer.other}
								disabled={sent || busy}
								onChange={(e) => patch({ other: e.target.value })}
								onKeyDown={(e) => {
									if (e.key === "Enter" && !e.shiftKey) {
										e.preventDefault()
										next()
									}
								}}
								rows={1}
								placeholder="Или опишите свой вариант…"
								className="placeholder:text-muted-foreground min-h-5 w-full flex-1 resize-none bg-transparent text-[14px] leading-5 outline-none"
							/>
						</div>
					</div>
				) : null}
			</div>

			<div className="flex items-center gap-1.5">
				<Button
					variant="subtle"
					size="sm"
					disabled={step === 0 || sent || busy}
					onClick={() => setStep((s) => Math.max(0, s - 1))}
				>
					<ArrowLeft className="size-3.5" />
					Назад
				</Button>
				<Button variant="subtle" size="sm" disabled={sent || busy} onClick={onSkip}>
					Пропустить
				</Button>
				<div className="flex-1" />
				<Button
					variant="brand"
					size="sm"
					disabled={sent || busy || (!filled && !last)}
					onClick={next}
				>
					{last ? "Отправить" : "Далее"}
					{last ? null : <ArrowRight className="size-3.5" />}
				</Button>
			</div>
		</div>
	)
}
