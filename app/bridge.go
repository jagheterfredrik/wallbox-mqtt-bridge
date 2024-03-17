package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jagheterfredrik/wallbox-mqtt-bridge/app/ratelimit"
	"github.com/jagheterfredrik/wallbox-mqtt-bridge/app/wallbox"
)

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	panic("Connection to MQTT lost")
}

func LaunchBridge(configPath string) {
	c := LoadConfig(configPath)
	w := wallbox.New()
	w.RefreshData()

	serialNumber := w.SerialNumber()
	entityConfig := getEntities(w)
	if c.Settings.DebugSensors {
		for k, v := range getDebugEntities(w) {
			entityConfig[k] = v
		}
	}

	if c.Settings.PowerBoostEnabled {
		for k, v := range getPowerBoostEntities(w, c) {
			entityConfig[k] = v
		}
	}

	topicPrefix := "wallbox_" + serialNumber
	availabilityTopic := topicPrefix + "/availability"

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", c.MQTT.Host, c.MQTT.Port))
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
		config := map[string]interface{}{
			"~":                  topicPrefix + "/" + key,
			"availability_topic": availabilityTopic,
			"state_topic":        "~/state",
			"unique_id":          uid,
			"device": map[string]string{
				"identifiers": serialNumber,
				"name":        c.Settings.DeviceName,
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

	published := make(map[string]interface{})
	rateLimiter := map[string]*ratelimit.DeltaRateLimit{
		"charging_power":         ratelimit.NewDeltaRateLimit(10, 100),
		"charging_power_l1":      ratelimit.NewDeltaRateLimit(10, 100),
		"charging_power_l2":      ratelimit.NewDeltaRateLimit(10, 100),
		"charging_power_l3":      ratelimit.NewDeltaRateLimit(10, 100),
		"charging_current_l1":    ratelimit.NewDeltaRateLimit(10, 0.2),
		"charging_current_l2":    ratelimit.NewDeltaRateLimit(10, 0.2),
		"charging_current_l3":    ratelimit.NewDeltaRateLimit(10, 0.2),
		"power_boost_power_l1":   ratelimit.NewDeltaRateLimit(10, 100),
		"power_boost_power_l2":   ratelimit.NewDeltaRateLimit(10, 100),
		"power_boost_power_l3":   ratelimit.NewDeltaRateLimit(10, 100),
		"power_boost_current_l1": ratelimit.NewDeltaRateLimit(10, 0.2),
		"power_boost_current_l2": ratelimit.NewDeltaRateLimit(10, 0.2),
		"power_boost_current_l3": ratelimit.NewDeltaRateLimit(10, 0.2),
		"added_energy":           ratelimit.NewDeltaRateLimit(10, 50),
	}

	for {
		select {
		case <-ticker.C:
			w.RefreshData()
			for key, val := range entityConfig {
				payload := val.Getter()
				bytePayload := []byte(fmt.Sprint(payload))
				if published[key] != payload {
					if rate, ok := rateLimiter[key]; ok && !rate.Allow(strToFloat(payload)) {
						continue
					}
					fmt.Println("Publishing: ", key, payload)
					token := client.Publish(topicPrefix+"/"+key+"/state", 1, true, bytePayload)
					token.Wait()
					published[key] = payload
				}
			}
		case <-interrupt():
			fmt.Println("Interrupted. Exiting...")
			token := client.Publish(availabilityTopic, 1, true, "offline")
			token.Wait()
			client.Disconnect(250)
			os.Exit(0)
		}
	}
}

func interrupt() <-chan os.Signal {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	return interrupt
}
