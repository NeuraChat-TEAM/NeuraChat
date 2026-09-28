// Тосты из мест, где нет доступа к onToast: событие на window вместо
// проброса пропсов сквозь всю цепочку компонентов. Слушатель живёт в App.

export const TOAST_EVENT = "neura:toast"

export type ToastDetail = { message: string; error?: boolean }

/** Показать тост из любого места фронта. */
export function toast(message: string, error?: boolean) {
	if (!message) return
	window.dispatchEvent(
		new CustomEvent<ToastDetail>(TOAST_EVENT, { detail: { message, error } }),
	)
}

/** Подписка на тосты; возвращает функцию отписки для useEffect. */
export function onToastEvent(handler: (detail: ToastDetail) => void) {
	const listener = (event: Event) => {
		const detail = (event as CustomEvent<ToastDetail>).detail
		if (detail?.message) handler(detail)
	}
	window.addEventListener(TOAST_EVENT, listener)
	return () => window.removeEventListener(TOAST_EVENT, listener)
}
