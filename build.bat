@echo off
echo ========================================
echo [1/2] Building frontend (Vite + Tailwind)...
echo ========================================

:: Use "call" to run npm, and specify the frontend directory with --prefix
:: so there's no need to constantly switch directories with cd
call npm --prefix gui\frontend run build

:: Check if frontend build succeeded; stop if it failed
if %errorlevel% neq 0 (
    echo.
    echo ❌ Frontend build failed. Please check the error messages!
    pause
    exit /b %errorlevel%
)

echo.
echo ========================================
echo [2/2] Building Go executable...
echo ========================================

:: Set Go build environment variables
set CGO_ENABLED=1
set PKG_CONFIG_PATH=C:\msys64\mingw64\lib\pkgconfig
set PATH=C:\msys64\mingw64\bin;%PATH%

:: Run Go build
go build -ldflags "-X main.SSM_VERSION=gui-custom" -o ssm-gui.exe

:: Check if Go build succeeded
if %errorlevel% neq 0 (
    echo.
    echo ❌ Go build failed!
    pause
    exit /b %errorlevel%
)

echo.
echo ✅ Done! Successfully generated ssm-gui.exe
pause