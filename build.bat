set CGO_ENABLED=1
set PKG_CONFIG_PATH=C:\msys64\mingw64\lib\pkgconfig
set PATH=C:\msys64\mingw64\bin;%PATH%

go build -ldflags "-X main.SSM_VERSION=gui-custom" -o ssm-gui.exe