# MQTT bridge for Wallbox

Polls the Wallbox local database and publishes changes to an external MQTT broker for Home Assistant.
Accepts changes from Home Assistant and updates the Wallbox local database.
Supports Home Assistant discovery.

Once you set this up you will have local control of your Wallbox in Home Assistant via entities like this:

![image](https://github.com/jagheterfredrik/wallbox-tooling/assets/9987465/60cbf100-f985-4c9c-a546-9776b3564705)

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
   - `/home/root/mqtt-bridge/mqtt-bridge.service`
6. `ssh` to your Wallbox and run the following commands

```sh
cd mqtt-bridge

# Create python virtual environment
python3 -m venv venv
source venv/bin/activate

# Install dependencies
pip install paho-mqtt==1.6.1
pip install pymysql==0.10.1
pip install redis==3.5.3

# Test the python script runs successfully
python bridge.py

# If it works press ctrl+C to kill it and continue below.
# If it doesn't start over.

# Setup systemd service to automatically start on boot
ln -s /home/root/mqtt-bridge/mqtt-bridge.service /lib/systemd/system/mqtt-bridge.service
systemctl enable mqtt-bridge

# Start service
systemctl start mqtt-bridge

# Check status
systemctl status mqtt-bridge

# If you make any changes restart the service with
systemctl restart mqtt-bridge
```
