@echo off
setlocal
pushd "%~dp0" || exit /b 1

odin build . -out:infinity-ui.exe -subsystem:windows
set ERR=%ERRORLEVEL%
if %ERR% neq 0 (
  echo build failed
  popd
  exit /b %ERR%
)
echo built infinity-ui.exe
popd
endlocal
