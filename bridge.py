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

redis_connection = redis.Redis(host='localhost', port=6379, db=0)

def sql_execute(sql, *args):
    with connection.cursor() as cursor:
        cursor.execute(sql, args)
        return cursor.fetchone()

libc = ctypes.CDLL(None)
syscall = libc.syscall

def pause_resume(val):
    # mq_open()
    mq = syscall(274, b'WALLBOX_MYWALLBOX_WALLBOX_STATEMACHINE', 0x2, 0x1c7)
    if mq < 0:
        return
    if val == 1:
        syscall(276, mq, b'EVENT_REQUEST_USER_ACTION#1.000000'.ljust(1024, b'\x00'), 1024, 0, None)
    else:
        syscall(276, mq, b'EVENT_REQUEST_USER_ACTION#2.000000'.ljust(1024, b'\x00'), 1024, 0, None)
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
        "setter": lambda val: sql_execute("UPDATE `wallbox_config` SET `lock`=%s;", val),
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
        "getter": lambda: wallbox_status_codes[int(redis_connection.hget("m2w", "tms.charger_status"))],
        "config": {
            "name": "Status",
        },
    },
    "added_energy": {
        "component": "sensor",
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
}

DB_QUERY = """
SELECT
  `wallbox_config`.`charging_enable`,
  `wallbox_config`.`lock`,
  `wallbox_config`.`max_charging_current`,
  `active_session`.`was_connected` AS cable_connected,
  `latest_state_value`.`ac_current_rms_l1` / 10.0 * `latest_state_value`.`ac_voltage_rms_l1`
    + `latest_state_value`.`ac_current_rms_l2` / 10.0 * `latest_state_value`.`ac_voltage_rms_l2`
    + `latest_state_value`.`ac_current_rms_l2` / 10.0 * `latest_state_value`.`ac_voltage_rms_l3` AS charging_power,
  `power_outage_values`.`charged_energy` AS cumulative_added_energy,
  IF(`active_session`.`id` != 1,
    `power_outage_values`.`charged_energy` - `active_session`.`start_charging_energy_tms`,
    `latest_session`.`energy_total`) AS added_energy,
  IF(`active_session`.`id` != 1,
    `active_session`.`charged_range`,
    `latest_session`.`charged_range`) AS added_range
FROM `wallbox_config`,
  `active_session`,
  `power_outage_values`,
  (SELECT * FROM `session` ORDER BY `id` DESC LIMIT 1) AS latest_session,
  (SELECT * FROM `state_values` ORDER BY `id` DESC LIMIT 1) AS latest_state_value;
"""

UPDATEABLE_WALLBOX_CONFIG_FIELDS = ["charging_enable", "lock", "max_charging_current"]

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

    with connection.cursor() as cursor:
        # Prepare the MQTT topic name to include the serial number of the Wallbox
        cursor.execute("SELECT `serial_num` FROM `charger_info`;")
        result = cursor.fetchone()
        assert result
        serial_num = str(result["serial_num"])

        # Set max available current
        cursor.execute("SELECT `max_avbl_current` FROM `state_values` ORDER BY `id` DESC LIMIT 1;")
        result = cursor.fetchone()
        assert result
        max_avbl_current = str(result["max_avbl_current"])
        ENTITIES_CONFIG["max_charging_current"]["config"]["max"] = int(max_avbl_current)


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
                print("Setting:", field, message.payload)
                ENTITIES_CONFIG[field]["setter"](message.payload)
            else:
                print("Setting unsupported:", field, message.payload)

    mqttc.on_connect = _on_connect
    mqttc.on_message = _on_message
    mqttc.username_pw_set(mqtt_username, mqtt_password)
    print("Connecting to MQTT", mqtt_host, mqtt_port)
    mqttc.connect_async(mqtt_host, mqtt_port)
    mqttc.loop_start()

    published = {}
    while True:
        if mqttc.is_connected():
            with connection.cursor() as cursor:
                cursor.execute(DB_QUERY)
                result = cursor.fetchone()
                assert result
            for k, v in ENTITIES_CONFIG.items():
                if "getter" in v:
                    result[k] = v["getter"]()
            for key, val in result.items():
                if published.get(key) != val:
                    print("Publishing:", key, val)
                    mqttc.publish(topic_prefix + "/" + key + "/state", val, retain=True)
                    published[key] = val
        time.sleep(polling_interval_seconds)

finally:
    connection.close()
    mqttc.loop_stop()
