// Дамп MCP-запросов из HAR для сверки с internal/notion/mcp.go
const har = JSON.parse(await Bun.file("C:/Users/Dimsk/Downloads/mcp.har").text())
const want = [
	"checkMcpOAuthSupport",
	"validateMcpConnection",
	"postWorkflowsMcpServerConnect",
	"updateMcpServerModuleSettings",
	"disconnectPersonalMcpServerModule",
]
for (const e of har.log.entries) {
	const url: string = e.request?.url ?? ""
	const hit = want.find((w) => url.includes(w))
	if (!hit) continue
	console.log("=== " + hit + " | " + e.request?.method + " | status " + e.response?.status)
	console.log("REQ: " + (e.request?.postData?.text ?? "").slice(0, 1500))
	console.log("RES: " + (e.response?.content?.text ?? "").slice(0, 1500))
	console.log("")
}
