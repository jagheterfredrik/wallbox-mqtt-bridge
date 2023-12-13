"""MQTT bridge.

Polls the local database and publishes changes to an external MQTT broker for Home Assistant.
Accepts changes from Home Assistant and updates the local database.
Supports Home Assistant discovery.
"""
import configparser
import ctypes
import json
import os
import re
import time
from typing import Any, Dict  # noqa: F401

import paho.mqtt.client as mqtt
import pymysql.cursors
import redis

wallbox_status_codes = [
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


def redis_get(name, key):
    result = redis_connection.hget(name, key)
    assert result, name + "." + key + " not found in redis"
    return result


# Below, we're using arm64 syscalls to interact with Posix message queues
# sysall 274 is mq_open(name, oflag, mode, attr)
# sysall 276 is mq_timedsend(mqdes, msg_ptr, msg_len, msg_prio, abs_timeout)
libc = ctypes.CDLL(None)
syscall = libc.syscall


def pause_resume(val):
    proposed_state = int(val)
    current_state = sql_execute("SELECT `charging_enable` FROM wallbox_config;")["charging_enable"]
    if proposed_state == current_state:
        return

    mq = syscall(274, b"WALLBOX_MYWALLBOX_WALLBOX_STATEMACHINE", 0x2, 0x1C7, None)
    if mq < 0:
        return
    if proposed_state == 1:
        syscall(276, mq, b"EVENT_REQUEST_USER_ACTION#1.000000".ljust(1024, b"\x00"), 1024, 0, None)
    elif proposed_state == 0:
        syscall(276, mq, b"EVENT_REQUEST_USER_ACTION#2.000000".ljust(1024, b"\x00"), 1024, 0, None)
    os.close(mq)


# Needed for unlock
wallbox_uid = sql_execute("SELECT `user_id` FROM `users` WHERE `user_id` != 1 ORDER BY `user_id` DESC LIMIT 1;")[
    "user_id"
]


def lock_unlock(val):
    proposed_state = int(val)
    current_state = sql_execute("SELECT `lock` FROM wallbox_config;")["lock"]
    if proposed_state == current_state:
        return

    mq = syscall(274, b"WALLBOX_MYWALLBOX_WALLBOX_LOGIN", 0x2, 0x1C7, None)
    if mq < 0:
        return
    if proposed_state == 1:
        syscall(276, mq, b"EVENT_REQUEST_LOCK".ljust(1024, b"\x00"), 1024, 0, None)
    elif proposed_state == 0:
        syscall(276, mq, (b"EVENT_REQUEST_LOGIN#%d.000000" % wallbox_uid).ljust(1024, b"\x00"), 1024, 0, None)
    os.close(mq)


ENTITIES_CONFIG = {
    "charging_enable": {
        "component": "switch",
        "setter": pause_resume,
        "config": {
            "name": "Charging enable",
            "payload_on": 1,
            "payload_off": 0,
            "command_topic": "~/set",
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
            "command_topic": "~/set",
        },
    },
    "max_charging_current": {
        "component": "number",
        "setter": lambda val: sql_execute("UPDATE `wallbox_config` SET `max_charging_current`=%s;", val),
        "config": {
            "name": "Max charging current",
            "command_topic": "~/set",
            "min": 6,
            "max": 40,
            "unit_of_measurement": "A",
            "device_class": "current",
        },
    },
    "cable_connected": {
        "component": "binary_sensor",
        "getter": lambda: int(redis_get("m2w", "tms.charger_status")) not in (0, 6),
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
        "getter": lambda: float(redis_get("m2w", "tms.line1.power_watt.value"))
        + float(redis_get("m2w", "tms.line2.power_watt.value"))
        + float(redis_get("m2w", "tms.line3.power_watt.value")),
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
        "getter": lambda: wallbox_status_codes[int(redis_get("m2w", "tms.charger_status"))],
        "config": {
            "name": "Status",
        },
    },
    "added_energy": {
        "component": "sensor",
        "getter": lambda: float(redis_get("state", "scheduleEnergy")),
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
    mqtt_host = config["mqtt"]["host"]
    mqtt_port = int(config["mqtt"]["port"])
    mqtt_username = config["mqtt"]["username"]
    mqtt_password = config["mqtt"]["password"]
    polling_interval_seconds = float(config["settings"]["polling_interval_seconds"])
    device_name = config["settings"]["device_name"]

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

    def _on_connect(client, userdata, flags, rc):
        print("Connected to MQTT with", rc)
        if rc == mqtt.MQTT_ERR_SUCCESS:
            mqttc.subscribe(set_topic)
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
                }
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
                print("Setting:", field, message.payload.decode())
                ENTITIES_CONFIG[field]["setter"](message.payload)
            else:
                print("Setting unsupported for field", field)

    mqttc.on_connect = _on_connect
    mqttc.on_message = _on_message
    mqttc.username_pw_set(mqtt_username, mqtt_password)
    print("Connecting to MQTT", mqtt_host, mqtt_port)
    mqttc.connect_async(mqtt_host, mqtt_port)
    mqttc.loop_start()

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
                    print("Publishing:", key, val)
                    mqttc.publish(topic_prefix + "/" + key + "/state", val, retain=True)
                    published[key] = val
            if publish_rate_limited:
                latest_rate_limit_publish = time.time()
        time.sleep(polling_interval_seconds)

finally:
    connection.close()
    redis_connection.close()
    mqttc.loop_stop()
