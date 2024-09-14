# MQTT Bridge for Wallbox

This open-source project connects your Wallbox fully locally to Home Assistant, providing you with unparalleled speed and reliability.

Note: Doesn't work with firmware v6.6.x see issue https://github.com/jagheterfredrik/wallbox-mqtt-bridge/issues/63

## Features

- **Instant Sensor Data:** The Wallbox's internal state is polled every second and any updates are immediately pushed to the external MQTT broker.

- **Instant Control:** Quickly lock/unlock, pause/resume or change the max charging current, without involving the manufacturer's servers.

- **Always available:** As long as your local network is up and your Wallbox has power, you're in control! No need to rely on a third party to communicate with the device you own.

- **Home Assistant MQTT Auto Discovery:** Enjoy a hassle-free setup with Home Assistant MQTT Auto Discovery support. The integration effortlessly integrates with your existing Home Assistant environment.

<br/>
<p align="center">
   <img src="https://github.com/jagheterfredrik/wallbox-mqtt-bridge/assets/9987465/06488a5d-e6fe-4491-b11d-e7176792a7f5" height="507" />
</p>

## Getting Started

1. [Root your Wallbox](https://github.com/jagheterfredrik/wallbox-pwn)
2. Setup an MQTT Broker, if you don't already have one. Here's an example [installing it as a Home Assistant add-on](https://www.youtube.com/watch?v=dqTn-Gk4Qeo)
3. `ssh` to your Wallbox and run

```sh
curl -sSfL https://github.com/jagheterfredrik/wallbox-mqtt-bridge/releases/download/bridge/install.sh > install.sh && bash install.sh
```

Note: To upgrade to new version, simply run the command from step 3 again.

## Acknowledgments

A big shoutout to [@tronikos](https://github.com/tronikos) for their valuable contributions. This project wouldn't be the same without the collaborative spirit of the open-source community.
