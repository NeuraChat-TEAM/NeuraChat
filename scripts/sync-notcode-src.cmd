@echo off
setlocal
set SRC=C:\Users\Dimsk\Documents\NotCode
set DST=%~dp0..\notcode-embedded
robocopy "%SRC%\src" "%DST%\src" /MIR /NFL /NDL /NJH /NJS /NP
robocopy "%SRC%\scripts" "%DST%\scripts" /MIR /NFL /NDL /NJH /NJS /NP
copy /Y "%SRC%\package.json" "%DST%\package.json" >nul
copy /Y "%SRC%\tsconfig.json" "%DST%\tsconfig.json" >nul
copy /Y "%SRC%\bun.lock" "%DST%\bun.lock" >nul
copy /Y "%SRC%\README.md" "%DST%\README.md" >nul
copy /Y "%SRC%\LICENSE" "%DST%\LICENSE" >nul
call "%~dp0sync-notcode-deps.cmd" "%SRC%" || echo [sync] node_modules не скопированы
echo SYNC_OK
