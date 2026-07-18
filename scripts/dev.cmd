@echo off
setlocal
node "%~dp0dev.mjs" %*
exit /b %ERRORLEVEL%
