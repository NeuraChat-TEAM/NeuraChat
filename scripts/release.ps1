# Packages a release: Neura.exe + zip + SHA256 + notes, then opens the folder.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'build\bin\Neura.exe'
if (-not (Test-Path $exe)) { throw "Neura.exe not found: $exe" }
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$out = Join-Path ([Environment]::GetFolderPath('Desktop')) "NeuraChat-Release-$stamp"
New-Item -ItemType Directory -Path $out -Force | Out-Null
Copy-Item $exe $out -Force
$zip = Join-Path $out 'NeuraChat-Windows-amd64.zip'
Compress-Archive -Path (Join-Path $out 'Neura.exe') -DestinationPath $zip -Force
$lines = foreach ($file in Get-ChildItem $out -File) {
  $hash = (Get-FileHash $file.FullName -Algorithm SHA256).Hash.ToLower()
  "$hash  $($file.Name)"
}
$lines | Set-Content (Join-Path $out 'SHA256SUMS.txt') -Encoding ASCII
$notes = @(
  "NeuraChat build $stamp",
  '',
  '- Full chat sync with Notion: history and status come from the thread/thread_message transcript.',
  '- State is re-checked when a chat is opened, when the window regains focus and every 20 seconds.',
  '- If Notion is still generating, the app shows it as running and picks up the answer after a dropped stream.',
  '- An empty cache no longer hides history when switching back to a chat.',
  '- MCP: query params, extra headers and tool auto-run are persisted and reused in new workspaces.',
  '- Reconnecting the same MCP server no longer creates a duplicate (URL compared without query).'
) -join [Environment]::NewLine
Set-Content (Join-Path $out 'RELEASE_NOTES.txt') $notes -Encoding UTF8
Write-Output $out
Start-Process explorer.exe $out
