@echo off
setlocal
set ZIP=C:\Users\Dimsk\Downloads\go.zip
set DEST=C:\Users\Dimsk\gosdk
if not exist "%ZIP%" (echo NO_ZIP & exit /b 1)
if not exist "%DEST%\go\bin\go.exe" (
  if not exist "%DEST%" mkdir "%DEST%"
  echo Extracting...
  tar -xf "%ZIP%" -C "%DEST%"
)
if not exist "%DEST%\go\bin\go.exe" (echo NO_GO_AFTER_EXTRACT & dir /b "%DEST%" & exit /b 1)
"%DEST%\go\bin\go.exe" version
exit /b %ERRORLEVEL%
