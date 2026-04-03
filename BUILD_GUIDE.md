# SSM GUI — 從原始碼編譯完整指南

## 概覽：為什麼要重新編譯？

Release 版本是預先編譯好的 .exe + DLL，你無法直接「修改」它。  
要加 GUI 功能，需要：
1. 安裝編譯環境（Go + MSYS2/MinGW）
2. 複製新增的檔案到 ssm 原始碼目錄
3. 執行編譯指令
4. 把原本的 DLL 複製到新 .exe 旁邊

---

## 第一步：安裝編譯環境（Windows）

### 1-1. 安裝 Go

前往 https://go.dev/dl/ 下載 Windows amd64 版本（.msi 安裝檔）。

安裝後，開新的命令提示字元（CMD），確認：
```
go version
```
應該看到類似 `go version go1.24.x windows/amd64`

### 1-2. 安裝 MSYS2（提供 gcc、libusb、ffmpeg）

1. 前往 https://www.msys2.org 下載安裝程式，一路按 Next  
2. 安裝完成後，開啟 **MSYS2 UCRT64** 終端機（不是 MSYS2 MSYS）
3. 執行以下指令安裝套件：

```bash
# 更新套件資料庫
pacman -Syu

# 若視窗自動關閉，重新開啟 MSYS2 UCRT64 再執行：
pacman -Su

# 安裝編譯工具與函式庫
pacman -S \
  mingw-w64-x86_64-gcc \
  mingw-w64-x86_64-pkgconf \
  mingw-w64-x86_64-libusb \
  mingw-w64-x86_64-ffmpeg \
  mingw-w64-x86_64-ntldd
```

> 注意：原始 CI 用的是 `mingw-w64-x86_64-*`（MINGW64），  
> 如果你用的是 UCRT64，把前綴換成 `mingw-w64-ucrt-x86_64-*`

### 1-3. 確認 gcc 可以被 Go 找到

**方法 A（推薦）**：把 MinGW 的 bin 加入 PATH

在 Windows 搜尋「環境變數」→「使用者的環境變數」→ Path → 新增：
```
C:\msys64\mingw64\bin
```

重新開啟 CMD，執行：
```
gcc --version
```
應看到版本資訊。

**方法 B**：每次編譯前在 CMD 手動 set PATH（臨時）：
```
set PATH=C:\msys64\mingw64\bin;%PATH%
```

---

## 第二步：下載 ssm 原始碼

```
git clone https://github.com/kvarenzn/ssm.git
cd ssm
```

---

## 第三步：複製新增檔案

把本包提供的檔案複製到 ssm 目錄裡，對應位置如下：

```
ssm/
├── main.go                   ← 替換（新增 --gui flag）
├── openbrowser_windows.go    ← 新增
├── openbrowser_unix.go       ← 新增
└── gui/                      ← 新增整個目錄
    ├── server.go
    └── static/
        └── index.html
```

**複製指令（假設你把本包解壓到 C:\ssm-gui-patch）：**

```
# 替換 main.go
copy /Y C:\ssm-gui-patch\main.go .\main.go

# 複製新檔
copy C:\ssm-gui-patch\openbrowser_windows.go .\
copy C:\ssm-gui-patch\openbrowser_unix.go .\

# 建立 gui 目錄並複製
mkdir gui
mkdir gui\static
copy C:\ssm-gui-patch\gui\server.go .\gui\
copy C:\ssm-gui-patch\gui\static\index.html .\gui\static\
```

---

## 第四步：編譯

在 ssm 目錄下，開 CMD（已確認 gcc 在 PATH）：

```
set CGO_ENABLED=1
set PKG_CONFIG_PATH=C:\msys64\mingw64\lib\pkgconfig
set PATH=C:\msys64\mingw64\bin;%PATH%

go build -ldflags "-X main.SSM_VERSION=gui-custom" -o ssm-gui.exe
```

> 如果出現 `pkg-config: not found` 錯誤，  
> 確認 `C:\msys64\mingw64\bin\pkg-config.exe` 存在。

成功後會出現 `ssm-gui.exe`。

---

## 第五步：複製 DLL

新的 `ssm-gui.exe` 仍然依賴相同的 DLL，把 release 包裡的所有 `.dll` 複製到 `ssm-gui.exe` 同目錄即可：

```
# 假設 release 解壓在 C:\ssm-release\
copy C:\ssm-release\*.dll .
```

或者用 ntldd 自動抓（需在 MSYS2 UCRT64 下）：

```bash
mkdir build
cp ssm-gui.exe build/
for dll in $(/c/msys64/mingw64/bin/ntldd.exe -R ./ssm-gui.exe | awk '{ if ($3 ~ /^C:\\msys64/) print $3; }'); do
  cp -v $dll ./build/
done
```

---

## 第六步：使用

### 原本的 CLI 模式（完全不變）
```
ssm-gui.exe -d expert -n 325
```

### 新的 GUI 模式
```
ssm-gui.exe --gui
```

瀏覽器會自動開啟 `http://127.0.0.1:8765`

自訂 port：
```
ssm-gui.exe --gui --port 9090
```

---

## 常見問題

### Q: `cgo: C compiler "gcc" not found`
確認 `C:\msys64\mingw64\bin` 在 PATH，且執行 `gcc --version` 有回應。

### Q: `libusb: not found` / `pkg-config: libusb-1.0 not found`
```
set PKG_CONFIG_PATH=C:\msys64\mingw64\lib\pkgconfig
```
確認 `C:\msys64\mingw64\lib\pkgconfig\libusb-1.0.pc` 存在。

### Q: `go: module requires Go 1.25`
Go 1.25 尚未正式發布，可能需要使用 gotip 或最新 RC 版本。  
臨時解法：把 `go.mod` 的 `go 1.25` 改成 `go 1.24`，通常不影響功能。

### Q: 瀏覽器沒有自動開啟
手動輸入 `http://127.0.0.1:8765` 即可。

### Q: GUI 顯示「就緒」但點「開始」沒反應
SSE 連線需要瀏覽器支援，建議使用 Chrome / Edge / Firefox 最新版。

---

## 檔案結構說明

```
gui/server.go       — HTTP server + SSE 廣播 + 所有 API endpoint
gui/static/index.html — 前端 UI（單一 HTML 打包，無需 npm）
main.go             — 加入 --gui / --port flag，注入 callback
openbrowser_*.go    — 跨平台自動開瀏覽器
```

API 端點一覽：
```
GET  /              → 前端 HTML
GET  /api/events    → SSE 即時狀態推送
GET  /api/status    → 當前狀態 JSON
POST /api/run       → 送入設定，開始準備播放
POST /api/start     → 觸發開始（等同按 ENTER）
POST /api/offset    → 調整時間偏移 { "delta": 10 }
POST /api/stop      → 中斷播放（等同 Ctrl-C）
GET  /api/device    → 取得裝置設定
POST /api/device    → 儲存裝置尺寸
POST /api/extract   → 解包素材 { "path": "..." }
```
