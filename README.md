# MQTT Bridge for Wallbox

This open-source project connects your Wallbox charger fully locally to Home Assistant, providing unparalleled speed and reliability.

## Features

- **Real-time sensor data:** The Wallbox's internal state is polled every second (configurable), and any changes are immediately pushed to your MQTT broker. Live Redis pub/sub notifications ensure instant updates without waiting for the next poll cycle.

- **Local control:** Lock/unlock, pause/resume charging, adjust max charging current, and set halo brightness — all without involving the manufacturer's servers.

- **Resilient connection:** The bridge automatically retries on startup if the MQTT broker is unavailable and reconnects seamlessly after any network interruption. All Home Assistant discovery configs and sensor states are re-published on every reconnect.

- **Home Assistant MQTT Auto Discovery:** Sensors, switches, locks, and number controls are automatically registered in Home Assistant. No manual entity configuration required.

- **Optional Power Boost support:** Enable per-phase power, current, and voltage sensors for Wallbox Power Boost installations.

<br/>
<p align="center">
   <img src="https://github.com/jagheterfredrik/wallbox-mqtt-bridge/assets/9987465/06488a5d-e6fe-4491-b11d-e7176792a7f5" height="507" />
</p>

## Entities

### Always present

| Entity | Type | Writable |
|---|---|---|
| Status | sensor | — |
| Charging power (total + L1/L2/L3) | sensor | — |
| Charging current (L1/L2/L3) | sensor | — |
| Voltage (L1/L2/L3) | sensor | — |
| Added energy (session) | sensor | — |
| Cumulative added energy | sensor | — |
| Added range | sensor | — |
| Cable connected | binary_sensor | — |
| Temperature (L1/L2/L3) | sensor | — |
| Charging enable | switch | ✓ |
| Lock | lock | ✓ |
| Max charging current | number (6 A – max) | ✓ |
| Halo brightness | number (0–100 %) | ✓ |

### Power Boost (optional, `power_boost_enabled = true`)

| Entity | Type |
|---|---|
| Power Boost power (L1/L2/L3) | sensor |
| Power Boost current (L1/L2/L3) | sensor |
| Power Boost voltage (L1/L2/L3) | sensor |
| Power Boost cumulative energy | sensor |
| Power Boost meter status | sensor |

### Debug sensors (optional, `debug_sensors = true`)

| Entity | Type |
|---|---|
| State machine state | sensor |
| Control pilot | sensor |
| M2W status | sensor |
| S2 open | sensor |

## Getting Started

1. [Root your Wallbox](https://github.com/jagheterfredrik/wallbox-pwn)
2. Set up an MQTT broker if you don't already have one. Here's an example of [installing it as a Home Assistant add-on](https://www.youtube.com/watch?v=dqTn-Gk4Qeo).
3. SSH to your Wallbox and run:

```sh
curl -sSfL https://github.com/jagheterfredrik/wallbox-mqtt-bridge/releases/download/bridge/install.sh > install.sh && bash install.sh
```

The installer will download the correct binary for your architecture (`armhf` or `arm64`), prompt you to create a configuration file on first run, install the bridge as a systemd service, and start it automatically.

> **Upgrading:** run the same command again to upgrade to the latest version. Your `bridge.ini` configuration file is preserved.

## Configuration

The bridge is configured via `~/mqtt-bridge/bridge.ini`. To create or reconfigure it interactively, run:

```sh
cd ~/mqtt-bridge && ./bridge --config
```

### Configuration reference

```ini
[mqtt]
# Broker address. Use `url` for a full URI (e.g. mqtts://host:8883),
# or `host`+`port` for a plain TCP connection.
url      =
host     = 127.0.0.1
port     = 1883
username =
password =

[settings]
polling_interval_seconds = 1
device_name              = Wallbox
debug_sensors            = false
power_boost_enabled      = false
```

### MQTT topic layout

All topics are prefixed with `wallbox_<serial_number>/`.

| Topic | Description |
|---|---|
| `wallbox_<serial>/availability` | `online` / `offline` (retained LWT) |
| `wallbox_<serial>/<entity>/state` | Current value (retained) |
| `wallbox_<serial>/<entity>/set` | Write a new value |

Home Assistant discovery configs are published to `homeassistant/<component>/<serial>_<entity>/config`.

## Acknowledgments

A big shoutout to [@tronikos](https://github.com/tronikos) for their valuable contributions. This project wouldn't be the same without the collaborative spirit of the open-source community.
