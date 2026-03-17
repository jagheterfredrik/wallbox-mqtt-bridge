package bridge

import (
	"fmt"
	"os"

	"gopkg.in/ini.v1"
)

type WallboxConfig struct {
	MQTT struct {
		URL      string `ini:"url"`
		Host     string `ini:"host"`
		Port     int    `ini:"port"`
		Username string `ini:"username"`
		Password string `ini:"password"`
	} `ini:"mqtt"`

	Settings struct {
		PollingIntervalSeconds int    `ini:"polling_interval_seconds"`
		DeviceName             string `ini:"device_name"`
		DebugSensors           bool   `ini:"debug_sensors"`
		PowerBoostEnabled      bool   `ini:"power_boost_enabled"`
	} `ini:"settings"`
}

func (w *WallboxConfig) SaveTo(path string) {
	cfg := ini.Empty()
	if err := cfg.ReflectFrom(w); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to reflect config: %v\n", err)
		return
	}
	if err := cfg.SaveTo(path); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save config to %s: %v\n", path, err)
	}
}

func LoadConfig(path string) *WallboxConfig {
	cfg, err := ini.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: could not load config file at %s: %v\n— run with --config to create it\n", path, err)
		os.Exit(1)
	}

	var config WallboxConfig
	if err := cfg.MapTo(&config); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: could not parse config file at %s: %v\n", path, err)
		os.Exit(1)
	}

	return &config
}
