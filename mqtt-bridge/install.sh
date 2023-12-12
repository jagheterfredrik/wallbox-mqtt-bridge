if [ ! -d "$HOME/.wallbox" ]; then
    echo "This script should only be run on a Wallbox!"
    exit -1
fi

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

echo "Done!"
