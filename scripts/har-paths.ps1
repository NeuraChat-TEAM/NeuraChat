# Печатает только path запросов из HAR и урезанное тело (без полных URL).
param([string]$Har, [int]$Body = 900)
$ErrorActionPreference = 'Stop'
$h = Get-Content -Raw -LiteralPath $Har | ConvertFrom-Json
$i = 0
foreach ($e in $h.log.entries) {
	$i++
	$u = [uri]$e.request.url
	if ($u.AbsolutePath -match '\.(js|css|woff2?|png|svg|ico)$') { continue }
	if ($u.Host -like '*splunk*') { continue }
	$host2 = $u.Host -replace '\.', '_'
	"### {0} {1} {2} host={3} path={4}" -f $i, $e.request.method, $e.response.status, $host2, $u.AbsolutePath
	$pd = $e.request.postData.text
	if ($pd) {
		if ($pd.Length -gt $Body) { $pd = $pd.Substring(0, $Body) + '...' }
		'REQ: ' + ($pd -replace 'https://', 'hxxps_//')
	}
	$rc = $e.response.content.text
	if ($rc) {
		if ($rc.Length -gt $Body) { $rc = $rc.Substring(0, $Body) + '...' }
		'RES: ' + ($rc -replace 'https://', 'hxxps_//')
	}
}
