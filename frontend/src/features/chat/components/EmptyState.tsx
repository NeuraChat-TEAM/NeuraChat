import { Compass } from "lucide-react"

const QUICK_ACTIONS: { label: string; prompt: string }[] = [
	{ label: "Разобрать задачу", prompt: "Разбери задачу по шагам и предложи план решения:\n\n" },
	{ label: "Помочь с кодом", prompt: "Помоги с кодом. Вот что нужно сделать:\n\n" },
	{ label: "Объяснить документ", prompt: "Объясни этот документ простыми словами и выдели главное:\n\n" },
]

/** Пустой чат по макету: круглая иконка, заголовок и три текстовые ссылки. */
export default function EmptyState({
	onPick,
}: {
	onPersonalize?: () => void
	onPick?: (prompt: string) => void
}) {
	return (
		<div className="mx-auto flex w-full max-w-[800px] flex-col items-center px-6 pt-[22vh] pb-[10vh]">
			<div className="grid size-[52px] place-items-center rounded-full bg-[#4a4a4a] text-[#1f1f1f]">
				<Compass className="size-6" strokeWidth={2} />
			</div>

			<h1 className="mt-6 text-[30px] leading-9 font-semibold tracking-[-0.015em]">Чем помочь?</h1>

			<div className="mt-7 flex flex-wrap items-center justify-center gap-x-7 gap-y-2">
				{QUICK_ACTIONS.map(({ label, prompt }) => (
					<button
						key={label}
						type="button"
						onClick={() => onPick?.(prompt)}
						className="text-muted-foreground hover:text-foreground text-[13px] transition-colors"
					>
						{label}
					</button>
				))}
			</div>
		</div>
	)
}
