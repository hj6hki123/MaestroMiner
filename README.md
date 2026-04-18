<p align="center">
    <strong>A Web-based GUI for automated mobile rhythm game playback and chart parsing.</strong>
</p>

# SSM Web GUI — MAA Branch

This is a personal fork of [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui) (itself a fork of [kvarenzn/ssm](https://github.com/kvarenzn/ssm)).

The upstream main branch is now feature-frozen. This branch adds a **fully unattended / hands-off play mode** by integrating [MaaFramework](https://github.com/MaaXYZ/MaaFramework) for in-game navigation and [juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus)'s calibrated touch method.

**Support: BanG Dream! Girls Band Party · Project Sekai: Colorful Stage (full-auto not yet supported)**

---

## What's new compared to upstream

###  MAA Full-Auto Navigation

Integrates [MaaFramework](https://github.com/MaaXYZ/MaaFramework) (v5.10.0) to drive the **entire pre-game flow** over ADB without any human input:

- Launches the game and waits through the start screen
- Navigates to the song-select screen automatically
- Selects difficulty
- Detects the currently highlighted song via **OCR** (`SongRecognition` pipeline task)
- Enters the live screen and hands off to the playback engine once the pause button is visible
- **Loops continuously** — after one song ends it returns to song-select and plays the next

Enable via the **Auto Navigation** toggle in the Play Control panel (ADB backend required).

---

###  Auto-Trigger

Integrates the scrcpy touch-coordinate method contributed by [juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus).

All touch packets are addressed in the scrcpy **stream coordinate space**, with correct width/height swap for landscape orientation. This produces significantly more stable and visually accurate hit positions compared to the previous approach.

Instead of manually pressing **Start** when the first note approaches, **Auto-Trigger** watches the live scrcpy MJPEG stream and fires the trigger automatically.

Enable via the **Auto Trigger** toggle; the configuration panel expands/collapses accordingly.

---

###  AP-Avoidance 

Forces a specific number of tap notes to land as **Great** (slightly early/late) rather than Perfect, so the score does not register as an All-Perfect.

| Setting | Description |
|---------|-------------|
| **Great Offset (ms)** | Absolute timing shift applied to chosen taps (default 10 ms) |
| **Great Count** | Exact number of taps to shift; `0` = probability mode |

The first tap note is always left as Perfect. Remaining targets are chosen randomly from eligible taps so the selection is unpredictable each run.


---

## What's inherited from upstream

All features from [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui) main are included:

- **Web GUI** — playback control panel, now-playing card, jacket art, one-click difficulty selection
- **Smart song search** — real-time search across the full Bestdori library, keyword or song ID
- **Offset adjustment** — keyboard shortcut fine-tuning on the fly
- **BanG Dream + Project Sekai** chart support (BMS / SUS)
- **HID and ADB** controller backends
- **Legacy CLI** — all original `kvarenzn/ssm` command-line parameters still work

---

## Quick Start

### English

1. **Download** — get the latest package from [Releases](../../releases) and extract it.
2. **Start** — double-click `ssm-gui.exe`; the UI opens at `http://127.0.0.1:8765`.
3. **Connect phone** — USB cable, USB debugging enabled.
4. **Copy game resources** to your PC:
   ```bash
   adb pull /sdcard/Android/data/jp.co.craftegg.band/files/data/
   ```
5. **Settings** — add device (serial auto-detected), choose HID or ADB.
6. **Manual mode** — Song Setup → Play Control → Start. Press **Start** (or Enter / Space) when the first note approaches.
7. **Full-auto mode** — enable **Auto Navigation**, select the game server region, press Start. Enable **Auto Trigger** for completely hands-free operation.

### 中文

1. **下载** — 从 [Releases](../../releases) 下载最新版本并解压。
2. **启动** — 双击 `ssm-gui.exe`，浏览器自动打开 `http://127.0.0.1:8765`。
3. **连接手机** — USB 数据线，开启 USB 调试。
4. **复制游戏资源**到电脑：
   ```bash
   adb pull /sdcard/Android/data/jp.co.craftegg.band/files/data/
   ```
5. **Settings** — 添加设备（序列号可自动检测），选择 HID 或 ADB 连接方式。
6. **手动模式** — Song Setup → Play Control → Start。第一个音符接近判定线时按 **Start**（或 Enter / Space）。
7. **全自动模式** — 开启 **Auto Navigation** 并选择游戏服务器区域，按 Start。同时开启 **Auto Trigger** 可实现完全无人值守。

---

## Credits

- **Core play logic & chart parsing** — [kvarenzn/ssm](https://github.com/kvarenzn/ssm)
- **Web GUI shell** — [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui)
- **Touch calibration method** — [juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus)
- **Automation framework** — [MaaXYZ/MaaFramework](https://github.com/MaaXYZ/MaaFramework)
- Licensed under **GPL-3.0-or-later**

---

## Disclaimer

> [!IMPORTANT]
> **This project is developed for personal learning and research purposes only.**
>
> - **Non-Affiliation**: Independent third-party tool, not affiliated with any game developer or publisher.
> - **Risk of Use**: Use may violate game service terms, potentially leading to account suspension or bans.
> - **Limitation of Liability**: The author assumes no responsibility for any consequences resulting from use.


### Demonstration
https://github.com/user-attachments/assets/09c6585a-64fb-44ad-af82-6239ee994b1b
