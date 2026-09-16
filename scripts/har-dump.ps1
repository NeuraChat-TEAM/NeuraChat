param([string]$Har = "C:\Users\Dimsk\Downloads\mcp.har", [string]$Mode = "paths", [string]$Filter = "")
$h = Get-Content $Har -Raw | ConvertFrom-Json
if ($Mode -eq "paths") {
  $h.log.entries | ForEach-Object { $_.request.method + " " + ([uri]$_.request.url).AbsolutePath } | Sort-Object -Unique
  return
}
if ($Mode -eq "req") {
  foreach ($e in $h.log.entries) {
    $p = ([uri]$e.request.url).AbsolutePath
    if ($Filter -and $p -notlike "*$Filter*") { continue }
    "=== $($e.request.method) $p  ->  $($e.response.status)"
    if ($e.request.postData.text) { "--- request"; $e.request.postData.text.Substring(0, [Math]::Min(3000, $e.request.postData.text.Length)) }
    if ($e.response.content.text) { "--- response"; $e.response.content.text.Substring(0, [Math]::Min(6000, $e.response.content.text.Length)) }
  }
}
