$bin = "C:\Users\Dimsk\Documents\noti-source\noti\neura-wails\build\bin"

"--- было ---"
foreach ($n in @("Neura.exe","Neura.exe~")) {
  $p = Join-Path $bin $n
  if (Test-Path $p) { $f = Get-Item $p; "$n  $($f.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'))  $([math]::Round($f.Length/1MB,2))MB" }
}

"--- гасим процессы ---"
foreach ($name in @("Neura","bun","ngrok")) {
  $procs = Get-Process $name -ErrorAction SilentlyContinue
  foreach ($p in $procs) {
    try { Stop-Process -Id $p.Id -Force -ErrorAction Stop; "убит $name pid=$($p.Id)" } catch { "не убит $name pid=$($p.Id): $($_.Exception.Message)" }
  }
}
Start-Sleep -Milliseconds 800

$old = Join-Path $bin "Neura.exe~"
if (Test-Path $old) { Remove-Item $old -Force -ErrorAction SilentlyContinue; "удалён Neura.exe~" }
$exe = Join-Path $bin "Neura.exe"
if (Test-Path $exe) { Remove-Item $exe -Force -ErrorAction SilentlyContinue; "удалён старый Neura.exe" }
"готово к сборке: exe есть? $(Test-Path $exe)"
