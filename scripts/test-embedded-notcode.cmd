@echo off
rem Проверяет СОБРАННУЮ копию NotCode из build\bin\notcode на порту 3011.
rem Рабочий экземпляр на 3000 не трогается.
setlocal
set DIR=%~dp0..\build\bin\notcode
cd /d "%DIR%"
start "notcode-embedded-test" /b cmd /c "bun run start -- --port 3011 --host 127.0.0.1 > C:\Users\Dimsk\AppData\Local\Temp\notcode-3011.log 2>&1"
ping -n 9 127.0.0.1 >nul
for /f "usebackq delims=" %%T in (`bun -e "const c=await Bun.file(process.env.USERPROFILE+'/.notcode/config.json').json();console.log(c.token)"`) do set TOK=%%T
echo == GET /health ==
curl -s -o - -w "\nHTTP %%{http_code}\n" http://127.0.0.1:3011/health
echo == POST /mcp initialize ==
curl -s -o - -w "\nHTTP %%{http_code}\n" -X POST http://127.0.0.1:3011/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Authorization: Bearer %TOK%" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"probe\",\"version\":\"1\"}}}"
echo == POST /mcp tools/list ==
curl -s -X POST http://127.0.0.1:3011/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Authorization: Bearer %TOK%" -d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}" > C:\Users\Dimsk\AppData\Local\Temp\mcp-tools-3011.json
bun -e "const j=await Bun.file('C:/Users/Dimsk/AppData/Local/Temp/mcp-tools-3011.json').json();console.log('tools:',(j.result?.tools||[]).length)"
echo == лог запуска ==
type C:\Users\Dimsk\AppData\Local\Temp\notcode-3011.log
echo == stop ==
for /f "tokens=5" %%P in ('netstat -ano ^| findstr /c:"127.0.0.1:3011"') do taskkill /PID %%P /F >nul 2>&1
exit /b 0
