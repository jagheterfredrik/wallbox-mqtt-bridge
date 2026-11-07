package bridge

import (
	"fmt"

	"github.com/jagheterfredrik/wallbox-mqtt-bridge/app/wallbox"
)

// boolIntToStr formats a bool as the "1"/"0" payload used by switch entities.
func boolIntToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// scheduleDayNames names the 7 weekday switches so Home Assistant's name-sort
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
// through the schedule_manager Redis command (CMD_UPDATE_SCHEDULE /
// CMD_CREATE_SCHEDULE); the getters read the cache filled by RefreshData so
// entities reflect the charger's real current schedule.
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
				"name": prefix + " Start",
				"icon": "mdi:clock-start",
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
				"name": prefix + " Stop",
				"icon": "mdi:clock-end",
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
				"name": prefix + " Delete",
				"icon": "mdi:calendar-remove",
			},
		}
	}

	// 6. Atomic set_schedule JSON command. Its state is the last accepted
	// payload, so the text entity shows what was last set.
	entities["set_schedule"] = Entity{
		Component: "text",
		Setter:    func(val string) { w.SetScheduleJSON(val) },
		Getter:    func() string { return w.LastScheduleJSON() },
		Config: map[string]string{
			"name": "Set schedule",
			"icon": "mdi:calendar-edit",
			"mode": "text",
		},
	}

	return entities
}
