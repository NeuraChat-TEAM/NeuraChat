// Дамп ответов usage-эндпоинтов из HAR для сверки с internal/notion/usage.go
const har = JSON.parse(await Bun.file("C:/Users/Dimsk/Downloads/mcp.har").text())
const want = ["getCreditRateLimitStatus", "getAIUsageEligibilityV2", "getAIUsageEligibility"]
for (const e of har.log.entries) {
	const url: string = e.request?.url ?? ""
	const hit = want.find((w) => url.includes(w))
	if (!hit) continue
	const text = e.response?.content?.text
	if (!text) continue
	let parsed: unknown
	try {
		parsed = JSON.parse(text)
	} catch {
		parsed = text.slice(0, 400)
	}
	console.log("=== " + hit + " | status " + e.response?.status)
	console.log("req: " + (e.request?.postData?.text ?? "").slice(0, 300))
	console.log(JSON.stringify(parsed, null, 1).slice(0, 4000))
}
