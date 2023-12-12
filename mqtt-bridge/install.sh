if [ ! -d "$HOME/.wallbox" ]; then
    echo "This script should only be run on a Wallbox!"
    exit -1
fi
if [ ! -d "/home/root/mqtt-bridge" ]; then
    echo "/home/root/mqtt-bridge not found!"
    exit -1
fi
if [ ! -f "/home/root/mqtt-bridge/bridge.py" ]; then
    echo "/home/root/mqtt-bridge/bridge.py not found!"
    exit -1
fi
if [ ! -f "/home/root/mqtt-bridge/bridge.ini" ]; then
    echo "/home/root/mqtt-bridge/bridge.ini not found!"
    exit -1
fi
if [ ! -f "/home/root/mqtt-bridge/requirements.txt" ]; then
    echo "/home/root/mqtt-bridge/requirements.txt not found!"
    exit -1
fi
if [ ! -f "/home/root/mqtt-bridge/mqtt-bridge.service" ]; then
    echo "/home/root/mqtt-bridge/mqtt-bridge.service not found!"
    exit -1
fi

cd /home/root/mqtt-bridge

echo "Setting up virtual environment"
python3 -m venv venv

echo "Installing Python dependencies"
venv/bin/pip install -r requirements.txt

echo "Setting up the MQTT bridge systemd service"
ln -s /home/root/mqtt-bridge/mqtt-bridge.service /lib/systemd/system/mqtt-bridge.service

echo "Enable the service to start on boot.."
systemctl enable mqtt-bridge

echo "..and launch the service now"
systemctl restart mqtt-bridge

systemctl status mqtt-bridge --no-pager

echo "Done!"
