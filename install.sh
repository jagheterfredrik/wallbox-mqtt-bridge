#!/bin/bash

# Remove any previous installation, except any configuration
systemctl stop mqtt-bridge 2> /dev/null
systemctl disable mqtt-bridge 2> /dev/null
rm -f /lib/systemd/system/mqtt-bridge.service
find ~/mqtt-bridge/ -type f ! -name bridge.ini -delete

# Download the bridge
echo "Downloading the bridge"
arch=$(uname -m)
if [ "$arch" == "armv7l" ]; then
    curl -sSfL --create-dirs -o ~/mqtt-bridge/bridge https://github.com/jagheterfredrik/wallbox-mqtt-bridge/releases/download/bridge/bridge-armhf
elif [ "$arch" == "aarch64" ]; then
    curl -sSfL --create-dirs -o ~/mqtt-bridge/bridge https://github.com/jagheterfredrik/wallbox-mqtt-bridge/releases/download/bridge/bridge-arm64
else
    echo "Unknown architecture $arch"
    exit 1
fi

chmod +x ~/mqtt-bridge/bridge

# Create config if it doesn't exist
if [ ! -e ~/mqtt-bridge/bridge.ini ]; then
    echo "No configuration found, please provide it now"
    cd ~/mqtt-bridge/
    ./bridge --config
fi

# Install the service
echo "Setting up auto-start"

content="[Unit]
Description=MQTT Bridge
After=network.target
Requires=mysqld.service
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
RestartSec=1
User=root
ExecStart=/home/root/mqtt-bridge/bridge /home/root/mqtt-bridge/bridge.ini

[Install]
WantedBy=multi-user.target"

echo "$content" > /lib/systemd/system/mqtt-bridge.service

systemctl daemon-reload
systemctl enable mqtt-bridge
systemctl restart mqtt-bridge

echo "Done!"
