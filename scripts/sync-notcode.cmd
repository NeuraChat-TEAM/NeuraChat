@echo off
REM Копирует модифицированный notcode в репозиторий (notcode-embedded),
REM откуда он попадает рядом с Neura.exe как подпапка notcode.
setlocal
set SRC=%~1
if "%SRC%"=="" set SRC=%USERPROFILE%\Downloads\notcode-main (2)\notcode-main
set DST=%~dp0..\notcode-embedded

if not exist "%SRC%\package.json" (
  echo [sync-notcode] нет package.json в "%SRC%"
  exit /b 1
)

robocopy "%SRC%" "%DST%" /MIR /NFL /NDL /NJH /NJS /NP /XD node_modules .git dist build /XF *.log
if errorlevel 8 exit /b 1
echo [sync-notcode] notcode синхронизирован в "%DST%"
exit /b 0
