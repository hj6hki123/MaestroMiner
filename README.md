<p align="center">
    <a href="https://github.com/hj6hki123/ssm-gui">
        <img src="imgs/page.png" alt="ssm-gui-banner"/>
    </a>
    <br>
    <strong>A Web-based GUI for automated mobile rhythm game playback and chart parsing.</strong>
</p>

##  Auto-Mining Branch 

This branch is all about turning you into a proper **lazy legend** — full automation so you can just sit back and let the game play itself.

Planned features:

- [x] **Auto first tap** (for us butterfingers out there)
- [ ] Delay compensation
- [ ] Auto song title detection
- [ ] Auto single-player song cycling / grinding
- [ ] Ensemble / co-op support
---
此分支致力于塑造一个解放双手的懒人，预计开发功能如下：

- [x] 自动打击第一下音符（手残党用）
- [ ] 延迟补偿
- [ ] 自动辨识歌名
- [ ] 自动单人轮巡打歌
- [ ] 支持协奏

虽然本人开始的初衷只是为了自己做一个简单易用的GUI，却抵不住码农的血液在流淌...

### Vision Auto Trigger Technical Specification

To enhance the stability of first-note auto-triggering on low-end devices, this version upgrades the trigger logic to a hybrid model of **"Adaptive Noise Threshold + Multi-region Temporal Flow"**. The system enters the `armed` state upon pressing Start and exits immediately upon pressing Stop or task cancellation to prevent background false triggers.

Engineering changes:

- Added sub-mode ROI (BanG Dream / PJSK) and polling cycle (poll ms) configurations.
- Upgraded low-end device adaptation from fixed thresholds to adaptive thresholds, reducing the impact of video decoding jitter.

The detection model (simplified) is as follows:

#### 1. Luma and Difference Definition
The single ROI is vertically divided into three segments to calculate the average luma:
* **Global Average Luma:** $L_t$
* **Segmented Luma:** $L_{t(top)}, L_{t(mid)}, L_{t(bottom)}$
* **Instantaneous Difference:**

$$\Delta L_t = L_t - L_{t-1}$$

$$\Delta d_{top} = L_{t(top)} - L_{t-1(top)}$$

$$\Delta d_{mid} = L_{t(mid)} - L_{t-1(mid)}$$

$$\Delta d_{bottom} = L_{t(bottom)} - L_{t-1(bottom)}$$

#### 2. Adaptive Noise Estimation
Exponential smoothing is used to dynamically track environmental and device thermal noise:

$$Noise_t = (1 - \alpha) \cdot Noise_{t-1} + \alpha \cdot \max\left(|\Delta L_t|, \frac{|\Delta d_{top}| + |\Delta d_{mid}| + |\Delta d_{bottom}|}{3}\right)$$

> $\alpha$ is the smoothing coefficient (typically $0.05 \sim 0.1$).

#### 3. Dynamic Threshold Calculation
Thresholds scale in real-time based on $Noise_t$ to ensure stability under high noise conditions on low-end devices:

$$Thr_{stable} = \max(Base_{stable}, 1.8 \cdot Noise_t)$$

$$Thr_{trigger} = \max(Base_{trigger}, 3.2 \cdot Noise_t)$$

$$Thr_{rise} = \max(0.9, 2.2 \cdot Noise_t)$$

---

### Detection Logic

The system utilizes a dual-track decision-making process; the trigger fires if either condition is met:

#### A. Flow Trigger — Optimized for Low-end Devices
Within a defined time window $W$, luma rise peaks must be observed sequentially, simulating the physical falling process:
1. **Step 1:** $\Delta d_{top} \ge Thr_{rise}$
2. **Step 2:** $\Delta d_{mid} \ge Thr_{rise}$
3. **Step 3:** $\Delta d_{bottom} \ge Thr_{rise}$
> **Advantage:** Filters out random noise effectively by leveraging spatial-temporal correlation.

#### B. Luma Backup — Fail-safe Mechanism
Activated when an object moves extremely fast (large frame skips), causing the flow to become discontinuous:
* **Trigger Condition:** Once the stable count reaches its requirement, if $\Delta L_t \ge Thr_{trigger}$, the trigger fires immediately.

### Demonstration
https://github.com/user-attachments/assets/09c6585a-64fb-44ad-af82-6239ee994b1b


## Disclaimer
> [!IMPORTANT]
> This version is a **development build** and is not recommended for general users. If you wish to use it, you must **compile it yourself**.


## 📜 License & Credits

* **Core Play Logic & Chart Parsing**: Credited to the original author [kvarenzn](https://github.com/kvarenzn/ssm).
* **Web GUI Implementation**: Custom integrated control panel developed specifically for this branch.
* This project is licensed under the **GPL-3.0-or-later** license.
