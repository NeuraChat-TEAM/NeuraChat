# Сборка Go-части локальным SDK из Downloads\go.zip (Go нет в PATH).
$ErrorActionPreference = 'Stop'
$zip = 'C:\Users\Dimsk\Downloads\go.zip'
$sdk = 'C:\Users\Dimsk\gosdk'
$go  = Join-Path $sdk 'go\bin\go.exe'

if (-not (Test-Path $go)) {
	Write-Host "Распаковываю $zip -> $sdk"
	Expand-Archive -Path $zip -DestinationPath $sdk -Force
}
if (-not (Test-Path $go)) { throw "go.exe не найден в $sdk" }

$env:GOROOT = Join-Path $sdk 'go'
$env:PATH = "$($env:GOROOT)\bin;$($env:PATH)"

& $go version
Set-Location 'C:\Users\Dimsk\Documents\noti-source\noti\neura-wails'
& $go build ./...
Write-Host "exit=$LASTEXITCODE"
