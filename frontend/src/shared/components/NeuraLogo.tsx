import { cn } from "../lib/utils"

export default function NeuraLogo({ className }: { className?: string; animated?: boolean }) {
	return (
		<svg viewBox="0 0 512 512" aria-hidden="true" className={cn("neura-logo", className)}>
			<rect x="36" y="36" width="440" height="440" rx="124" fill="#111214" />
			<path d="M144 286C190 286 202 218 256 218C310 218 322 286 368 286" fill="none" stroke="#F2F2EF" strokeWidth="42" strokeLinecap="round" />
		</svg>
	)
}
