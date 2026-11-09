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

### Charging schedules (optional, `schedules_enabled = true`)

The charger exposes a fixed set of 4 schedule slots. Entities are created for every slot (1–4). There is no true create/delete: a slot is "created" by enabling it with values, and "deleted"/parked by disabling it. All schedule writes are applied live and persisted by the charger's own `schedule_manager` (via a Redis command), and the entity states are read back from the charger every poll, so changes made in the Wallbox app are reflected too.

To keep the device page uncluttered, **all entities for slots 2–4 are disabled by default** in Home Assistant (`enabled_by_default: false`) — that includes the enable switch, start/stop times and all 7 weekday switches. If you need an extra slot, enable its entities under **Settings → Devices & Services → Wallbox → Entities** (filter by "disabled"), then configure it like slot 1. Slot 1 is fully enabled out of the box.

Per slot (`N` = 1–4):

| Entity | Type | Writable | Notes |
|---|---|---|---|
| Schedule N Enable | switch | ✓ | Turn the slot on/off |
| Schedule N Start | time | ✓ | Local wall-clock start (HH:MM). **Create-on-entry:** if the slot is currently off, setting the start auto-fills stop = start + 1 min (wraps midnight), enables all 7 days and turns the slot on. If the slot is already on, only the start changes. |
| Schedule N Stop | time | ✓ | Local wall-clock stop (HH:MM) |
| Schedule N — 1 Monday … 7 Sunday | switch (×7) | ✓ | One per weekday (`config` category); named with a 1–7 prefix so they sort Mon→Sun |

There is no separate delete button — turning **Schedule N Enable** off parks the slot (the charger has no true delete; all other fields are kept).

Times are **local wall-clock** values — no timezone conversion (e.g. `09:00` → 0900, `22:05` → 2205). Max charging current is not per-slot; the slots use the charger's configured max (the existing **Max charging current** entity).

#### Atomic `set_schedule` command (for automations)

A single **Set schedule** `text` entity accepts a JSON payload that fully specifies one slot in one write — handy for Home Assistant automations:

```json
{"slot":1,"enable":true,"start":"2029","stop":"2100","days":["mon","tue","wed","thu","fri"],"max_current":-1,"name":""}
```

| Field | Required | Format / default |
|---|---|---|
| `slot` | yes | 1–4 |
| `start` | yes | `"HHMM"` or `"HH:MM"` (local) |
| `stop` | yes | `"HHMM"` or `"HH:MM"` (local) |
| `days` | yes | list of `mon`..`sun` (case-insensitive) and/or keywords `all`, `weekdays`, `weekends`, `none` |
| `enable` | no | defaults to `true` |
| `max_current` | no | defaults to `-1` (use the charger's configured max) |
| `name` | no | defaults to the slot's current name |

Malformed JSON, an out-of-range slot, a bad time, or an unrecognised day is logged as a warning and ignored — no command is sent.

The entity lives in the device's **Configuration** section (`entity_category: config`) so it stays out of the main controls card, and its entity ID is `text.set_schedule` (verify under the entity's settings). Example automation:

```yaml
automation:
  - alias: Weekday charging schedule
    trigger:
      - platform: time
        at: "06:00:00"
    action:
      - service: text.set_value
        target:
          entity_id: text.set_schedule
        data:
          value: '{"slot":1,"enable":true,"start":"06:30","stop":"09:00","days":["weekdays"]}'
```

Individual slots can also be driven from automations without JSON via the per-slot entities (e.g. `switch.schedule_1_enable`, `time.schedule_1_start` — check the entity IDs under the device page).

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
schedules_enabled        = false
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
