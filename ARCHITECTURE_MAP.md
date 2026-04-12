# SSM GUI Architecture Map

This document is a practical index for quickly finding where to read or change code.
It reflects the current repository state, including the song-detect refactor that moved logic into `songdetect/`.

## 1. What This Project Does

`ssm-gui` is a Go application for automated mobile rhythm gameplay.

- GUI mode (default when no CLI args): embedded web UI + local HTTP API + SSE state updates.
- CLI mode: terminal/TUI flow for manual run.
- Supports two game modes: `bang` and `pjsk`.
- Supports two control backends: `adb` (scrcpy control socket) and `hid` (USB accessory HID).

Core capabilities:

- Parse chart files (`.txt` BMS/SUS) and generate touch event timelines.
- Convert touch timelines into backend-specific wire data (scrcpy or HID).
- Auto-navigation before gameplay via MaaFramework pipeline.
- OCR-assisted song detection and scene checks.
- Optional visual first-note trigger from decoded video frames.

## 2. Primary Entry Points

## 2.1 App Startup

- `main.go`
  - `main()` parses flags and chooses GUI mode vs CLI mode.
  - `runGUI(conf)` is the main orchestrator for the web flow.
  - CLI path still exists for original TUI/backend flow.

## 2.2 Browser Launch Helpers

- `openbrowser_windows.go`
- `openbrowser_unix.go`

These only open the local GUI URL after server startup.

## 2.3 Build Entrypoints

- `build.bat`
  - builds frontend (`npm --prefix gui/frontend run build`)
  - then builds Go binary (`go build ... -o ssm-gui.exe`)

## 3. End-to-End Runtime Flows

## 3.1 GUI Flow (Normal)

1. Frontend sends `POST /api/run`.
2. `gui.Server.handleRun` decodes `RunRequest` and calls `OnRunRequest`.
3. In `runGUI`, `OnRunRequest` points to `runOnce(req)` in `main.go`.
4. `runOnce` prepares device/backend, chart, generated events, and now-playing metadata.
5. `srv.SetReady(...)` puts server into `StateReady`.
6. User clicks Start, frontend sends `POST /api/start`, server receives `TriggerStart()`.
7. `srv.WaitForStart(ctx)` transitions to `StatePlaying`.
8. `srv.Autoplay(ctx, start)` sends events through selected controller.
9. On completion, state goes `Playing -> Done -> Idle`.

## 3.2 GUI Auto Mode (Auto Navigation + Auto Detect Song)

1. Frontend `onAutoModeChanged()` enforces auto-related toggles.
2. `submitRun()` includes `autoNavigation`, `autoDetectSong`, ROI config, and jitter/advanced params.
3. Backend `runOnce` (in order):
   - optional MAA navigator init (`maacontrol.NewNavigator`)
   - optional OCR screen check + song detection (`songdetect.DetectByModeTextsDetailed`)
   - chart parse + event generation
   - `SetReady`
   - if `AutoNavigation`, call `TriggerStart()` immediately
   - `WaitForStart`
   - if `AutoNavigation`, execute `nav.Run(ctx, mode, diff)`
   - if `AutoTriggerVision`, run first-note visual trigger loop
   - `Autoplay`

This is the one-click load/start path for unattended play.

## 3.3 CLI Flow

`main.go` still supports non-GUI mode:

- load config/database
- parse chart (`scores.ParseBMS` or `scores.ParseSUS`)
- generate events
- pick backend (`adb` or `hid`)
- play via TUI loop

## 4. Package Map (Who Owns What)

## 4.1 GUI and HTTP Surface

- `gui/server.go`
  - API handlers, SSE broadcast, playback state machine.
  - `RunRequest`, `NowPlaying`, `AutoTriggerDebug` live here.
  - Key methods: `SetReady`, `WaitForStart`, `Autoplay`, `TriggerStart`, `SetError`.

- `gui/device_picker.go`
  - shared ADB device picker for frame fallback path.

- `gui/frontend/`
  - Vite app (single-page JS file currently at `src/main.js`).
  - Manages mode/backends/settings panel, ROI editor, logs, and SSE-driven UI refresh.

## 4.2 Runtime Orchestration

- `main.go`
  - top-level coordinator that binds GUI server callbacks to concrete runtime work.
  - owns backend selection, chart lookup, event generation, nav/OCR trigger ordering.

- `nav_ocr.go`
  - shared ROI defaults and coordinate helpers used by orchestration and navigator config.
  - difficulty tap coordinate strategy and OCR-assisted difficulty point lookup helpers.

## 4.3 Song Detection (Current Refactored Home)

- `songdetect/matcher.go`
  - generic text scoring/ranking against candidates.
  - key: `RankByTexts`, `DetectByTextsDetailed`.

- `songdetect/mode_detect.go`
  - mode-aware candidate loading and detection wrappers.
  - key: `DetectByModeTextsDetailed`, `DetectByModeTexts`, `DetectByModeText`.

- `songdetect/scene_check.go`
  - scene title checks and fuzzy keyword similarity for screen validation.
  - key: `SongSelectTitleScore`, `IsSongSelectTitle`, `NormalizeSceneTexts`, `FormatMatchCandidates`.

Notes:

- Former mixed logic file `gui/song_detect.go` is removed.
- Runtime now calls `songdetect` directly from `main.go`.

## 4.4 Auto Navigation (MAA)

- `maacontrol/nav.go`
  - wraps MaaFramework controller/resource/tasker lifecycle.
  - registers custom recognitions/actions.
  - executes pipeline entry task (`Nav`).

- `maacontrol/resource/pipeline/`
  - declarative state machine split into:
    - `main.json` (root jump)
    - `start.json` (layer jump)
    - `live.json` (main route)
    - `common.json` (interrupt handlers/buttons)

- `maacontrol/ocr_client.go`
  - OCR facade used by runtime/nav.

- `maacontrol/ocr_backend.go`
  - MaaFramework OCR backend implementation and model resolution.

## 4.5 Device Control and I/O

- `controllers/scrcpy.go`
  - scrcpy server bootstrap, sockets, touch message encoding, optional frame decode.
  - `LatestFrame()` feeds ROI debug and visual trigger logic.

- `controllers/hid.go`
  - USB accessory HID touch injection path.
  - converts normalized events into HID packets.

- `adb/`
  - in-repo ADB protocol client (`Client`, `Device`, sync push/pull, shell, forward).

## 4.6 Chart Parsing and Event Generation

- `scores/`
  - parsers: `ParseBMS`, `ParseSUS`
  - generator: `GenerateTouchEvent`
  - graph/coloring utilities used by slide/finger assignment logic.

- `stage/`
  - game-specific judge-line geometry (`BanGJudgeLinePos`, `PJSKJudgeLinePos`).

- `common/events.go`
  - shared touch event types and action normalization.

## 4.7 Databases and Metadata

- `db/bestdori.go`
  - BanG song/title/band metadata and jacket lookup.

- `db/sekai-world.go`
  - PJSK metadata and jacket lookup.

- `db/db.go`
  - DB interface + fetch/cache helpers.

- Root cache files:
  - `all.1.json`
  - `all.5.json`
  - `musics.json` and other fetched cache files (generated as needed)

## 4.8 Asset Extraction and Unity Decoding

- `extract.go`
  - high-level extraction pipeline from game asset bundles.

- `k/decrypt.go`
  - Sekai bundle decryption reader adapter.

- `uni/`
  - Unity bundle/serialized object readers and object model parsing.

- `decoders/`
  - texture decoders (ASTC/ETC/AV) used by extraction.

## 4.9 Infra Utilities

- `config/config.go`: device config load/save and lookup.
- `log/log.go`: logging wrappers and debug gate.
- `locale/`: localized message helpers.
- `term/`: terminal rendering/input helpers (used by CLI path).
- `utils/`: generic data structures and helpers.
- `optional/`: tiny generic optional wrapper.

## 5. HTTP API Map

Implemented in `gui/server.go`.

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/api/events` | GET | SSE stream of state, now playing, debug telemetry |
| `/api/status` | GET | snapshot state JSON |
| `/api/run` | POST | submit run config and prepare playback |
| `/api/start` | POST | trigger start when ready |
| `/api/offset` | POST | runtime timing offset delta |
| `/api/stop` | POST | stop current flow and return to idle |
| `/api/device` | GET/POST/DELETE | device config CRUD |
| `/api/extract` | POST | trigger asset extraction |
| `/api/songdb` | GET | proxy/load song metadata for selected mode |
| `/api/kill-adb` | POST | kill adb server process |
| `/api/detect-adb` | GET | detect first authorized adb device |
| `/api/vision-roi.png` | GET | cropped grayscale ROI preview for visual trigger |
| `/api/nav-roi.png` | GET | cropped grayscale ROI preview for nav song panel |
| `/api/frame.png` | GET | full frame preview (scrcpy or ADB screenshot fallback) |

## 6. Frontend Responsibilities (Single File Today)

Main UI code lives in `gui/frontend/src/main.js`.

Key clusters:

- i18n and theme switching.
- device picker and settings.
- ROI editor (load frame, drag ROI, apply/copy values).
- song search/dropdown and manual ID selection.
- run submission and start/stop controls.
- SSE consumption and UI/state/log synchronization.

If this file grows further, split by feature domain first:

- `state/` + `api/` + `roi-editor/` + `song-search/` + `playback/` + `i18n/`.

## 7. "Where Do I Change X?" Quick Index

## 7.1 Auto flow behavior (load/start/nav order)

- `main.go` inside `runGUI -> runOnce`.
- `gui/server.go` for state transitions and request handling.
- `gui/frontend/src/main.js` for what request body gets sent.

## 7.2 Song match quality and thresholds

- matching algorithm: `songdetect/matcher.go`
- mode candidate loading: `songdetect/mode_detect.go`
- scene keyword checks: `songdetect/scene_check.go`

## 7.3 MAA pre-game route logic

- state machine: `maacontrol/resource/pipeline/*.json`
- custom recognitions/actions: `maacontrol/nav.go`
- stage telemetry to frontend: `main.go` (`OnProgress` callback wiring)

## 7.4 ROI defaults and ROI tool behavior

- frontend target registry/defaults: `gui/frontend/src/main.js` (`ROI_EDITOR_TARGETS`)
- backend normalization/preview crop: `gui/server.go`
- runtime coordinate helpers: `nav_ocr.go`

## 7.5 Device transport issues

- ADB connection/commands: `adb/`
- scrcpy socket and control packets: `controllers/scrcpy.go`
- HID path: `controllers/hid.go`

## 7.6 Chart parsing and touch generation

- parse logic: `scores/bms.go`, `scores/sus.go`
- event generation/jitter shaping: `scores/generate.go`
- judge-line mapping: `stage/`

## 7.7 Extraction/decode failures

- extraction pipeline: `extract.go`
- Unity object parsing: `uni/`
- codec decode support: `decoders/`
- Sekai decryption: `k/decrypt.go`

## 8. Data and Resource Layout Notes

- Embedded web assets are served from `gui/frontend/dist` via `go:embed` in `gui/server.go`.
- MAA navigation templates and pipeline json live under `maacontrol/resource/`.
- Extracted gameplay assets expected under:
  - BanG: `assets/star/forassetbundle/startapp/...`
  - PJSK: `assets/sekai/assetbundle/resources/startapp/...`

## 9. OCR Stack Notes

Active runtime path uses MAA OCR facade:

- `maacontrol.GetOCRClient()`
- backend in `maacontrol/ocr_backend.go`

There is also a local go-ocr implementation in root `ocr_client.go` (same main package), which is currently not the primary path used by `runGUI`.

## 10. Suggested Next Structural Refactors

If you continue cleanup, highest ROI items are:

1. Split `main.go` `runOnce` into feature-focused files (`run_prepare.go`, `run_nav.go`, `run_detect.go`, `run_autoplay.go`).
2. Split frontend `src/main.js` by domain to reduce merge/conflict risk.
3. Move duplicated ROI normalization helpers into one shared utility package.
4. Decide whether root `ocr_client.go` should remain as fallback or be retired.
