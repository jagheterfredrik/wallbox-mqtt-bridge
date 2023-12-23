"""MQTT bridge.

Polls the local database and publishes changes to an external MQTT broker for Home Assistant.
Accepts changes from Home Assistant and updates the local database.
Supports Home Assistant discovery.
"""
import configparser
import ctypes
import json
import logging
import os
import re
import time
from typing import Any, Dict  # noqa: F401

import paho.mqtt.client as mqtt
import pymysql.cursors
import redis

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("mqtt-bridge")

WALLBOX_STATUS_CODES = [
    "Ready",
    "Charging",
    "Connected waiting car",
    "Connected waiting schedule",
    "Paused",
    "Schedule end",
    "Locked",
    "Error",
    "Connected waiting current assignation",
    "Unconfigured power sharing",
    "Queue by power boost",
    "Discharging",
    "Connected waiting admin auth for mid",
    "Connected mid safety margin exceeded",
    "OCPP unavailable",
    "OCPP charge finishing",
    "OCPP reserved",
    "Updating",
    "Queue by eco smart",
]

STATE_OVERRIDES = {
    0xA1: 0,
    0xA2: 9,
    0xA3: 14,
    0xA4: 15,
    0xA6: 17,
    0xB1: 3,
    0xB2: 4,
    0xB3: 3,
    0xB4: 2,
    0xB5: 2,
    0xB6: 4,
    0xB7: 8,
    0xB8: 8,
    0xB9: 10,
    0xBA: 10,
    0xBB: 12,
    0xBC: 13,
    0xBD: 18,
    0xC1: 1,
    0xC2: 1,
    0xC3: 11,
    0xC4: 11,
    0xD1: 6,
    0xD2: 6,
}

connection = pymysql.connect(
    host="localhost",
    user="root",
    password="fJmExsJgmKV7cq8H",
    db="wallbox",
    charset="utf8mb4",
    cursorclass=pymysql.cursors.DictCursor,
)
# Because the transaction isolation is set to REPEATABLE-READ we need to commit after every write and read.
# If it was READ-COMMITTED this would only be needed after every write.
# Check the transaction isolation with:
# "SELECT @@GLOBAL.tx_isolation, @@tx_isolation;"
connection.autocommit(True)

redis_connection = redis.Redis(host="localhost", port=6379, db=0)


def sql_execute(sql, *args):
    with connection.cursor() as cursor:
        cursor.execute(sql, args)
        return cursor.fetchone()


def redis_hget(name, key):
    result = redis_connection.hget(name, key)
    assert result, name + " " + key + " not found in redis"
    return result


def redis_hmget(name, keys):
    result = redis_connection.hmget(name, keys)
    assert result
    return result


# Below, we're using arm64 syscalls to interact with Posix message queues
# sysall 274 is mq_open(name, oflag, mode, attr)
# sysall 276 is mq_timedsend(mqdes, msg_ptr, msg_len, msg_prio, abs_timeout)
libc = ctypes.CDLL(None)
syscall = libc.syscall


def send_to_posix_queue(name, msg):
    mq = syscall(274, name, 0x2, 0x1C7, None)
    if mq < 0:
        return
    syscall(276, mq, msg.ljust(1024, b"\x00"), 1024, 0, None)
    os.close(mq)


def pause_resume(val):
    proposed_state = int(val)
    current_state = sql_execute("SELECT `charging_enable` FROM wallbox_config;")["charging_enable"]
    if proposed_state == current_state:
        return

    if proposed_state == 1:
        send_to_posix_queue(b"WALLBOX_MYWALLBOX_WALLBOX_STATEMACHINE", b"EVENT_REQUEST_USER_ACTION#1.000000")
    elif proposed_state == 0:
        send_to_posix_queue(b"WALLBOX_MYWALLBOX_WALLBOX_STATEMACHINE", b"EVENT_REQUEST_USER_ACTION#2.000000")


# Needed for unlock
wallbox_uid = sql_execute("SELECT `user_id` FROM `users` WHERE `user_id` != 1 ORDER BY `user_id` DESC LIMIT 1;")[
    "user_id"
]


def lock_unlock(val):
    proposed_state = int(val)
    current_state = sql_execute("SELECT `lock` FROM wallbox_config;")["lock"]
    if proposed_state == current_state:
        return

    if proposed_state == 1:
        send_to_posix_queue(b"WALLBOX_MYWALLBOX_WALLBOX_LOGIN", b"EVENT_REQUEST_LOCK")
    elif proposed_state == 0:
        send_to_posix_queue(b"WALLBOX_MYWALLBOX_WALLBOX_LOGIN", (b"EVENT_REQUEST_LOGIN#%d.000000" % wallbox_uid))


# Applies some additional rules to the internal state and returns the status as a string
def effective_status_string():
    tms_status = int(redis_hget("m2w", "tms.charger_status"))
    state = int(redis_hget("state", "session.state"))
    if state in STATE_OVERRIDES:
        tms_status = STATE_OVERRIDES[state]
    return WALLBOX_STATUS_CODES[tms_status]


ENTITIES_CONFIG = {
    "charging_enable": {
        "component": "switch",
        "setter": pause_resume,
        "config": {
            "name": "Charging enable",
            "payload_on": 1,
            "payload_off": 0,
            "icon": "mdi:ev-station",
        },
    },
    "lock": {
        "component": "lock",
        "setter": lock_unlock,
        "config": {
            "name": "Lock",
            "payload_lock": 1,
            "payload_unlock": 0,
            "state_locked": 1,
            "state_unlocked": 0,
        },
    },
    "max_charging_current": {
        "component": "number",
        "setter": lambda val: sql_execute("UPDATE `wallbox_config` SET `max_charging_current`=%s;", val),
        "config": {
            "name": "Max charging current",
            "min": 6,
            "max": 40,
            "unit_of_measurement": "A",
            "device_class": "current",
        },
    },
    "halo_brightness": {
        "component": "number",
        "setter": lambda val: sql_execute("UPDATE `wallbox_config` SET `halo_brightness`=%s;", val),
        "config": {
            "name": "Halo Brightness",
            "min": 0,
            "max": 100,
            "icon": "mdi:brightness-percent",
            "unit_of_measurement": "%",
            "entity_category": "config",
        },
    },
    "cable_connected": {
        "component": "binary_sensor",
        "getter": lambda: int(int(redis_hget("m2w", "tms.charger_status")) not in (0, 6)),
        "config": {
            "name": "Cable connected",
            "payload_on": 1,
            "payload_off": 0,
            "icon": "mdi:ev-plug-type1",
            "device_class": "plug",
        },
    },
    "charging_power": {
        "component": "sensor",
        "getter": lambda: sum(
            float(v)
            for v in redis_hmget(
                "m2w", ["tms.line1.power_watt.value", "tms.line2.power_watt.value", "tms.line3.power_watt.value"]
            )
        ),
        "config": {
            "name": "Charging power",
            "device_class": "power",
            "unit_of_measurement": "W",
            "state_class": "total",
            "suggested_display_precision": 1,
        },
    },
    "status": {
        "component": "sensor",
        "getter": effective_status_string,
        "config": {
            "name": "Status",
        },
    },
    "added_energy": {
        "component": "sensor",
        "getter": lambda: float(redis_hget("state", "scheduleEnergy")),
        "config": {
            "name": "Added energy",
            "device_class": "energy",
            "unit_of_measurement": "Wh",
            "state_class": "total",
            "suggested_display_precision": 1,
        },
    },
    "cumulative_added_energy": {
        "component": "sensor",
        "config": {
            "name": "Cumulative added energy",
            "device_class": "energy",
            "unit_of_measurement": "Wh",
            "state_class": "total_increasing",
            "suggested_display_precision": 1,
        },
    },
    "added_range": {
        "component": "sensor",
        "config": {
            "name": "Added range",
            "device_class": "distance",
            "unit_of_measurement": "km",
            "state_class": "total",
            "suggested_display_precision": 1,
            "icon": "mdi:map-marker-distance",
        },
    },
}  # type: Dict[str, Dict[str, Any]]

DB_QUERY = """
SELECT
  `wallbox_config`.`charging_enable`,
  `wallbox_config`.`lock`,
  `wallbox_config`.`max_charging_current`,
  `wallbox_config`.`halo_brightness`,
  `power_outage_values`.`charged_energy` AS cumulative_added_energy,
  IF(`active_session`.`unique_id` != 0,
    `active_session`.`charged_range`,
    `latest_session`.`charged_range`) AS added_range
FROM `wallbox_config`,
    `active_session`,
    `power_outage_values`,
    (SELECT * FROM `session` ORDER BY `id` DESC LIMIT 1) AS latest_session;
"""

mqttc = mqtt.Client()

try:
    config = configparser.ConfigParser()
    config.read(os.path.join(os.path.dirname(__file__), "bridge.ini"))
    mqtt_host = config.get("mqtt", "host")
    mqtt_port = config.getint("mqtt", "port")
    mqtt_username = config.get("mqtt", "username")
    mqtt_password = config.get("mqtt", "password")
    polling_interval_seconds = config.getfloat("settings", "polling_interval_seconds")
    device_name = config.get("settings", "device_name")
    if config.getboolean("settings", "legacy_locking", fallback=False):
        ENTITIES_CONFIG["lock"]["setter"] = lambda val: sql_execute("UPDATE `wallbox_config` SET `lock`=%s;", val)

    # Prepare the MQTT topic name to include the serial number of the Wallbox
    result = sql_execute("SELECT `serial_num` FROM `charger_info`;")
    assert result
    serial_num = str(result["serial_num"])

    # Set max available current
    result = sql_execute("SELECT `max_avbl_current` FROM `state_values` ORDER BY `id` DESC LIMIT 1;")
    assert result
    ENTITIES_CONFIG["max_charging_current"]["config"]["max"] = result["max_avbl_current"]

    topic_prefix = "wallbox_" + serial_num
    set_topic = topic_prefix + "/+/set"
    set_topic_re = re.compile(topic_prefix + "/(.*)/set")
    availability_topic = topic_prefix + "/availability"

    def _on_connect(client, userdata, flags, rc):
        logger.info("Connected to MQTT with %d", rc)
        if rc == mqtt.MQTT_ERR_SUCCESS:
            mqttc.subscribe(set_topic)
            mqttc.publish(availability_topic, "online", retain=True)
            for k, v in ENTITIES_CONFIG.items():
                unique_id = serial_num + "_" + k
                component = v["component"]
                config = {
                    "~": topic_prefix + "/" + k,
                    "state_topic": "~/state",
                    "unique_id": unique_id,
                    "device": {
                        "identifiers": serial_num,
                        "name": device_name,
                    },
                    "availability_topic": availability_topic,
                }
                if "setter" in v:
                    config["command_topic"] = "~/set"
                config = {**v["config"], **config}
                mqttc.publish(
                    "homeassistant/" + component + "/" + unique_id + "/config",
                    json.dumps(config),
                    retain=True,
                )

    def _on_message(client, userdata, message):
        m = set_topic_re.match(message.topic)
        if m:
            field = m.group(1)
            if field in ENTITIES_CONFIG and "setter" in ENTITIES_CONFIG[field]:
                logger.info("Setting: %s %s", field, message.payload.decode())
                ENTITIES_CONFIG[field]["setter"](message.payload)
            else:
                logger.info("Setting unsupported for field %s", field)

    def on_disconnect(client, userdata, rc):
        if rc != 0:
            raise Exception("Disconnected")

    mqttc.on_disconnect = on_disconnect
    mqttc.on_connect = _on_connect
    mqttc.on_message = _on_message
    mqttc.username_pw_set(mqtt_username, mqtt_password)
    mqttc.will_set(availability_topic, "offline", retain=True)
    logger.info("Connecting to MQTT %s %s", mqtt_host, mqtt_port)
    mqttc.connect(mqtt_host, mqtt_port)

    published = {}  # type: Dict[str, Any]
    # If we change more than this, we publish even though we're rate limited
    rate_limit_deltas = {
        "charging_power": 100,
        "added_energy": 50,
    }
    rate_limit_s = 10.0
    latest_rate_limit_publish = 0.0
    while True:
        if mqttc.is_connected():
            result = sql_execute(DB_QUERY)
            assert result
            for k, v in ENTITIES_CONFIG.items():
                if "getter" in v:
                    result[k] = v["getter"]()
            publish_rate_limited = latest_rate_limit_publish + rate_limit_s < time.time()
            for key, val in result.items():
                if published.get(key) != val:
                    if key in rate_limit_deltas and not publish_rate_limited:
                        if abs(published.get(key, 0) - val) < rate_limit_deltas[key]:
                            continue
                    logger.info("Publishing: %s %s", key, val)
                    mqttc.publish(topic_prefix + "/" + key + "/state", val, retain=True)
                    published[key] = val
            if publish_rate_limited:
                latest_rate_limit_publish = time.time()
        mqttc.loop(timeout=polling_interval_seconds)

finally:
    # Intentionally not calling mqttc.disconnect() so that the broker sends the will (offline to the availability_topic)
    connection.close()
    redis_connection.close()
