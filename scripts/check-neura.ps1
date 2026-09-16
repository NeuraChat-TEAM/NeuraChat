$procs = Get-Process Neura -ErrorAction SilentlyContinue
if (-not $procs) { "Neura.exe не запущен"; exit 1 }
foreach ($p in $procs) { "pid=$($p.Id)  старт=$($p.StartTime.ToString('HH:mm:ss'))  память=$([math]::Round($p.WorkingSet64/1MB,1))MB" }
$c = Get-Process bun,ngrok -ErrorAction SilentlyContinue
foreach ($p in $c) { "дочерний: $($p.ProcessName) pid=$($p.Id)" }
