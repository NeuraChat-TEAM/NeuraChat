@echo off
setlocal
set GOROOT=C:\Users\Dimsk\gosdk\go
set PATH=%GOROOT%\bin;%PATH%
cd /d "%~dp0.."
echo == go build ==
go build ./...
if errorlevel 1 (echo BUILD_FAIL & exit /b 1)
echo == go vet ==
go vet ./...
if errorlevel 1 (echo VET_FAIL & exit /b 1)
echo GO_OK
exit /b 0
