package bridge

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jagheterfredrik/wallbox-mqtt-bridge/app/wallbox"
)

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	panic("Connection to MQTT lost")
}

func RunBridge(configPath string) {
	c := LoadConfig(configPath)
	w := wallbox.New()
	w.IncludePowerBoost = c.Settings.PowerBoostEnabled

	w.RefreshData()

	w.StartRedisSubscriptions()
	defer w.StopRedisSubscriptions()

	serialNumber := w.SerialNumber()
	firmwareVersion := w.FirmwareVersion()
	partNumber := w.PartNumber()

	entityConfig := getEntities(w)
	if c.Settings.DebugSensors {
		maps.Copy(entityConfig, getDebugEntities(w))
	}

	if c.Settings.PowerBoostEnabled {
		maps.Copy(entityConfig, getPowerBoostEntities(w))
	}

	topicPrefix := "wallbox_" + serialNumber
	availabilityTopic := topicPrefix + "/availability"

	opts := mqtt.NewClientOptions()
	brokerURL := c.MQTT.URL
	if brokerURL == "" {
		brokerURL = fmt.Sprintf("tcp://%s:%d", c.MQTT.Host, c.MQTT.Port)
	}
	opts.AddBroker(brokerURL)
	opts.SetUsername(c.MQTT.Username)
	opts.SetPassword(c.MQTT.Password)
	opts.SetWill(availabilityTopic, "offline", 1, true)
	opts.OnConnectionLost = connectLostHandler

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	for key, val := range entityConfig {
		component := val.Component
		uid := serialNumber + "_" + key
		config := map[string]any{
			"~":                  topicPrefix + "/" + key,
			"availability_topic": availabilityTopic,
			"state_topic":        "~/state",
			"unique_id":          uid,
			"device": map[string]string{
				"identifiers":  serialNumber,
				"name":         c.Settings.DeviceName,
				"model":        partNumber,
				"manufacturer": "Wallbox",
				"sw_version":   firmwareVersion,
			},
		}
		if val.Setter != nil {
			config["command_topic"] = "~/set"
		}
		for k, v := range val.Config {
			config[k] = v
		}
		jsonPayload, _ := json.Marshal(config)
		token := client.Publish("homeassistant/"+component+"/"+uid+"/config", 1, true, jsonPayload)
		token.Wait()
	}

	token := client.Publish(availabilityTopic, 1, true, "online")
	token.Wait()

	messageHandler := func(client mqtt.Client, msg mqtt.Message) {
		field := strings.Split(msg.Topic(), "/")[1]
		payload := string(msg.Payload())
		setter := entityConfig[field].Setter
		fmt.Println("Setting", field, payload)
		setter(payload)
	}

	topic := topicPrefix + "/+/set"
	client.Subscribe(topic, 1, messageHandler)

	ticker := time.NewTicker(time.Duration(c.Settings.PollingIntervalSeconds) * time.Second)
	defer ticker.Stop()

	published := make(map[string]any)

	// publishChanged iterates every entity, evaluates its getter, and publishes
	// to MQTT if the value has changed since the last publish — subject to the
	// entity's rate limiter. It is called both on ticker ticks (after a full
	// SQL+Redis refresh) and on live notifications (no SQL refresh).
	publishChanged := func() {
		for key, val := range entityConfig {
			payload := val.Getter()
			if published[key] == payload {
				continue
			}
			if val.RateLimit != nil && !val.RateLimit.Allow(strToFloat(payload)) {
				continue
			}
			fmt.Println("Publishing:", key, payload)
			token := client.Publish(topicPrefix+"/"+key+"/state", 1, true, []byte(payload))
			token.Wait()
			published[key] = payload
		}
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			// Full refresh
			w.RefreshData()
			publishChanged()

		case <-w.Updates:
			// Instant Real-Time loop: pub/sub event arrived
			publishChanged()

		case <-interrupt:
			fmt.Println("Interrupted. Exiting...")
			token := client.Publish(availabilityTopic, 1, true, "offline")
			token.Wait()
			client.Disconnect(250)
			return
		}
	}
}
