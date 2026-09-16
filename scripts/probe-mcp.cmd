@echo off
setlocal
set "GOROOT=C:\Users\Dimsk\gosdk\go"
set "PATH=%GOROOT%\bin;%PATH%"
cd /d "%~dp0.."
rem Проверка всех воркспейсов на разрешённые кастомные MCP.
rem Работает долго, поэтому пишет в файл.
go run ./cmd/diag -probe-all -mcp %1 -token %2 > "%TEMP%\probe-mcp.log" 2>&1
echo PROBE_DONE
