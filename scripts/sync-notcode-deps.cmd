@echo off
REM Кладёт node_modules рядом со встроенным NotCode.
REM Без них bun падает: "Cannot find package 'elysia'".
setlocal
set SRC=%~1
if "%SRC%"=="" set SRC=C:\Users\Dimsk\Documents\NotCode
set ROOT=%~dp0..

if not exist "%SRC%\node_modules\elysia\package.json" (
  echo [deps] в "%SRC%\node_modules" нет elysia - сначала bun install в источнике
  exit /b 1
)

robocopy "%SRC%\node_modules" "%ROOT%\notcode-embedded\node_modules" /MIR /NFL /NDL /NJH /NJS /NP
if errorlevel 8 exit /b 1

if exist "%ROOT%\build\bin\notcode\package.json" (
  robocopy "%SRC%\node_modules" "%ROOT%\build\bin\notcode\node_modules" /MIR /NFL /NDL /NJH /NJS /NP
  if errorlevel 8 exit /b 1
)

echo DEPS_OK
exit /b 0
