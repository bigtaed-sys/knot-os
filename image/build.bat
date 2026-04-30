@echo off
REM Wrapper that runs build.ps1 with sane execution-policy.
REM
REM Use:  image\build.bat              (default distro)
REM       image\build.bat -Clean       (force fresh WSL build dir)
REM       image\build.bat -Distro Ubuntu-22.04
REM
REM The window stays open at the end so you can read the result
REM when launched from Explorer.

setlocal
set "SCRIPT=%~dp0build.ps1"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" %*
set "RC=%ERRORLEVEL%"

if not "%CMDCMDLINE:~0,4%"=="cmd " (
    echo.
    echo Press any key to close...
    pause >nul
)
exit /b %RC%
