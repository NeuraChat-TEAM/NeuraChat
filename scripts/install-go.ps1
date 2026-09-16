# Ставит Go SDK в %USERPROFILE%\gosdk\go (именно там его ищет scripts\build.cmd).
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$version = 'go1.23.6'
$root = Join-Path $env:USERPROFILE 'gosdk'
$goExe = Join-Path $root 'go\bin\go.exe'

if (Test-Path $goExe) {
  Write-Host "already installed"
  & $goExe version
  exit 0
}

$zip = Join-Path $env:TEMP "$version.windows-amd64.zip"
if (-not (Test-Path $zip)) {
  $url = "https://go.dev/dl/$version.windows-amd64.zip"
  Write-Host "downloading $url"
  Invoke-WebRequest -Uri $url -OutFile $zip -TimeoutSec 540
}
Write-Host ("zip bytes: " + (Get-Item $zip).Length)

New-Item -ItemType Directory -Force -Path $root | Out-Null
Write-Host "extracting to $root"
Expand-Archive -Path $zip -DestinationPath $root -Force

& $goExe version
