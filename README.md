<p align="center">
    <strong>A Web-based GUI for automated mobile rhythm game playback and chart parsing, powered by MAA.</strong>
</p>

# MaestroMiner

This project is an advanced evolution of [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui) (originally a fork of [kvarenzn/ssm](https://github.com/kvarenzn/ssm)). 

 **MaestroMiner** transforms the core playback engine into a **fully unattended, hands-off experience**. By orchestrating [MaaFramework](https://github.com/MaaXYZ/MaaFramework) for in-game navigation and integrating [juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus)'s calibrated touch methods, it conducts your gameplay seamlessly from the start screen to the final score.

**Support: BanG Dream! Girls Band Party · Project Sekai: Colorful Stage (full-auto not yet supported)**
Full automation only supports the JP&TW version.Expanding multilingual support
---

##  What's New in MaestroMiner

###  MAA Full-Auto Navigation
Integrates [MaaFramework](https://github.com/MaaXYZ/MaaFramework) (v5.10.0) to drive the **entire pre-game flow** over ADB without any human input:
* Launches the game and waits through the start screen.
* Navigates to the song-select screen automatically.
* Selects difficulty.
* Detects the currently highlighted song via **OCR** (`SongRecognition` pipeline task).
* Enters the live screen and hands off to the playback engine once the pause button is visible.
* **Loops continuously** — after one song ends, it returns to song-select and plays the next.

> *Enable via the **Auto Navigation** toggle in the Play Control panel (ADB backend required).*

###  Auto-Trigger
Integrates the scrcpy touch-coordinate method contributed by [juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus).
Instead of manually pressing **Start** when the first note approaches, **Auto-Trigger** watches the live scrcpy MJPEG stream and fires the trigger automatically.

> *Enable via the **Auto Trigger** toggle; the configuration panel expands/collapses accordingly.*

###  AP-Avoidance 
Forces a specific number of tap notes to land as **Great** (slightly early/late) rather than Perfect, so the score does not register as a highly suspicious All-Perfect.

| Setting | Description |
| :--- | :--- |
| **Great Offset (ms)** | Absolute timing shift applied to chosen taps (default **10 ms**). |
| **Great Count** | Exact number of taps to shift; `0` = probability mode. |

*Note: The first tap note is always left as Perfect. Remaining targets are chosen randomly from eligible taps so the selection is unpredictable each run.*


---

##  Quick Start

### 1 · Prerequisites

| Requirement | Notes |
| :--- | :--- |
| Android phone | USB debugging enabled (Developer Options → USB Debugging) |
| USB cable | Data-capable (not charge-only) |
| ADB | Included in the package — no separate install needed |

> **First time enabling USB debugging?**  
> Go to **Settings → About Phone**, tap **Build Number** 7 times to unlock Developer Options, then enable **USB Debugging** inside Developer Options.

---

### 2 · Installation

1. Download the latest `.zip` from [Releases](../../releases) and extract it anywhere.
2. Double-click **`maestrominer.exe`**.  
   A browser tab opens automatically at `http://127.0.0.1:8765`.  
   If it doesn't, open the URL manually.

---

### 3 · Connect Your Phone & Register the Device

1. Plug the phone into your PC via USB.
2. On the phone, tap **Allow** when the *"Allow USB debugging?"* prompt appears.
3. In the app, click **Settings** in the left navigation bar.
4. Scroll down to the **"Add / Update Device"** card.
5. Click **Auto-Detect** next to the **Serial** field — it queries ADB and fills in your device's serial number automatically. If it fails, run `adb devices` in a terminal and paste the serial manually.
6. Fill in the **screen resolution**:
   - Run the following command in a terminal (the `adb` binary is inside the extracted folder):
     ```bash
     adb shell wm size
     ```
     Example output: `Physical size: 1080x2340` → enter **1080** in *Width* and **2340** in *Height*.
   - Alternatively, on the phone go to **Settings → Display → Screen Resolution** (label varies by brand).
7. Click **Save**. A green confirmation message appears below the button.

> ⚠️ The resolution must match the **actual rendered resolution** of the phone, not the display scale setting. `adb shell wm size` always returns the correct value.

---

### 4 · Copy Game Resources

The app uses local game asset files to look up song titles, jacket images, and difficulty levels. Without them, song search and auto-detection will not work.

Open a terminal inside the extracted folder (where `adb.exe` is) and run the command for your game:

**BanG Dream! Girls Band Party (JP)**
```bash
adb pull /sdcard/Android/data/jp.co.craftegg.band/files/data/
```

**Project Sekai: Colorful Stage (JP)**
```bash
adb pull /sdcard/Android/data/com.sega.ColorfulStage/files/
```

This copies the folder to your PC. Leave it next to `maestrominer.exe` — the app finds it automatically on startup.

---

### 5 · Choose a Connection Backend

Before playing, select how the app sends tap inputs to the phone. In the **Song Setup** page, look for the **Connection** selector near the top:

| Option | How it works | When to use |
| :--- | :--- | :--- |
| **HID** | Sends taps over USB as a HID device — very low latency | Recommended for most users |
| **ADB** | Sends taps via `adb shell input tap` — slightly higher latency | Use if HID is not registering taps |

If taps are missing or the phone doesn't respond, switch to ADB.

---

### 6 · Play — Manual Mode

You select the song and manually press Start when the notes arrive. Best for first-time users.

1. Click **Song Setup** in the left navigation bar.
2. Use the **search bar** to find a song by name, or type a Song ID directly in the *"Or enter Song ID directly"* field.
3. Click a song in the dropdown to select it. The song title and jacket appear in the top bar.
4. Select a **Difficulty** using the row of buttons (EASY / NORMAL / HARD / EXPERT / SPECIAL / APPEND).
5. Click **▶ Load & Prepare** at the bottom of the page.
6. The app switches to the **Play Control** page automatically.
7. Start the song on your phone manually. When the **first note is about to appear**, press **▶ START** on screen — or use `Enter` / `Space`.

---

### 7 · Play — Auto Trigger

You still select the song manually and load it, but the app **detects the moment the first notes appear** on screen and fires the start command automatically — no need to watch and press Start yourself.

1. In **Song Setup**, enable the **Auto Trigger** toggle. A live preview panel and sliders expand below it.
2. **First time setup — Calibration:**
   - Click the **Calibrate** button next to the toggle.
   - A live preview of your phone's screen opens in a popup.
   - Use the **Detect Y** slider to position the yellow detection line on the note lane just before the judgement line.
   - Use **Spacing** to spread the 7 detection boxes until each one aligns with a lane.
   - Use **Sensitivity** to control how bright a note pixel must be to count as a trigger.
   - Click **Apply & Close** when all 7 boxes sit correctly over the lanes.
3. Search and select a song as in Manual Mode, then click **▶ Load & Prepare**.
4. Start the song on your phone. The app watches the live stream; when note brightness crosses the threshold, it triggers Start automatically.
5. To stop at any time, click **■ Stop** on the Play Control page.

---

### 8 · Play — Full-Auto Mode *(BanG Dream only)*

The app takes over completely: it opens the game, navigates through menus, identifies the song via OCR, selects the difficulty, and enters the live screen — all without any input from you. Combine with **Auto Trigger** for a fully hands-free loop.

1. In **Song Setup**, enable the **Auto Mode** toggle. It turns green when active.
2. Select the correct **Game Server** region from the dropdown (JP / EN / TW / KR / CN). This determines which OCR language model is used to read song titles on screen.
3. *(Recommended)* Also enable **Auto Trigger** and complete its calibration (see §7 above), so the start command fires automatically once the pipeline hands off to the live screen.
4. Click **▶ Load & Prepare**. The app immediately begins the automation pipeline:
   - Launches BanG Dream and waits through the title screen.
   - Navigates to song select and uses OCR to find the target song.
   - Selects the configured difficulty and enters the live screen.
   - Hands off to the playback engine (Auto Trigger fires if enabled).
5. After the song ends, the pipeline loops back and plays the next song automatically.
6. To stop at any time, click **■ Stop** on the Play Control page.

---

### 8 · Timing Calibration

If notes are consistently early or late, adjust the **Timing Offset** on the **Play Control** page:

- Click **＋** to delay taps (hit later) or **－** to advance them (hit earlier).
- Keyboard shortcut: `↑` / `↓` arrow keys while on the Play Control page.
- Each step = **5 ms**. A value of `+10` means all taps fire 10 ms later than the chart timing.

Start with small adjustments (±10 ms) and refine from there.

---

##  Credits

* **Core play logic & chart parsing** — [kvarenzn/ssm](https://github.com/kvarenzn/ssm)
* **Web GUI shell** — [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui)
* **Touch calibration method** — [juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus)
* **Automation framework** — [MaaXYZ/MaaFramework](https://github.com/MaaXYZ/MaaFramework)
* Licensed under **GPL-3.0-or-later**

---

##  Disclaimer

> [!IMPORTANT]
> **This project is developed for personal learning and research purposes only.**
> 
> * **Non-Affiliation**: This is an independent third-party tool and is not affiliated with any game developer or publisher.
> * **Risk of Use**: Use of this software may violate game service terms, potentially leading to account suspension or bans.
> * **Limitation of Liability**: The author assumes no responsibility for any consequences resulting from the use of this project.

### 🎥 Demonstration
