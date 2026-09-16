# Полный дамп одной записи HAR по номеру (1-based).
param([string]$Har, [int]$Index, [int]$Body = 6000)
$ErrorActionPreference = 'Stop'
$h = Get-Content -Raw -LiteralPath $Har | ConvertFrom-Json
$e = $h.log.entries[$Index - 1]
$u = [uri]$e.request.url
"path=" + $u.AbsolutePath
foreach ($n in @('REQ','RES')) {
	$t = if ($n -eq 'REQ') { $e.request.postData.text } else { $e.response.content.text }
	if (-not $t) { continue }
	if ($t.Length -gt $Body) { $t = $t.Substring(0, $Body) + '...' }
	$n + ': ' + ($t -replace 'https://', 'hxxps_//')
}
