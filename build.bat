@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title Neura Build

set "LOCAL_GO=%USERPROFILE%\gosdk\go\bin"
if exist "%LOCAL_GO%\go.exe" set "PATH=%LOCAL_GO%;%PATH%"

where go >nul 2>nul || (echo [ERROR] Go not found. Install Go or unpack it to %%USERPROFILE%%\gosdk\go.& exit /b 1)
where bun >nul 2>nul || (echo [ERROR] Bun not found. Install Bun and reopen the terminal.& exit /b 1)

echo [1/3] Building frontend...
pushd frontend
call bun install --frozen-lockfile || (popd & exit /b 1)
call bun run build || (popd & exit /b 1)
popd

echo [2/3] Checking Go code...
go test ./... || exit /b 1
go vet ./... || exit /b 1

echo [3/3] Building Neura.exe...
where wails >nul 2>nul
if not errorlevel 1 (
  wails build
) else (
  go run github.com/wailsapp/wails/v2/cmd/wails@v2.10.1 build
)
if errorlevel 1 exit /b 1

echo.
echo Build complete: build\bin\Neura.exe
endlocal
