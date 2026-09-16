# Закрывает ТОЛЬКО Neura.exe. bun/ngrok не трогаем: через них работает
# внешнее MCP-подключение.
$procs = Get-Process Neura -ErrorAction SilentlyContinue
if (-not $procs) { "Neura не запущен" }
foreach ($p in $procs) {
  try { Stop-Process -Id $p.Id -Force -ErrorAction Stop; "закрыт Neura pid=$($p.Id)" }
  catch { "не закрылся Neura pid=$($p.Id): $($_.Exception.Message)" }
}
Start-Sleep -Milliseconds 800
$exe = "C:\Users\Dimsk\Documents\noti-source\noti\neura-wails\build\bin\Neura.exe"
if (Test-Path $exe) {
  $f = Get-Item $exe
  "exe изменён: $($f.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'))  размер: $([math]::Round($f.Length/1MB,2)) MB"
} else { "exe не найден" }
"сейчас: $((Get-Date).ToString('yyyy-MM-dd HH:mm:ss'))"
