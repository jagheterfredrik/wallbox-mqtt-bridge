package bridge

import (
	"fmt"
	"strconv"

	"github.com/jagheterfredrik/wallbox-mqtt-bridge/app/ratelimit"
	"github.com/jagheterfredrik/wallbox-mqtt-bridge/app/wallbox"
)

type Entity struct {
	Component string
	Getter    func() string
	Setter    func(string)
	RateLimit *ratelimit.DeltaRateLimit
	Config    map[string]string
}

func strToInt(val string) int {
	i, _ := strconv.Atoi(val)
	return i
}

// readingPayload formats a measurement for publishing. A missing reading
// returns "", which publishChanged skips, so Home Assistant keeps its last
// value instead of seeing 0 before the first telemetry event. An invalid one
// returns "None", which Home Assistant shows as unknown rather than as 0.
func readingPayload(v float64, state wallbox.ReadingState) string {
	switch state {
	case wallbox.ReadingMissing:
		return ""
	case wallbox.ReadingInvalid:
		return "None"
	}
	return fmt.Sprint(v)
}

func strToFloat(val string) float64 {
	f, _ := strconv.ParseFloat(val, 64)
	return f
}

func getEntities(w *wallbox.Wallbox) map[string]Entity {
	return map[string]Entity{
		"added_energy": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.AddedEnergy()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 50),
			Config: map[string]string{
				"name":                        "Added energy",
				"device_class":                "energy",
				"unit_of_measurement":         "Wh",
				"state_class":                 "total_increasing",
				"suggested_display_precision": "1",
			},
		},
		"added_range": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.Data.SQL.AddedRange) },
			Config: map[string]string{
				"name":                        "Added range",
				"device_class":                "distance",
				"unit_of_measurement":         "km",
				"state_class":                 "total_increasing",
				"suggested_display_precision": "1",
				"icon":                        "mdi:map-marker-distance",
			},
		},
		"cable_connected": {
			Component: "binary_sensor",
			Getter:    func() string { return fmt.Sprint(w.CableConnected()) },
			Config: map[string]string{
				"name":         "Cable connected",
				"payload_on":   "1",
				"payload_off":  "0",
				"icon":         "mdi:ev-plug-type1",
				"device_class": "plug",
			},
		},
		"charging_enable": {
			Component: "switch",
			Setter:    func(val string) { w.SetChargingEnable(strToInt(val)) },
			Getter:    func() string { return fmt.Sprint(w.ChargingEnable()) },
			Config: map[string]string{
				"name":        "Charging enable",
				"payload_on":  "1",
				"payload_off": "0",
				"icon":        "mdi:ev-station",
			},
		},
		"charging_power": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingPower()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Charging power",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"charging_power_l1": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingPowerL1()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Charging power L1",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"charging_power_l2": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingPowerL2()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Charging power L2",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"charging_power_l3": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingPowerL3()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Charging power L3",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"charging_current_l1": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingCurrentL1()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.2),
			Config: map[string]string{
				"name":                        "Charging current L1",
				"device_class":                "current",
				"unit_of_measurement":         "A",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"charging_current_l2": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingCurrentL2()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.2),
			Config: map[string]string{
				"name":                        "Charging current L2",
				"device_class":                "current",
				"unit_of_measurement":         "A",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"charging_current_l3": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.ChargingCurrentL3()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.2),
			Config: map[string]string{
				"name":                        "Charging current L3",
				"device_class":                "current",
				"unit_of_measurement":         "A",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"cumulative_added_energy": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.CumulativeAddedEnergy()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Cumulative added energy",
				"device_class":                "energy",
				"unit_of_measurement":         "Wh",
				"state_class":                 "total_increasing",
				"suggested_display_precision": "1",
			},
		},
		"halo_brightness": {
			Component: "number",
			Setter:    func(val string) { w.SetHaloBrightness(strToInt(val)) },
			Getter:    func() string { return fmt.Sprint(w.Data.SQL.HaloBrightness) },
			Config: map[string]string{
				"name":                "Halo Brightness",
				"command_topic":       "~/set",
				"min":                 "0",
				"max":                 "100",
				"icon":                "mdi:brightness-percent",
				"unit_of_measurement": "%",
				"entity_category":     "config",
			},
		},
		"lock": {
			Component: "lock",
			Setter:    func(val string) { w.SetLocked(strToInt(val)) },
			Getter:    func() string { return fmt.Sprint(w.Data.SQL.Lock) },
			Config: map[string]string{
				"name":           "Lock",
				"payload_lock":   "1",
				"payload_unlock": "0",
				"state_locked":   "1",
				"state_unlocked": "0",
				"command_topic":  "~/set",
			},
		},
		"max_charging_current": {
			Component: "number",
			Setter:    func(val string) { w.SetMaxChargingCurrent(strToInt(val)) },
			Getter:    func() string { return fmt.Sprint(w.MaxChargingCurrent()) },
			Config: map[string]string{
				"name":                "Max charging current",
				"command_topic":       "~/set",
				"min":                 "6",
				"max":                 fmt.Sprint(w.AvailableCurrent()),
				"unit_of_measurement": "A",
				"device_class":        "current",
				"entity_category":     "config",
			},
		},
		"status": {
			Component: "sensor",
			Getter:    w.EffectiveStatus,
			Config: map[string]string{
				"name": "Status",
				"icon": "mdi:information-outline",
			},
		},
		"temp_l1": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.TemperatureReading(0)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.5),
			Config: map[string]string{
				"name":                        "Temperature Line 1",
				"unit_of_measurement":         "°C",
				"device_class":                "temperature",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"temp_l2": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.TemperatureReading(1)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.5),
			Config: map[string]string{
				"name":                        "Temperature Line 2",
				"unit_of_measurement":         "°C",
				"device_class":                "temperature",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"temp_l3": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.TemperatureReading(2)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.5),
			Config: map[string]string{
				"name":                        "Temperature Line 3",
				"unit_of_measurement":         "°C",
				"device_class":                "temperature",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"voltage_l1": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.VoltageReading(0)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 2),
			Config: map[string]string{
				"name":                        "Voltage Line 1",
				"unit_of_measurement":         "V",
				"device_class":                "voltage",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"voltage_l2": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.VoltageReading(1)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 2),
			Config: map[string]string{
				"name":                        "Voltage Line 2",
				"unit_of_measurement":         "V",
				"device_class":                "voltage",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"voltage_l3": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.VoltageReading(2)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 2),
			Config: map[string]string{
				"name":                        "Voltage Line 3",
				"unit_of_measurement":         "V",
				"device_class":                "voltage",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
	}
}

// boolIntToStr formats a bool as the "1"/"0" payload used by switch entities.
func boolIntToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// scheduleDay names the 7 weekday switches so Home Assistant's name-sort
// shows Monday->Sunday: each name is prefixed with its 1..7 index.
var scheduleDayNames = [7]struct {
	key   string // entity key fragment, e.g. "monday"
	label string // display label, e.g. "1 Monday"
}{
	{"monday", "1 Monday"},
	{"tuesday", "2 Tuesday"},
	{"wednesday", "3 Wednesday"},
	{"thursday", "4 Thursday"},
	{"friday", "5 Friday"},
	{"saturday", "6 Saturday"},
	{"sunday", "7 Sunday"},
}

// getScheduleEntities builds the per-slot charging schedule entities for all 4
// fixed slots, plus one atomic set_schedule JSON command entity. All writes go
// through the schedule_manager Redis command (CMD_UPDATE_SCHEDULE); the getters
// read the cache filled by RefreshData so entities reflect the charger's real
// current schedule.
func getScheduleEntities(w *wallbox.Wallbox) map[string]Entity {
	entities := make(map[string]Entity)

	for slot := 1; slot <= 4; slot++ {
		slot := slot // capture per iteration for the closures below
		prefix := fmt.Sprintf("Schedule %d", slot)

		// 1. Enable switch.
		entities[fmt.Sprintf("schedule_%d_enable", slot)] = Entity{
			Component: "switch",
			Setter:    func(val string) { w.SetScheduleEnable(slot, strToInt(val) == 1) },
			Getter: func() string {
				s, ok := w.ScheduleSlot(slot)
				if !ok {
					return ""
				}
				return boolIntToStr(s.Enable)
			},
			Config: map[string]string{
				"name":        prefix + " Enable",
				"payload_on":  "1",
				"payload_off": "0",
				"icon":        "mdi:calendar-clock",
			},
		}

		// 2. Start time.
		entities[fmt.Sprintf("schedule_%d_start", slot)] = Entity{
			Component: "time",
			Setter: func(val string) {
				if hhmm, ok := wallbox.ClockToHHMMInt(val); ok {
					w.SetScheduleStart(slot, hhmm)
				} else {
					fmt.Println("Warning: ignoring bad schedule start time:", val)
				}
			},
			Getter: func() string {
				s, ok := w.ScheduleSlot(slot)
				if !ok {
					return ""
				}
				return wallbox.HHMMIntToClock(s.StartTime) + ":00"
			},
			Config: map[string]string{
				"name":          prefix + " Start",
				"command_topic": "~/set",
				"icon":          "mdi:clock-start",
			},
		}

		// 3. Stop time.
		entities[fmt.Sprintf("schedule_%d_stop", slot)] = Entity{
			Component: "time",
			Setter: func(val string) {
				if hhmm, ok := wallbox.ClockToHHMMInt(val); ok {
					w.SetScheduleStop(slot, hhmm)
				} else {
					fmt.Println("Warning: ignoring bad schedule stop time:", val)
				}
			},
			Getter: func() string {
				s, ok := w.ScheduleSlot(slot)
				if !ok {
					return ""
				}
				return wallbox.HHMMIntToClock(s.StopTime) + ":00"
			},
			Config: map[string]string{
				"name":          prefix + " Stop",
				"command_topic": "~/set",
				"icon":          "mdi:clock-end",
			},
		}

		// 4. Seven day switches (Monday..Sunday).
		for i, day := range scheduleDayNames {
			dayIndex := i + 1 // 1=Monday..7=Sunday
			dayKey := day.key
			entities[fmt.Sprintf("schedule_%d_day_%s", slot, dayKey)] = Entity{
				Component: "switch",
				Setter:    func(val string) { w.SetScheduleDay(slot, dayIndex, strToInt(val) == 1) },
				Getter: func() string {
					s, ok := w.ScheduleSlot(slot)
					if !ok {
						return ""
					}
					days := [7]bool{s.Monday, s.Tuesday, s.Wednesday, s.Thursday, s.Friday, s.Saturday, s.Sunday}
					return boolIntToStr(days[dayIndex-1])
				},
				Config: map[string]string{
					"name":            prefix + " — " + day.label,
					"payload_on":      "1",
					"payload_off":     "0",
					"icon":            "mdi:calendar-today",
					"entity_category": "config",
				},
			}
		}

		// 5. Delete/park button. Parking = enable false (there is no true delete).
		entities[fmt.Sprintf("schedule_%d_delete", slot)] = Entity{
			Component: "button",
			Setter:    func(string) { w.ParkSchedule(slot) },
			Getter:    func() string { return "" },
			Config: map[string]string{
				"name":          prefix + " Delete",
				"command_topic": "~/set",
				"icon":          "mdi:calendar-remove",
			},
		}
	}

	// 6. Atomic set_schedule JSON command. Getter returns "" (bridge skips it).
	entities["set_schedule"] = Entity{
		Component: "text",
		Setter:    func(val string) { w.SetScheduleJSON(val) },
		Getter:    func() string { return "" },
		Config: map[string]string{
			"name":          "Set schedule",
			"command_topic": "~/set",
			"icon":          "mdi:calendar-edit",
			"mode":          "text",
		},
	}

	return entities
}

func getPowerBoostEntities(w *wallbox.Wallbox) map[string]Entity {
	return map[string]Entity{
		"power_boost_power_l1": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostPowerL1()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Power Boost L1",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"power_boost_power_l2": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostPowerL2()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Power Boost L2",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"power_boost_power_l3": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostPowerL3()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 100),
			Config: map[string]string{
				"name":                        "Power Boost L3",
				"device_class":                "power",
				"unit_of_measurement":         "W",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"power_boost_current_l1": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostCurrentL1()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.2),
			Config: map[string]string{
				"name":                        "Power Boost current L1",
				"device_class":                "current",
				"unit_of_measurement":         "A",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"power_boost_current_l2": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostCurrentL2()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.2),
			Config: map[string]string{
				"name":                        "Power Boost current L2",
				"device_class":                "current",
				"unit_of_measurement":         "A",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"power_boost_current_l3": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostCurrentL3()) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 0.2),
			Config: map[string]string{
				"name":                        "Power Boost current L3",
				"device_class":                "current",
				"unit_of_measurement":         "A",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
			},
		},
		"power_boost_voltage_l1": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.PowerBoostVoltageReading(0)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 2),
			Config: map[string]string{
				"name":                        "Power Boost voltage L1",
				"device_class":                "voltage",
				"unit_of_measurement":         "V",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"power_boost_voltage_l2": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.PowerBoostVoltageReading(1)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 2),
			Config: map[string]string{
				"name":                        "Power Boost voltage L2",
				"device_class":                "voltage",
				"unit_of_measurement":         "V",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"power_boost_voltage_l3": {
			Component: "sensor",
			Getter:    func() string { return readingPayload(w.PowerBoostVoltageReading(2)) },
			RateLimit: ratelimit.NewDeltaRateLimit(10, 2),
			Config: map[string]string{
				"name":                        "Power Boost voltage L3",
				"device_class":                "voltage",
				"unit_of_measurement":         "V",
				"state_class":                 "measurement",
				"suggested_display_precision": "1",
				"entity_category":             "diagnostic",
			},
		},
		"power_boost_meter_status": {
			Component: "sensor",
			Getter:    w.ExternalMeterStatus,
			Config: map[string]string{
				"name":            "Power Boost meter status",
				"icon":            "mdi:meter-electric",
				"entity_category": "diagnostic",
			},
		},
		"power_boost_cumulative_added_energy": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.PowerBoostCumulativeEnergy()) },
			Config: map[string]string{
				"name":                        "Power Boost cumulative energy",
				"device_class":                "energy",
				"unit_of_measurement":         "Wh",
				"state_class":                 "total_increasing",
				"suggested_display_precision": "1",
			},
		},
	}
}

func getDebugEntities(w *wallbox.Wallbox) map[string]Entity {
	return map[string]Entity{
		"control_pilot": {
			Component: "sensor",
			Getter:    w.ControlPilotStatus,
			Config: map[string]string{
				"name":            "Control pilot",
				"icon":            "mdi:car-connected",
				"entity_category": "diagnostic",
			},
		},
		"m2w_status": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.M2WStatus()) },
			Config: map[string]string{
				"name":            "M2W Status",
				"icon":            "mdi:state-machine",
				"entity_category": "diagnostic",
			},
		},
		"state_machine_state": {
			Component: "sensor",
			Getter:    w.StateMachineState,
			Config: map[string]string{
				"name":            "State machine",
				"icon":            "mdi:state-machine",
				"entity_category": "diagnostic",
			},
		},
		"s2_open": {
			Component: "sensor",
			Getter:    func() string { return fmt.Sprint(w.S2Open()) },
			Config: map[string]string{
				"name":            "S2 open",
				"icon":            "mdi:switch",
				"entity_category": "diagnostic",
			},
		},
	}
}
