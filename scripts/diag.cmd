@echo off
setlocal
set GOROOT=C:\Users\Dimsk\gosdk\go
set PATH=%GOROOT%\bin;%PATH%
cd /d "%~dp0.."
go run ./cmd/diag %*
exit /b %ERRORLEVEL%
