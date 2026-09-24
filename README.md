# bleprox

> A lightweight Bluetooth Low Energy (BLE) proximity daemon and monitoring tool for Linux to automatically lock and unlock your session based on RSSI strength.

`bleprox` monitors the RSSI (Received Signal Strength Indicator) of any nearby BLE device (smartwatch, fitness tracker, phone, or BLE beacon) and triggers `loginctl lock-session` / `loginctl unlock-session` as you step away from or return to your computer.

---

## Features

- 🔒 **Automatic Lock & Unlock**: Uses `loginctl` to lock/unlock your Linux desktop session seamlessly.
- 📈 **EMA RSSI Smoothing**: Uses Exponential Moving Average filtering ($\alpha = 0.2$) to reduce signal noise and eliminate false triggers.
- ⚡ **Hysteresis Gap**: Configurable lock (`-lock-rssi`) and unlock (`-unlock-rssi`) thresholds to prevent rapid state toggling at boundary distances.
- ⏱️ **Signal Timeout Protection**: Automatically locks the session if BLE advertisement packets stop arriving for a specified duration (`-timeout`).
- 🔍 **Interactive Monitor Mode**: Discover nearby BLE devices, observe real-time RSSI signal strength bars, and receive auto-calculated threshold recommendations upon exit.
- ⚙️ **Systemd Integration**: Runs clean in the background as a systemd user service (`systemctl --user`), with quiet logging mode (`-quiet`) to prevent journald spam.

---

## Requirements

- **OS**: Linux with BlueZ and D-Bus active.
- **Session Manager**: `loginctl` (systemd-logind or elogind).
- **Go**: Version 1.20+ (to build from source).
- **Permissions**: User must have permission to access the Bluetooth adapter via D-Bus / BlueZ.

---

## Quick Start

### 1. Build and Install

Clone the repository and build using `make`:

```bash
git clone https://github.com/sehari24jam/bleprox.git
cd bleprox
make build
make install
```

This builds `bleprox` and installs the binary to `/usr/local/bin/bleprox`.

---

### 2. Discover Devices & Measure RSSI

Run `bleprox` in **Monitor Mode** to scan nearby BLE devices:

```bash
bleprox -monitor
# or via make:
make monitor
```

To monitor your specific device MAC address and get recommended daemon settings:

```bash
bleprox -monitor -mac AA:BB:CC:DD:EE:FF
```

Press `Ctrl+C` when finished. `bleprox` will output a summary and recommended parameters:

```text
===============================================================
               BLE PROXIMITY MONITOR SUMMARY                   
===============================================================
Total Monitor Duration : 45s
Target MAC             : AA:BB:CC:DD:EE:FF (Smartwatch)
Total Advertisements   : 142 packets
RSSI Min               : -88 dBm
RSSI Max               : -62 dBm
RSSI Average           : -71.4 dBm
Last Smoothed RSSI     : -65.0 dBm

RECOMMENDED DAEMON COMMAND:
  bleprox -mac AA:BB:CC:DD:EE:FF -lock-rssi -81.4 -unlock-rssi -66.4 -timeout 10
===============================================================
```

---

### 3. Run Daemon Mode

Run the proximity daemon manually with your chosen thresholds:

```bash
bleprox -mac AA:BB:CC:DD:EE:FF -lock-rssi -85 -unlock-rssi -70 -timeout 15
```

---

## Systemd User Service

Run `bleprox` continuously in the background as a systemd user service.

1. **Install Service**:
   ```bash
   make install-service MAC=AA:BB:CC:DD:EE:FF
   ```

2. **Enable & Start Service**:
   ```bash
   systemctl --user enable --now bleprox
   ```

3. **Check Status & Logs**:
   ```bash
   systemctl --user status bleprox
   journalctl --user -u bleprox -f
   ```

---

## Command-Line Options

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-mac <string>` | `""` | Target BLE device MAC address (e.g. `AA:BB:CC:DD:EE:FF`). *(Required in daemon mode)* |
| `-lock-rssi <float>` | `-85.0` | RSSI threshold in dBm below which session locks. |
| `-unlock-rssi <float>` | `-70.0` | RSSI threshold in dBm above which session unlocks. |
| `-timeout <int>` | `15` | Seconds without advertisement signal before triggering lock. |
| `-quiet` | `false` | Quiet mode: reduces journald log volume by only logging state changes and periodic status updates. |
| `-log-interval <duration>` | `1m` | Periodic status logging interval in quiet mode (e.g. `30s`, `1m`). |
| `-monitor` | `false` | Run as interactive monitoring and device discovery tool. |
| `-interval <duration>` | `1s` | Monitoring report interval in monitor mode (e.g. `500ms`, `1s`). |
| `-v`, `-version` | `false` | Show version information and exit. |

---

## Makefile Commands

| Command | Description |
| :--- | :--- |
| `make build` | Compiles the `bleprox` binary with version information. |
| `make install` | Installs `bleprox` binary to `/usr/local/bin`. |
| `make install-service MAC=...` | Configures and installs the systemd user service. |
| `make run MAC=...` | Runs daemon mode directly via `go run`. |
| `make monitor [MAC=...]` | Runs interactive monitor mode. |
| `make test` | Runs unit tests. |
| `make clean` | Removes built binary. |

---

## How It Works

1. **BLE Advertisement Scanning**: `bleprox` uses `tinygo.org/x/bluetooth` to scan passive BLE advertisement packets from your target device.
2. **Signal Filtering**: Raw RSSI values are smoothed using an Exponential Moving Average (EMA):
   $$\text{Smoothed RSSI} = (0.2 \times \text{Raw RSSI}) + (0.8 \times \text{Smoothed RSSI}_{\text{prev}})$$
3. **Session Lock**: Triggers `loginctl lock-session` when `Smoothed RSSI` falls below `-lock-rssi` OR when no packet is received for longer than `-timeout` seconds.
4. **Session Unlock**: Triggers `loginctl unlock-session` when `Smoothed RSSI` rises above `-unlock-rssi`.

---

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](file:///home/yulianto/gofiles/src/github.com/sehari24jam/bleprox/LICENSE) file for details.