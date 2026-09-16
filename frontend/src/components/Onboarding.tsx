import { useEffect, useMemo, useState } from "react"
import { ArrowRight, Check, HelpCircle, Loader2, Plus, Trash2 } from "lucide-react"
import { api, errText } from "../lib/api"
import type { Account, ConnectionState } from "../lib/types"
import { cn } from "../lib/utils"
import { ASSISTANT_FACE } from "./TitleBar"
import ArtifactCard from "./ArtifactCard"
import type { Artifact } from "../lib/artifacts"
import { Button, Popover, PopoverContent, PopoverTrigger, ScrollArea } from "./ui"

const GREETING = [
	"Привет! Я Notion AI",
	"Для начала вам нужно загрузить ключи авторизации",
]

const HOWTO = [
	"Откройте app.notion.com и нажмите F12 (DevTools).",
	"Перейдите на вкладку Network и введите в фильтр run.",
	"Напишите любое сообщение в чат Notion AI.",
	"Правая кнопка по запросу run → Copy → Copy as cURL (bash).",
	"Вставьте скопированное в поле ниже.",
]

/**
 * Стартовый экран: диалог, как в чате, где ассистент просит ключи
 * авторизации. Можно добавить сразу несколько аккаунтов; после этого все
 * ключи собираются в карточку-артефакт accounts.json.
 */
export default function Onboarding({
	onDone,
	onConnectionChange,
	onToast,
	onOpenArtifact,
}: {
	onDone: () => void
	onConnectionChange: (state: ConnectionState | null) => void
	onToast: (msg: string, error?: boolean) => void
	onOpenArtifact: (a: Artifact) => void
}) {
	const [step, setStep] = useState(0)
	const [curl, setCurl] = useState("")
	const [busy, setBusy] = useState(false)
	const [accounts, setAccounts] = useState<Account[]>([])

	// Сообщения появляются по очереди, как живой диалог.
	useEffect(() => {
		if (step >= GREETING.length + 1) return
		const t = window.setTimeout(() => setStep((s) => s + 1), step === 0 ? 260 : 620)
		return () => window.clearTimeout(t)
	}, [step])

	useEffect(() => {
		void refresh()
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [])

	async function refresh() {
		try {
			const ws = await api.listWorkspaces()
			setAccounts(ws?.accounts ?? [])
		} catch {
			/* ещё нет ни одного ключа — это нормально */
		}
	}

	async function addAccount() {
		const text = curl.trim()
		if (!text || busy) return
		setBusy(true)
		try {
			const state = await api.importCurl(text)
			onConnectionChange(state)
			setCurl("")
			await refresh()
			onToast("Ключ принят")
		} catch (e) {
			onToast(errText(e), true)
		} finally {
			setBusy(false)
		}
	}

	const artifact = useMemo<Artifact>(() => {
		const payload = {
			accounts: accounts.map((a) => ({
				userId: a.userId,
				name: a.name,
				email: a.email,
				spaces: (a.spaces ?? []).map((s) => ({ id: s.id, name: s.name, plan: s.planType })),
			})),
		}
		return {
			id: "onboarding-accounts",
			name: "accounts",
			ext: ".json",
			lang: "json",
			code: JSON.stringify(payload, null, 2),
			kind: "data",
		}
	}, [accounts])

	const ready = accounts.length > 0
	const shown = Math.min(step, GREETING.length)

	return (
		<ScrollArea className="min-h-0 flex-1">
			<div className="mx-auto flex w-full max-w-[680px] flex-col gap-4 px-6 pt-16 pb-12">
				{GREETING.slice(0, shown).map((line, i) => (
					<div key={i} className="answer-reveal flex items-start gap-3">
						<img
							src={ASSISTANT_FACE}
							alt=""
							draggable={false}
							className={cn(
								"bg-card size-8 shrink-0 rounded-full border object-cover shadow-xs",
								i > 0 && "invisible",
							)}
						/>
						<p className={cn("pt-1 leading-6", i === 0 ? "text-xl font-semibold" : "text-base")}>
							{line}
						</p>
					</div>
				))}

				{step > GREETING.length ? (
					<div className="answer-reveal bg-card mt-2 flex flex-col gap-3 rounded-2xl border p-4 shadow-xs">
						<div className="flex items-center gap-1.5">
							<span className="text-[13px] font-medium">Ключ авторизации (cURL)</span>
							<Popover>
								<PopoverTrigger asChild>
									<button
										type="button"
										aria-label="Как получить ключ"
										className="text-muted-foreground hover:text-foreground grid size-5 place-items-center rounded-full transition-colors"
									>
										<HelpCircle className="size-4" />
									</button>
								</PopoverTrigger>
								<PopoverContent className="w-[340px]">
									<p className="mb-2 text-[13px] font-medium">Как скопировать cURL</p>
									<ol className="text-muted-foreground list-decimal space-y-1 ps-4 text-xs leading-5">
										{HOWTO.map((s) => (
											<li key={s}>{s}</li>
										))}
									</ol>
								</PopoverContent>
							</Popover>
						</div>

						<textarea
							value={curl}
							onChange={(e) => setCurl(e.target.value)}
							spellCheck={false}
							placeholder="curl 'https://www.notion.so/api/v3/runInferenceTranscript' -H 'cookie: token_v2=…' --data-raw '…'"
							className="bg-background placeholder:text-muted-foreground min-h-[104px] w-full resize-none rounded-lg border px-3 py-2 font-mono text-[12px] leading-5 outline-none focus-visible:border-[var(--blue-accent)]"
						/>

						<div className="flex items-center justify-between gap-2">
							<span className="text-muted-foreground text-xs">
								Можно добавить несколько аккаунтов по очереди.
							</span>
							<Button
								variant="secondary"
								size="sm"
								disabled={!curl.trim() || busy}
								onClick={() => void addAccount()}
							>
								{busy ? (
									<Loader2 className="size-3.5 animate-spin" />
								) : (
									<Plus className="size-3.5" />
								)}
								Добавить аккаунт
							</Button>
						</div>

						{accounts.length > 0 ? (
							<div className="flex flex-col gap-1.5 border-t pt-3">
								{accounts.map((a) => (
									<div
										key={a.userId}
										className="artifact-in flex min-w-0 items-center gap-2 rounded-lg px-1 py-1"
									>
										<span className="bg-brand/15 text-brand grid size-7 shrink-0 place-items-center rounded-full text-[12px] font-semibold uppercase">
											{(a.name || a.email || "?").charAt(0)}
										</span>
										<div className="flex min-w-0 flex-1 flex-col">
											<span className="truncate text-[13px] font-medium">
												{a.name || a.email || a.userId}
											</span>
											<span className="text-muted-foreground truncate text-[11px]">
												{(a.spaces ?? []).length} воркспейсов
											</span>
										</div>
										<Check className="size-4 shrink-0 text-emerald-500" />
									</div>
								))}
							</div>
						) : null}
					</div>
				) : null}

				{ready ? (
					<div className="answer-reveal flex flex-col gap-3">
						<p className="text-muted-foreground text-[13px]">
							Готово — ключи собраны в артефакт:
						</p>
						<ArtifactCard artifact={artifact} onOpen={onOpenArtifact} />
						<div className="flex items-center gap-2 pt-1">
							<Button variant="brand" onClick={onDone}>
								Далее
								<ArrowRight className="size-4" />
							</Button>
							<Button
								variant="subtle"
								onClick={() => {
									void api
										.clearSession()
										.then(() => {
											onConnectionChange(null)
											setAccounts([])
										})
										.catch((e) => onToast(errText(e), true))
								}}
							>
								<Trash2 className="size-3.5" />
								Сбросить ключи
							</Button>
						</div>
					</div>
				) : null}
			</div>
		</ScrollArea>
	)
}
