@echo off
rem Поднимает ВТОРОЙ экземпляр NotCode на порту 3010 и проверяет POST /mcp.
rem Рабочий NotCode на 3000 не трогается.
setlocal
set SRC=C:\Users\Dimsk\Documents\NotCode
set NOTCODE_PORT=3010
set NOTCODE_HOST=127.0.0.1
cd /d "%SRC%"
start "notcode-test" /b cmd /c "bun run src/index.ts start > C:\Users\Dimsk\AppData\Local\Temp\notcode-3010.log 2>&1"
ping -n 7 127.0.0.1 >nul
for /f "usebackq delims=" %%T in (`bun -e "const c=await Bun.file(process.env.USERPROFILE+'/.notcode/config.json').json();console.log(c.token)"`) do set TOK=%%T
echo TOKEN_LEN_OK
echo == GET /health ==
curl -s -o - -w "\nHTTP %%{http_code}\n" http://127.0.0.1:3010/health
echo == POST /mcp initialize ==
curl -s -o - -w "\nHTTP %%{http_code}\n" -X POST http://127.0.0.1:3010/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Authorization: Bearer %TOK%" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"probe\",\"version\":\"1\"}}}"
echo == POST /mcp tools/list ==
curl -s -X POST http://127.0.0.1:3010/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Authorization: Bearer %TOK%" -d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}" > C:\Users\Dimsk\AppData\Local\Temp\mcp-tools.json
bun -e "const j=await Bun.file('C:/Users/Dimsk/AppData/Local/Temp/mcp-tools.json').json();console.log('tools:',(j.result?.tools||[]).length)"
echo == POST /mcp без токена ==
curl -s -o nul -w "HTTP %%{http_code}\n" -X POST http://127.0.0.1:3010/mcp -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/list\"}"
echo == GET /mcp ==
curl -s -o - -w "\nHTTP %%{http_code}\n" http://127.0.0.1:3010/mcp
echo == stop test instance ==
for /f "tokens=5" %%P in ('netstat -ano ^| findstr /r /c:":3010 .*LISTENING"') do taskkill /PID %%P /F >nul 2>&1
exit /b 0
