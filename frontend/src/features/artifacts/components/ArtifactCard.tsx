import { ChevronRight, Code2, Database, FileText, Globe } from "lucide-react"
import type { Artifact } from "../model/artifacts"
import { artifactFile } from "../model/artifacts"
import { cn } from "../../../shared/lib/utils"
import { Tooltip } from "../../../shared/ui"

const ICONS = {
	web: Globe,
	code: Code2,
	data: Database,
	doc: FileText,
} as const

/**
 * Карточка артефакта — геометрия как у Notion: радиус 12, высота 48,
 * квадрат иконки 32×32, имя + расширение отдельными спанами (обрезается
 * только имя), шеврон справа и действие, появляющееся при наведении.
 */
export default function ArtifactCard({
	artifact,
	active,
	onOpen,
	onSave,
}: {
	artifact: Artifact
	active?: boolean
	onOpen: (a: Artifact) => void
	onSave?: (a: Artifact) => void
}) {
	const Icon = ICONS[artifact.kind]
	const lines = artifact.code.split("\n").length

	return (
		<div className="artifact-in flex min-w-0 items-center">
			<div
				role="button"
				tabIndex={0}
				onClick={() => onOpen(artifact)}
				onKeyDown={(e) => {
					if (e.key === "Enter" || e.key === " ") {
						e.preventDefault()
						onOpen(artifact)
					}
				}}
				className={cn(
					"group bg-muted hover:bg-accent flex w-[min(320px,100%)] min-w-0 cursor-pointer items-center",
					"rounded-xl border px-3 py-1.5 transition-colors",
					active && "border-brand/60 bg-accent",
				)}
			>
				<div className="me-2 grid size-8 shrink-0 place-items-center rounded-md bg-[color-mix(in_srgb,var(--blue-accent)_18%,transparent)]">
					<Icon className="text-brand size-[18px]" />
				</div>

				<div className="flex min-w-0 flex-1 flex-col justify-center py-1">
					<div className="flex min-w-0 text-[12px] font-medium">
						<span className="truncate">{artifact.name}</span>
						<span className="shrink-0 whitespace-nowrap">{artifact.ext}</span>
					</div>
					<div className="text-muted-foreground text-[11px]">
						{artifact.kind === "web" ? "открыть в браузере" : "открыть"} · {lines} стр.
					</div>
				</div>

				{onSave ? (
					<Tooltip label="Скачать файл">
						<button
							type="button"
							aria-label="Скачать файл"
							onClick={(e) => {
								e.stopPropagation()
								onSave(artifact)
							}}
							className="bg-card text-muted-foreground hover:text-foreground me-1 hidden size-7 shrink-0 place-items-center rounded-md border group-hover:grid"
						>
							<FileText className="size-3.5" />
						</button>
					</Tooltip>
				) : null}

				<ChevronRight className="text-muted-foreground ms-1 size-4 shrink-0" />
			</div>
		</div>
	)
}

/** Горизонтальная лента карточек, как в Notion под шагами ответа. */
export function ArtifactRow({
	artifacts,
	activeId,
	onOpen,
	onSave,
}: {
	artifacts: Artifact[]
	activeId?: string
	onOpen: (a: Artifact) => void
	onSave?: (a: Artifact) => void
}) {
	if (artifacts.length === 0) return null
	return (
		<div className="flex min-w-0 flex-wrap gap-2">
			{artifacts.map((a) => (
				<ArtifactCard
					key={a.id + artifactFile(a)}
					artifact={a}
					active={activeId === a.id}
					onOpen={onOpen}
					onSave={onSave}
				/>
			))}
		</div>
	)
}
