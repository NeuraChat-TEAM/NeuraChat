import { Maximize2, Menu, Minus, X } from "lucide-react"
import { api } from "../lib/api"
import { cn } from "../lib/utils"
import { Button } from "./ui"

/** Официальное лого Notion AI вместо самодельного кружка со звёздочкой. */
export const ASSISTANT_FACE =
	"https://img.notionusercontent.com/ext/https%3A%2F%2Fapp.notion.com%2Fimages%2Fassistant_static_face_favicon.png/size/w=200?mtd=com"

/**
 * Оконный хром. Левый блок с брендом живёт ровно над сайдбаром и сжимается
 * с тем же transition, что и сам сайдбар — иначе при сворачивании видно расхождение
 * границы окна и границы сайдбара.
 */
export default function TitleBar({
	title,
	collapsed,
	sidebarWidth = 276,
	animate = true,
	onToggleSidebar,
}: {
	title: string
	collapsed?: boolean
	/** Ширина бренд-блока равна ширине сайдбара, иначе видно расхождение. */
	sidebarWidth?: number
	/** Пока сайдбар тянут мышью, анимацию ширины выключаем — иначе шапка отстаёт. */
	animate?: boolean
	onToggleSidebar: () => void
	onNewChat?: () => void
}) {
	const drag = { "--wails-draggable": "drag" } as React.CSSProperties

	return (
		<header className="bg-background flex h-[38px] shrink-0 items-center overflow-hidden">
			{/* Ровно ширина сайдбара: раньше блок был на 4px шире (под ручку
			    ресайза) и в шапке виднелась лишняя полоска. */}
			<div
				style={{ ...drag, width: collapsed ? 0 : sidebarWidth }}
				className={cn(
					"bg-sidebar flex h-full shrink-0 items-center gap-2 overflow-hidden",
					animate &&
						"transition-[width,padding] duration-200 ease-out motion-reduce:transition-none",
					collapsed ? "px-0" : "px-3",
				)}
			>
				<img
					src={ASSISTANT_FACE}
					alt=""
					draggable={false}
					className="size-[18px] shrink-0 rounded-full"
				/>
				<span className="text-foreground truncate text-[13px]">Neura</span>
			</div>

			<div className="flex h-full min-w-0 flex-1 items-center gap-2 px-2" style={drag}>
				<Button
					variant="subtle"
					size="icon-sm"
					aria-label="Боковая панель"
					title="Боковая панель"
					onClick={onToggleSidebar}
				>
					<Menu className="size-[17px]" />
				</Button>
				<span className="text-foreground min-w-0 truncate text-[13px]">{title}</span>
				<div className="h-full flex-1" style={drag} />
			</div>

			<div className="flex h-full shrink-0 items-center">
				<button
					type="button"
					aria-label="Свернуть"
					className="text-muted-foreground hover:bg-accent hover:text-foreground grid h-[38px] w-[46px] place-items-center transition-colors"
					onClick={() => void api.windowMinimise()}
				>
					<Minus className="size-4" />
				</button>
				<button
					type="button"
					aria-label="Развернуть"
					className="text-muted-foreground hover:bg-accent hover:text-foreground grid h-[38px] w-[46px] place-items-center transition-colors"
					onClick={() => void api.windowToggleMaximise()}
				>
					<Maximize2 className="size-[14px]" />
				</button>
				<button
					type="button"
					aria-label="Закрыть"
					className="text-muted-foreground hover:bg-destructive hover:text-destructive-foreground grid h-[38px] w-[46px] place-items-center transition-colors"
					onClick={() => void api.windowClose()}
				>
					<X className="size-4" />
				</button>
			</div>
		</header>
	)
}
