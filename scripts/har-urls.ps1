# Выводит список запросов из HAR: метод, URL, статус.
param([string]$Har)
$ErrorActionPreference = 'Stop'
$h = Get-Content -Raw -LiteralPath $Har | ConvertFrom-Json
foreach ($e in $h.log.entries) {
	$u = $e.request.url
	if ($u -match '\.(js|css|woff2?|png|svg|ico)(\?|$)') { continue }
	"{0}`t{1}`t{2}" -f $e.request.method, $e.response.status, $u
}
