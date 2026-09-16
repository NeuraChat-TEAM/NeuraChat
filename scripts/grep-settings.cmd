@echo off
cd /d "%~dp0.."
set F=frontend\src\components\SettingsModal.tsx
echo == ngrokRunning
findstr /n /c:"ngrokRunning" %F%
echo == tunnel
findstr /n /i /c:"tunnel" %F%
echo == usage
findstr /n /c:"UsageBar" %F%
echo == basic
findstr /n /c:"basicSpace" %F%
echo == limitReached
findstr /n /c:"limitReached" %F%
echo == ngrok text
findstr /n /c:"ngrok" %F%
exit /b 0
