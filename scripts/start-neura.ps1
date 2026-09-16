# Запускает Neura.exe полностью отдельным процессом (не дочерним для текущей оболочки),
# иначе приложение умирает вместе с терминалом агента.
$exe = Join-Path $PSScriptRoot '..\build\bin\Neura.exe'
$exe = (Resolve-Path $exe).Path
$res = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
  CommandLine      = '"' + $exe + '"'
  CurrentDirectory = (Split-Path $exe -Parent)
}
"create rc=$($res.ReturnValue) pid=$($res.ProcessId)"
Start-Sleep -Seconds 5
$p = Get-Process -Id $res.ProcessId -ErrorAction SilentlyContinue
if ($p) { "alive pid=$($p.Id) память=$([math]::Round($p.WorkingSet64/1MB,1))MB" } else { "процесс умер" }
