# MQTT bridge for Wallbox

Polls the Wallbox local database and publishes changes to an external MQTT broker for Home Assistant.
Accepts changes from Home Assistant and updates the Wallbox local database.
Supports Home Assistant discovery.

Big thanks for all the contributions from @tronikos

Once you set this up you will have local control of your Wallbox in Home Assistant via entities like this:

![image](https://github.com/jagheterfredrik/wallbox-mqtt-bridge/assets/9987465/06488a5d-e6fe-4491-b11d-e7176792a7f5)

## Instructions

1. Root your Wallbox following instructions at <https://github.com/jagheterfredrik/wallbox-pwn>
2. If you don't have a Mosquitto broker, install and setup the Mosquitto broker addon following instructions at <https://www.youtube.com/watch?v=dqTn-Gk4Qeo>
3. Download the contents of this directory.
4. Edit bridge.ini
   - Change `host` to point to the IP address of your MQTT broker
   - Change `username` and `password` to the correct ones for your MQTT broker
5. Copy the files to your Wallbox using `scp` (On Windows you can use WinSCP). You should end up with the following files in your Wallbox:
   - `/home/root/mqtt-bridge/bridge.ini`
   - `/home/root/mqtt-bridge/bridge.py`
   - `/home/root/mqtt-bridge/install.sh`
   - `/home/root/mqtt-bridge/mqtt-bridge.service`
   - `/home/root/mqtt-bridge/requirements.txt`

   On OS X/Linux this can be done using `scp -r /path/to/wallbox-mqtt-bridge/mqtt-bridge root@<wallbox-ip>:~`
6. `ssh` to your Wallbox and run the following commands

```sh
cd mqtt-bridge
./install.sh
```
