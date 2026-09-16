# Запускает собранный Neura.exe отсоединённо и показывает состояние процесса.
$ErrorActionPreference = 'SilentlyContinue'

$exe = Join-Path $PSScriptRoot '..\build\bin\Neura.exe'
$exe = (Resolve-Path $exe).Path

Get-Process Neura | Stop-Process -Force
Start-Sleep -Milliseconds 400

Start-Process -FilePath $exe -WorkingDirectory (Split-Path $exe)
Start-Sleep -Seconds 3

$p = Get-Process Neura
if ($p) {
  foreach ($proc in $p) {
    Write-Output ("RUNNING pid=" + $proc.Id + " title='" + $proc.MainWindowTitle + "'")
  }
} else {
  Write-Output 'NOT_RUNNING'
}
