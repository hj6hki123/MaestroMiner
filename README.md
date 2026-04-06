<p align="center">
    <a href="https://github.com/hj6hki123/ssm-gui">
        <img src="imgs/ssm_gui_github_banner.svg" alt="ssm-gui-banner"/>
    </a>
    <br>
    <strong>A Web-based GUI for automated mobile rhythm game playback and chart parsing.</strong>
</p>


# SSM Web GUI (Star Stone Miner Web GUI Version)

This project is an extended branch based on the core architecture of [kvarenzn/ssm](https://github.com/kvarenzn/ssm).
Given that the original author has stopped development (I think?), I've adapted its core architecture, using AI to quickly and easily introduce a GUI interface for greater ease of use. Since my HID is unusable, I've only tested the ADB functionality. There are still a few bugs to fix, but overall, the operation is smoother.

![Interface Features](/imgs/page.png "page")
##  What's new

### 🎵 Smart Song Search
![Keyword Search](/imgs/retrival.png "retrival")
- Real-time search across the full Bestdori library
- One-click difficulty selection (EASY → SPECIAL)
- Still supports manual Song ID and custom `.txt` chart paths for the power users

### ▶️ Playback Control Panel
![Control Panel](/imgs/paly_page.png "Control Panel")
- **Now Playing card** — jacket art, song title, band, difficulty, all in one glance
- **Interrupt & restart instantly** — hit Stop, then Start again without re-loading anything
- **Offset adjustment** — fine-tune timing on the fly with keyboard shortcuts




## Quick Start

1. **Download & Extract**: Get the latest release from [Releases](https://github.com/hj6hki123/ssm-gui/releases) and extract the archive.
* *Note*: If you have already downloaded the original project, simply place the .exe version into the same folder.
2. **Launch**: Double-click **ssm-gui.exe**, or run it from a terminal using the command **`./ssm-gui.exe`**.
3. **Access UI**: Your browser should open automatically at **http://127.0.0.1:8765**. If it doesn't, open it manually.
4. **Connect Device**: Connect your mobile device to your computer and ensure **ADB Debugging** is enabled.
5. **Device Setup**: 
   - Go to the **Settings** page to add your device.
   - **Select Connection Type**: Choose **ADB** (Highly Recommended). 
   - **Note on HID**: Since my physical device is incompatible with HID mode, this feature remains **untested** and may not function as expected. Contributions are welcome if you can verify this.
   - **Identify Device**: Enter your device ID (found via **`adb devices`**) and your screen resolution .
   - *Alternative*: You can skip the Settings page. When you click **LOAD & PLAY**, the terminal will prompt you to enter the connection type and resolution manually.
6. **Start Playing**: Follow the workflow in the UI: **Song Setup → Play Control → Start**.

## Disclaimer
This program was heavily developed with the assistance of AI. Please use it at your own discretion and feel free to report any unexpected bugs or issues.

> [!IMPORTANT]
> **This project is developed for personal learning and research purposes only. The stability and applicability of its functions are not guaranteed.**
>
> * **Non-Affiliation**: This project is an independent third-party tool and is **not** affiliated with, authorized by, or associated with any game developers, publishers, or related organizations.
> * **Risk of Use**: Use of this project may violate the service terms of the games or platforms involved, potentially leading to account suspension, bans, or data corruption.
> * **Limitation of Liability**: The author assumes no responsibility for any consequences resulting from the use of this project. Users are advised to evaluate the risks and use the software with caution.

## Future Projects
1. Mobile Porting & Deployment: Porting the application to mobile devices for use on non-rooted hardware (leveraging ADB tools such as Shizuku).

2. Automated Rhythm Game Playback: Implementation of image recognition for automated gameplay in rhythm games.

---

## 📜 License & Credits

* **Core Play Logic & Chart Parsing**: Credited to the original author [kvarenzn](https://github.com/kvarenzn/ssm).
* **Web GUI Implementation**: Custom integrated control panel developed specifically for this branch.
* This project is licensed under the **GPL-3.0-or-later** license.