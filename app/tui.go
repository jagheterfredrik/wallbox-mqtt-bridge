package bridge

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func askConfirmOrNew(field *string, name string) {
	fmt.Printf("%s (%s): ", name, *field)
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if len(input) > 0 {
		*field = input
	}
}

func askConfirmOrNewInt(field *int, name string) {
	fmt.Printf("%s (%d): ", name, *field)
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if len(input) > 0 {
		*field, _ = strconv.Atoi(input)
	}
}

func askConfirmOrNewBool(field *bool, name string) {
	fmt.Printf("%s (y/N): ", name)
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if len(input) > 0 && input == "y" {
		*field = true
	}
}

func RunTuiSetup() {
	config := WallboxConfig{}
	config.MQTT.Host = "127.0.0.1"
	config.MQTT.Port = 1883
	config.MQTT.Username = ""
	config.MQTT.Password = ""
	config.Settings.PollingIntervalSeconds = 1
	config.Settings.DeviceName = "Wallbox"
	config.Settings.DebugSensors = false
	config.Settings.PowerBoostEnabled = false

	askConfirmOrNew(&config.MQTT.Host, "MQTT Host")
	askConfirmOrNewInt(&config.MQTT.Port, "MQTT Port")
	askConfirmOrNew(&config.MQTT.Username, "MQTT Username")
	askConfirmOrNew(&config.MQTT.Password, "MQTT Password")
	askConfirmOrNewInt(&config.Settings.PollingIntervalSeconds, "Polling interval")
	askConfirmOrNew(&config.Settings.DeviceName, "Device name")
	askConfirmOrNewBool(&config.Settings.DebugSensors, "Debug sensors")
	askConfirmOrNewBool(&config.Settings.PowerBoostEnabled, "Enable Power Boost sensors")

	config.SaveTo("bridge.ini")
}
