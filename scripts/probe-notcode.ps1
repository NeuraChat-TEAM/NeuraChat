param(
  [int]$Port = 3000,
  [string]$McpPath = ""
)

# Проверяет, что notcode реально отвечает на /health и на MCP-маршруте из конфига.
# Работает на Windows PowerShell 5.1 (без -SkipHttpErrorCheck).

$cfgPath = "$env:USERPROFILE\.notcode\config.json"
$token = ""
if (Test-Path $cfgPath) {
  $cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json
  $token = $cfg.token
  if (-not $McpPath -and $cfg.sse -and $cfg.sse.mcpPath) { $McpPath = $cfg.sse.mcpPath }
  if ($Port -eq 0 -and $cfg.port) { $Port = [int]$cfg.port }
}
if (-not $McpPath) { $McpPath = "/mcp" }
$base = "http://127.0.0.1:$Port"

Add-Type -AssemblyName System.Net.Http | Out-Null
$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(20)

function Probe([string]$name, [string]$method, [string]$url, [bool]$withAuth, [string]$body) {
  try {
    $req = New-Object System.Net.Http.HttpRequestMessage ([System.Net.Http.HttpMethod]::$method), $url
    if ($body) {
      $req.Content = New-Object System.Net.Http.StringContent($body, [System.Text.Encoding]::UTF8, "application/json")
      $req.Headers.Accept.ParseAdd("application/json, text/event-stream")
    }
    if ($withAuth -and $token) { $req.Headers.Authorization = New-Object System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", $token) }
    $res = $client.SendAsync($req).GetAwaiter().GetResult()
    $text = $res.Content.ReadAsStringAsync().GetAwaiter().GetResult()
    if ($text.Length -gt 180) { $text = $text.Substring(0, 180) }
    "{0,-14} {1,3}  {2}" -f $name, [int]$res.StatusCode, ($text -replace "\s+", " ")
  } catch {
    "{0,-14} ERR  {1}" -f $name, $_.Exception.Message
  }
}

$init = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"neura-probe","version":"1.0"}}}'

"base = $base   mcpPath = $McpPath"
Probe "health"      "Get"  "$base/health"   $false $null
Probe "status/auth"  "Get"  "$base/status"   $true  $null
Probe "mcp/noauth"   "Post" "$base$McpPath"  $false $init
Probe "mcp/auth"     "Post" "$base$McpPath"  $true  $init
Probe "bad path"     "Post" "$base/nope"     $true  $init
