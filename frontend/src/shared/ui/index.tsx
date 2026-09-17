// shadcn-style primitives (Radix + cva + tailwind-merge), kept in one module
// so the app has a single import surface: `import { Button, Dialog } from "./ui"`.
import * as React from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import * as PopoverPrimitive from "@radix-ui/react-popover"
import * as SelectPrimitive from "@radix-ui/react-select"
import * as SwitchPrimitive from "@radix-ui/react-switch"
import * as SliderPrimitive from "@radix-ui/react-slider"
import * as TabsPrimitive from "@radix-ui/react-tabs"
import * as TooltipPrimitive from "@radix-ui/react-tooltip"
import * as SeparatorPrimitive from "@radix-ui/react-separator"
import * as LabelPrimitive from "@radix-ui/react-label"
import * as ScrollAreaPrimitive from "@radix-ui/react-scroll-area"
import { cva, type VariantProps } from "class-variance-authority"
import { Check, ChevronDown, ChevronUp, X } from "lucide-react"
import { cn } from "../lib/utils"

/* ------------------------------------------------------------------ Button */

const buttonVariants = cva(
	"inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4",
	{
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground hover:bg-primary/90",
				brand: "bg-brand text-white hover:bg-brand/90",
				secondary: "bg-secondary text-secondary-foreground hover:bg-accent border",
				outline: "border bg-transparent hover:bg-accent",
				ghost: "hover:bg-accent text-foreground",
				subtle: "text-muted-foreground hover:bg-accent hover:text-foreground",
				destructive: "bg-destructive text-destructive-foreground hover:bg-destructive/90",
			},
			size: {
				default: "h-8 px-3",
				sm: "h-7 px-2 text-[13px]",
				lg: "h-9 px-4",
				icon: "size-8",
				"icon-sm": "size-7",
				"icon-lg": "size-9",
			},
			round: { true: "rounded-full", false: "" },
		},
		defaultVariants: { variant: "ghost", size: "default", round: false },
	},
)

export type ButtonProps = React.ComponentProps<"button"> & VariantProps<typeof buttonVariants>

export function Button({ className, variant, size, round, ...props }: ButtonProps) {
	return (
		<button
			data-slot="button"
			className={cn(buttonVariants({ variant, size, round }), className)}
			{...props}
		/>
	)
}

/* ------------------------------------------------------- Input / Textarea */

export function Input({ className, ...props }: React.ComponentProps<"input">) {
	return (
		<input
			data-slot="input"
			className={cn(
				"bg-background h-8 w-full min-w-0 rounded-md border px-2.5 py-1 text-sm shadow-xs transition-[color,box-shadow] outline-none",
				"placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/25",
				"disabled:cursor-not-allowed disabled:opacity-50",
				className,
			)}
			{...props}
		/>
	)
}

export function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
	return (
		<textarea
			data-slot="textarea"
			className={cn(
				"bg-background field-sizing-content min-h-16 w-full rounded-md border px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] outline-none",
				"placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/25",
				className,
			)}
			{...props}
		/>
	)
}

export function Label({ className, ...props }: React.ComponentProps<typeof LabelPrimitive.Root>) {
	return (
		<LabelPrimitive.Root
			className={cn("flex items-center gap-2 text-[13px] font-medium select-none", className)}
			{...props}
		/>
	)
}

export function Field({
	label,
	hint,
	className,
	children,
}: {
	label?: React.ReactNode
	hint?: React.ReactNode
	className?: string
	children: React.ReactNode
}) {
	return (
		<div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
			{label ? <Label>{label}</Label> : null}
			{children}
			{hint ? <p className="text-muted-foreground text-xs leading-4">{hint}</p> : null}
		</div>
	)
}

/* -------------------------------------------------------- Card / Badge etc */

export function Card({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			className={cn("bg-card flex flex-col gap-4 rounded-xl border p-4 shadow-xs", className)}
			{...props}
		/>
	)
}

const badgeVariants = cva(
	"inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium",
	{
		variants: {
			tone: {
				muted: "bg-muted text-muted-foreground",
				ok: "border-emerald-600/30 bg-emerald-600/10 text-emerald-600 dark:text-emerald-400",
				bad: "border-destructive/30 bg-destructive/10 text-destructive",
				brand: "border-brand/30 bg-brand/10 text-brand",
			},
		},
		defaultVariants: { tone: "muted" },
	},
)

export function Badge({
	className,
	tone,
	...props
}: React.ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
	return <span className={cn(badgeVariants({ tone }), className)} {...props} />
}

export function Separator({
	className,
	orientation = "horizontal",
	...props
}: React.ComponentProps<typeof SeparatorPrimitive.Root>) {
	return (
		<SeparatorPrimitive.Root
			decorative
			orientation={orientation}
			className={cn(
				"bg-border shrink-0",
				orientation === "horizontal" ? "h-px w-full" : "h-full w-px",
				className,
			)}
			{...props}
		/>
	)
}

/* ------------------------------------------------------------------ Switch */

export function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
	return (
		<SwitchPrimitive.Root
			className={cn(
				"peer data-[state=checked]:bg-brand data-[state=unchecked]:bg-input inline-flex h-[18px] w-8 shrink-0 items-center rounded-full border border-transparent transition-all outline-none focus-visible:ring-[3px] focus-visible:ring-ring/25 disabled:opacity-50",
				className,
			)}
			{...props}
		>
			<SwitchPrimitive.Thumb className="pointer-events-none block size-3.5 rounded-full bg-white shadow transition-transform data-[state=checked]:translate-x-[15px] data-[state=unchecked]:translate-x-0.5" />
		</SwitchPrimitive.Root>
	)
}

/* ------------------------------------------------------------------ Slider */

export function Slider({ className, ...props }: React.ComponentProps<typeof SliderPrimitive.Root>) {
	return (
		<SliderPrimitive.Root
			className={cn("relative flex w-full touch-none items-center select-none", className)}
			{...props}
		>
			<SliderPrimitive.Track className="bg-input relative h-1 w-full grow overflow-hidden rounded-full">
				<SliderPrimitive.Range className="bg-brand absolute h-full" />
			</SliderPrimitive.Track>
			<SliderPrimitive.Thumb className="border-brand bg-background block size-3.5 rounded-full border-2 shadow transition-[color,box-shadow] outline-none focus-visible:ring-[3px] focus-visible:ring-ring/25" />
		</SliderPrimitive.Root>
	)
}

/* -------------------------------------------------------------------- Tabs */

export const Tabs = TabsPrimitive.Root

export function TabsList({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.List>) {
	return (
		<TabsPrimitive.List
			className={cn("bg-muted inline-flex items-center gap-1 rounded-lg p-1", className)}
			{...props}
		/>
	)
}

export function TabsTrigger({
	className,
	...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
	return (
		<TabsPrimitive.Trigger
			className={cn(
				"text-muted-foreground inline-flex items-center gap-1.5 rounded-md px-3 py-1 text-[13px] font-medium whitespace-nowrap transition-colors outline-none",
				"hover:text-foreground data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-xs",
				className,
			)}
			{...props}
		/>
	)
}

export function TabsContent({
	className,
	...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) {
	return <TabsPrimitive.Content className={cn("outline-none", className)} {...props} />
}

/* ------------------------------------------------------------------ Dialog */

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

export function DialogContent({
	className,
	children,
	showClose = true,
	...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & { showClose?: boolean }) {
	return (
		<DialogPrimitive.Portal>
			<DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=open]:fade-in-0 data-[state=closed]:fade-out-0" />
			<DialogPrimitive.Content
				className={cn(
					"bg-background fixed top-1/2 left-1/2 z-50 flex w-[min(940px,94vw)] max-h-[88vh] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border shadow-sheet",
					"data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=open]:zoom-in-95 data-[state=closed]:zoom-out-95",
					className,
				)}
				{...props}
			>
				{children}
				{showClose ? (
					<DialogPrimitive.Close className="text-muted-foreground hover:bg-accent hover:text-foreground absolute end-3 top-3 grid size-7 place-items-center rounded-md transition-colors">
						<X className="size-4" />
						<span className="sr-only">Закрыть</span>
					</DialogPrimitive.Close>
				) : null}
			</DialogPrimitive.Content>
		</DialogPrimitive.Portal>
	)
}

export function DialogTitle({
	className,
	...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
	return <DialogPrimitive.Title className={cn("text-base font-semibold", className)} {...props} />
}

export function DialogDescription({
	className,
	...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
	return (
		<DialogPrimitive.Description
			className={cn("text-muted-foreground text-xs", className)}
			{...props}
		/>
	)
}

/* ----------------------------------------------------------------- Popover */

export const Popover = PopoverPrimitive.Root
export const PopoverTrigger = PopoverPrimitive.Trigger
export const PopoverAnchor = PopoverPrimitive.Anchor

export function PopoverContent({
	className,
	align = "start",
	sideOffset = 8,
	...props
}: React.ComponentProps<typeof PopoverPrimitive.Content>) {
	return (
		<PopoverPrimitive.Portal>
			<PopoverPrimitive.Content
				align={align}
				sideOffset={sideOffset}
				className={cn(
					"bg-popover text-popover-foreground z-50 w-72 rounded-xl border p-3 shadow-sheet outline-none",
					"data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=open]:zoom-in-95 data-[state=closed]:zoom-out-95",
					className,
				)}
				{...props}
			/>
		</PopoverPrimitive.Portal>
	)
}

/* ------------------------------------------------------------------ Select */

export const Select = SelectPrimitive.Root
export const SelectValue = SelectPrimitive.Value

export function SelectTrigger({
	className,
	children,
	...props
}: React.ComponentProps<typeof SelectPrimitive.Trigger>) {
	return (
		<SelectPrimitive.Trigger
			className={cn(
				"bg-background flex h-8 w-full items-center justify-between gap-2 rounded-md border px-2.5 text-sm whitespace-nowrap shadow-xs outline-none",
				"hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/25 disabled:opacity-50",
				"data-[placeholder]:text-muted-foreground",
				className,
			)}
			{...props}
		>
			{children}
			<SelectPrimitive.Icon asChild>
				<ChevronDown className="size-3.5 opacity-60" />
			</SelectPrimitive.Icon>
		</SelectPrimitive.Trigger>
	)
}

export function SelectContent({
	className,
	children,
	position = "popper",
	...props
}: React.ComponentProps<typeof SelectPrimitive.Content>) {
	return (
		<SelectPrimitive.Portal>
			<SelectPrimitive.Content
				position={position}
				className={cn(
					"bg-popover text-popover-foreground relative z-50 max-h-72 min-w-[8rem] overflow-hidden rounded-lg border shadow-sheet",
					"data-[state=open]:animate-in data-[state=open]:fade-in-0",
					className,
				)}
				{...props}
			>
				<SelectPrimitive.ScrollUpButton className="flex h-6 items-center justify-center">
					<ChevronUp className="size-3.5" />
				</SelectPrimitive.ScrollUpButton>
				<SelectPrimitive.Viewport className="p-1">{children}</SelectPrimitive.Viewport>
				<SelectPrimitive.ScrollDownButton className="flex h-6 items-center justify-center">
					<ChevronDown className="size-3.5" />
				</SelectPrimitive.ScrollDownButton>
			</SelectPrimitive.Content>
		</SelectPrimitive.Portal>
	)
}

export function SelectItem({
	className,
	children,
	...props
}: React.ComponentProps<typeof SelectPrimitive.Item>) {
	return (
		<SelectPrimitive.Item
			className={cn(
				"relative flex w-full cursor-default items-center gap-2 rounded-md py-1.5 pe-8 ps-2 text-sm outline-none select-none",
				"focus:bg-accent data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
				className,
			)}
			{...props}
		>
			<SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
			<span className="absolute end-2 flex items-center">
				<SelectPrimitive.ItemIndicator>
					<Check className="size-3.5" />
				</SelectPrimitive.ItemIndicator>
			</span>
		</SelectPrimitive.Item>
	)
}

export function SelectLabel({
	className,
	...props
}: React.ComponentProps<typeof SelectPrimitive.Label>) {
	return (
		<SelectPrimitive.Label
			className={cn("text-muted-foreground px-2 py-1 text-[11px] uppercase", className)}
			{...props}
		/>
	)
}

export const SelectGroup = SelectPrimitive.Group

/* -------------------------------------------------------------- ScrollArea */

export function ScrollArea({
	className,
	children,
	viewportRef,
	onViewportScroll,
	...props
}: React.ComponentProps<typeof ScrollAreaPrimitive.Root> & {
	viewportRef?: React.Ref<HTMLDivElement>
	onViewportScroll?: React.UIEventHandler<HTMLDivElement>
}) {
	return (
		<ScrollAreaPrimitive.Root
			className={cn("relative overflow-hidden", className)}
			scrollHideDelay={400}
			{...props}
		>
			<ScrollAreaPrimitive.Viewport
				ref={viewportRef}
				onScroll={onViewportScroll}
				className="size-full rounded-[inherit] outline-none"
			>
				{children}
			</ScrollAreaPrimitive.Viewport>
			<ScrollAreaPrimitive.Scrollbar
				orientation="vertical"
				className="flex w-2.5 touch-none p-0.5 transition-colors select-none"
			>
				<ScrollAreaPrimitive.Thumb className="bg-muted-foreground/40 relative flex-1 rounded-full" />
			</ScrollAreaPrimitive.Scrollbar>
		</ScrollAreaPrimitive.Root>
	)
}

/* ----------------------------------------------------------------- Tooltip */

export const TooltipProvider = TooltipPrimitive.Provider

export function Tooltip({
	label,
	children,
	side = "top",
}: {
	label: React.ReactNode
	children: React.ReactNode
	side?: "top" | "right" | "bottom" | "left"
}) {
	if (!label) return <>{children}</>
	return (
		<TooltipPrimitive.Root>
			<TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
			<TooltipPrimitive.Portal>
				<TooltipPrimitive.Content
					side={side}
					sideOffset={6}
					className="bg-primary text-primary-foreground z-50 rounded-md px-2 py-1 text-xs shadow-sheet"
				>
					{label}
				</TooltipPrimitive.Content>
			</TooltipPrimitive.Portal>
		</TooltipPrimitive.Root>
	)
}
