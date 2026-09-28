import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

/** shadcn's class merger: conditional classes + Tailwind conflict resolution. */
export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs))
}

export function formatTime(ms: number) {
	return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
}

export function relTime(ms: number): string {
	const min = Math.max(0, Math.floor((Date.now() - ms) / 60000))
	if (min < 1) return "только что"
	if (min < 60) return `${min}m назад`
	const h = Math.floor(min / 60)
	if (h < 24) return `${h}h назад`
	const d = Math.floor(h / 24)
	if (d < 7) return `${d}d назад`
	const w = Math.floor(d / 7)
	if (w < 5) return `${w}w назад`
	return `${Math.floor(d / 30)}mo назад`
}
