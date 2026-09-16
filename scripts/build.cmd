@echo off
REM Сборка Neura: подставляет Go и bun в PATH, синхронизирует notcode
REM и кладёт его рядом с Neura.exe как подпапку notcode.
setlocal
set ROOT=%~dp0..

if exist "%USERPROFILE%\gosdk\go\bin\go.exe" set PATH=%USERPROFILE%\gosdk\go\bin;%PATH%
if exist "%USERPROFILE%\.bun\bin\bun.exe" set PATH=%USERPROFILE%\.bun\bin;%PATH%
if exist "%USERPROFILE%\go\bin\wails.exe" set PATH=%USERPROFILE%\go\bin;%PATH%

where go >nul 2>nul || (echo [build] Go не найден & exit /b 1)
where wails >nul 2>nul || (echo [build] wails не найден & exit /b 1)

if exist "%ROOT%\notcode-embedded\package.json" goto :haveNotcode
call "%~dp0sync-notcode.cmd" || exit /b 1
:haveNotcode

pushd "%ROOT%"
wails build %*
set RC=%ERRORLEVEL%
popd
if not "%RC%"=="0" exit /b %RC%

call "%~dp0sync-notcode-deps.cmd" || echo [build] node_modules не скопированы - NotCode сделает bun install сам

robocopy "%ROOT%\notcode-embedded" "%ROOT%\build\bin\notcode" /MIR /NFL /NDL /NJH /NJS /NP /XD .git dist build /XF *.log
if errorlevel 8 exit /b 1

echo [build] готово: %ROOT%\build\bin\Neura.exe (+ notcode)
exit /b 0
