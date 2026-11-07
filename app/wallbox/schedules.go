package wallbox

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleRow is one row of the 'wallbox.schedules' table. The charger exposes
// a fixed set of 4 slots (ScheduleID 1..4); there is no real create/delete,
// only toggling Enable and rewriting fields.
type ScheduleRow struct {
	ScheduleID int    `db:"schedule_id"`
	Enable     bool   `db:"enable"`
	Monday     bool   `db:"monday"`
	Tuesday    bool   `db:"tuesday"`
	Wednesday  bool   `db:"wednesday"`
	Thursday   bool   `db:"thursday"`
	Friday     bool   `db:"friday"`
	Saturday   bool   `db:"saturday"`
	Sunday     bool   `db:"sunday"`
	StartTime  int    `db:"start_time"`
	StopTime   int    `db:"stop_time"`
	MaxCurrent int    `db:"max_current"`
	Name       string `db:"name"`
	Ulid       string `db:"ulid"`
	// Exists is false when the slot has no row in the schedules table yet
	// (the table starts empty on a factory charger). publishScheduleUpdate
	// uses it to pick CMD_CREATE_SCHEDULE over CMD_UPDATE_SCHEDULE, which
	// schedule_manager rejects for a schedule_id with no row. Not a column.
	Exists bool `db:"-"`
}

// scheduleSelectQuery reads the schedules table. stop_time and name are
// nullable in the schema, so they are COALESCEd to keep sqlx scans from
// failing on NULL; ulid gets the same treatment for never-written slots.
const scheduleSelectQuery = "SELECT `schedule_id`, `enable`, `monday`, `tuesday`, " +
	"`wednesday`, `thursday`, `friday`, `saturday`, `sunday`, " +
	"`start_time`, COALESCE(`stop_time`, 0) AS `stop_time`, `max_current`, " +
	"COALESCE(`name`, '') AS `name`, COALESCE(`ulid`, '') AS `ulid` " +
	"FROM `schedules` ORDER BY `schedule_id`"

// ReadSchedules returns the schedule slots ordered by schedule_id, read
// directly from the 'wallbox.schedules' table.
func (w *Wallbox) ReadSchedules() []ScheduleRow {
	var rows []ScheduleRow
	if err := w.sqlClient.Select(&rows, scheduleSelectQuery); err != nil {
		fmt.Println("SQL ReadSchedules error:", err)
	}
	for i := range rows {
		rows[i].Exists = true
	}
	return rows
}

// scheduleBySlot returns the current ScheduleRow for the given slot (1..4).
// It prefers the in-memory cache, which publishScheduleUpdate keeps up to
// date so back-to-back writes build on each other instead of re-reading
// MySQL before schedule_manager has persisted the previous command. Before
// the first RefreshData it falls back to a direct MySQL read. A slot with no
// row yields a zero-value row with Exists=false so the write path uses
// CMD_CREATE_SCHEDULE. The bool is false only for an out-of-range slot.
func (w *Wallbox) scheduleBySlot(slot int) (ScheduleRow, bool) {
	if slot < 1 || slot > 4 {
		return ScheduleRow{}, false
	}
	w.dataMu.RLock()
	r, ok := w.Data.Schedules[slot]
	w.dataMu.RUnlock()
	if ok {
		return r, true
	}
	for _, row := range w.ReadSchedules() {
		if row.ScheduleID == slot {
			return row, true
		}
	}
	return ScheduleRow{ScheduleID: slot}, true
}

// HHMMIntToClock converts a LOCAL wall-clock HHMM integer (e.g. 2200, 100,
// 900) into "HH:MM" for a Home Assistant time entity. No timezone conversion.
func HHMMIntToClock(i int) string {
	if i < 0 {
		i = 0
	}
	h := i / 100
	m := i % 100
	return fmt.Sprintf("%02d:%02d", h, m)
}

// ClockToHHMMInt parses a time string into a LOCAL HHMM integer. It accepts
// "HH:MM", "HH:MM:SS" (seconds ignored) and bare "HHMM"/"HMM". No timezone
// conversion. Returns (0, false) when the input can't be parsed.
func ClockToHHMMInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) < 2 {
			return 0, false
		}
		h, err1 := strconv.Atoi(parts[0])
		m, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, false
		}
		if h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, false
		}
		return h*100 + m, true
	}
	// Bare HHMM / HMM integer form.
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	h := v / 100
	m := v % 100
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*100 + m, true
}

// addOneMinuteWrap returns the HHMM integer one minute after the given one,
// wrapping over midnight via total-minutes mod 1440 (so 2359 -> 0000).
func addOneMinuteWrap(hhmm int) int {
	minutes := (hhmm/100)*60 + (hhmm % 100)
	minutes = (minutes + 1) % 1440
	return (minutes/60)*100 + (minutes % 60)
}

// scheduleUpdateCommand is the CMD_UPDATE_SCHEDULE / CMD_CREATE_SCHEDULE
// message published to the schedule_manager on /wbx/schedule_manager/cmd.
// The field set and ordering mirror the command captured from the charger's
// own app.
type scheduleUpdateCommand struct {
	Body struct {
		Schedule struct {
			ScheduleID  int    `json:"schedule_id"`
			Ulid        string `json:"ulid"`
			Enable      bool   `json:"enable"`
			Monday      bool   `json:"monday"`
			Tuesday     bool   `json:"tuesday"`
			Wednesday   bool   `json:"wednesday"`
			Thursday    bool   `json:"thursday"`
			Friday      bool   `json:"friday"`
			Saturday    bool   `json:"saturday"`
			Sunday      bool   `json:"sunday"`
			StartTime   int    `json:"start_time"`
			StopTime    int    `json:"stop_time"`
			MaxCurrent  int    `json:"max_current"`
			Type        string `json:"type"`
			Name        string `json:"name"`
			Recurrent   bool   `json:"recurrent"`
			EnabledDays []int  `json:"enabled_days"`
			CreatedAt   int64  `json:"created_at"`
		} `json:"schedule"`
	} `json:"body"`
	Header struct {
		MessageID string `json:"message_id"`
		RequestID string `json:"request_id"`
		Source    string `json:"source"`
		Timestamp string `json:"timestamp"`
	} `json:"header"`
}

// ulidAlphabet is the Crockford Base32 alphabet used by ULIDs.
const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newULID returns a fresh ULID: 48-bit millisecond timestamp + 80 bits of
// crypto randomness, encoded as 26 Crockford Base32 chars. The schedule_manager
// treats the ulid as the schedule's version token — the app, cloud sync and
// other consumers key off it, so every CMD_UPDATE_SCHEDULE must carry a NEW
// ulid or the change is deduplicated as already-seen (this is why time edits
// used to look like they did nothing until the slot was toggled). The Wallbox
// app itself generates a fresh ulid on every schedule edit.
func newULID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is effectively unreachable; fall back to a
		// time-derived value so we still emit a well-formed ULID.
		binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixNano()))
	}

	ms := uint64(time.Now().UnixMilli())
	var out [26]byte
	for i := 9; i >= 0; i-- {
		out[i] = ulidAlphabet[ms&0x1F]
		ms >>= 5
	}

	// 80 random bits -> 16 chars of 5 bits each, MSB-first.
	var acc uint64
	var nbits uint
	idx := 10
	for _, by := range b {
		acc = (acc << 8) | uint64(by)
		nbits += 8
		for nbits >= 5 {
			nbits -= 5
			out[idx] = ulidAlphabet[(acc>>nbits)&0x1F]
			idx++
		}
	}
	for idx < 26 {
		out[idx] = ulidAlphabet[0]
		idx++
	}
	return string(out[:])
}

// publishScheduleUpdate marshals a schedule command from the given row and
// publishes it on /wbx/schedule_manager/cmd. The firmware applies it live and
// persists it to MySQL, so this is the ONLY write path for schedules (the
// schedules table is never written directly). A FRESH ulid is generated for
// every update — reusing the stored one makes consumers treat the change as a
// duplicate. max_current of 0 is normalised to -1 ("use the charger's
// configured max"). type is sent as the captured string "Regular",
// recurrent as true (without it schedule_manager treats the schedule as
// one-time and disables it after a single session) and enabled_days as an
// empty array (the manager derives days from the per-day booleans).
//
// Rows that don't exist in MySQL yet (Exists=false) are sent as
// CMD_CREATE_SCHEDULE — schedule_manager rejects CMD_UPDATE_SCHEDULE for a
// schedule_id with no row ("Specified ID does not exist").
func (w *Wallbox) publishScheduleUpdate(s ScheduleRow) {
	var cmd scheduleUpdateCommand
	cmd.Body.Schedule.ScheduleID = s.ScheduleID
	cmd.Body.Schedule.Ulid = newULID()
	cmd.Body.Schedule.Enable = s.Enable
	cmd.Body.Schedule.Monday = s.Monday
	cmd.Body.Schedule.Tuesday = s.Tuesday
	cmd.Body.Schedule.Wednesday = s.Wednesday
	cmd.Body.Schedule.Thursday = s.Thursday
	cmd.Body.Schedule.Friday = s.Friday
	cmd.Body.Schedule.Saturday = s.Saturday
	cmd.Body.Schedule.Sunday = s.Sunday
	cmd.Body.Schedule.StartTime = s.StartTime
	cmd.Body.Schedule.StopTime = s.StopTime
	maxCurrent := s.MaxCurrent
	if maxCurrent == 0 {
		maxCurrent = -1
	}
	cmd.Body.Schedule.MaxCurrent = maxCurrent
	cmd.Body.Schedule.Type = "Regular"
	cmd.Body.Schedule.Recurrent = true
	cmd.Body.Schedule.Name = s.Name
	cmd.Body.Schedule.EnabledDays = []int{}
	cmd.Body.Schedule.CreatedAt = time.Now().Unix()

	w.scheduleMu.Lock()
	w.scheduleReqCounter++
	reqID := fmt.Sprintf("MQTTBRIDGE#%d", w.scheduleReqCounter)
	w.scheduleMu.Unlock()

	cmd.Header.MessageID = "CMD_UPDATE_SCHEDULE"
	if !s.Exists {
		cmd.Header.MessageID = "CMD_CREATE_SCHEDULE"
	}
	cmd.Header.RequestID = reqID
	cmd.Header.Source = "MYWALLBOX"
	cmd.Header.Timestamp = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	payload, err := json.Marshal(cmd)
	if err != nil {
		fmt.Println("Warning: failed to marshal schedule command:", err)
		return
	}

	ctx := context.Background()
	if err := w.redisClient.Publish(ctx, "/wbx/schedule_manager/cmd", string(payload)).Err(); err != nil {
		fmt.Println("Warning: failed to publish schedule command:", err)
		return
	}

	// Apply the write to the cache immediately so the entities reflect it and
	// the next setter's read-modify-write builds on it — schedule_manager
	// persists to MySQL asynchronously, so a MySQL re-read could still return
	// the pre-write state and clobber this change.
	s.Ulid = cmd.Body.Schedule.Ulid
	s.Exists = true
	w.dataMu.Lock()
	if w.Data.Schedules == nil {
		w.Data.Schedules = make(map[int]ScheduleRow)
	}
	w.Data.Schedules[s.ScheduleID] = s
	w.dataMu.Unlock()
}

// SetScheduleEnable turns a slot on/off, preserving all its other fields.
func (w *Wallbox) SetScheduleEnable(slot int, enable bool) {
	s, ok := w.scheduleBySlot(slot)
	if !ok {
		fmt.Println("Warning: SetScheduleEnable: unknown schedule slot", slot)
		return
	}
	s.Enable = enable
	w.publishScheduleUpdate(s)
}

// SetScheduleStart sets a slot's start_time. Create-on-entry: if the slot is
// currently disabled/empty, setting the start also fills stop_time with
// start+1min (wrapping midnight), enables all 7 days and sets enable=true, so
// typing a start time "creates" the schedule. If the slot is already enabled,
// only start_time changes.
func (w *Wallbox) SetScheduleStart(slot, hhmm int) {
	s, ok := w.scheduleBySlot(slot)
	if !ok {
		fmt.Println("Warning: SetScheduleStart: unknown schedule slot", slot)
		return
	}
	s.StartTime = hhmm
	if !s.Enable {
		s.StopTime = addOneMinuteWrap(hhmm)
		s.Monday = true
		s.Tuesday = true
		s.Wednesday = true
		s.Thursday = true
		s.Friday = true
		s.Saturday = true
		s.Sunday = true
		s.Enable = true
	}
	w.publishScheduleUpdate(s)
}

// SetScheduleStop sets a slot's stop_time, preserving all its other fields.
// There is no create-on-entry for the stop time.
func (w *Wallbox) SetScheduleStop(slot, hhmm int) {
	s, ok := w.scheduleBySlot(slot)
	if !ok {
		fmt.Println("Warning: SetScheduleStop: unknown schedule slot", slot)
		return
	}
	s.StopTime = hhmm
	w.publishScheduleUpdate(s)
}

// SetScheduleDay flips one weekday for a slot. dayIndex is 1=Monday..7=Sunday.
func (w *Wallbox) SetScheduleDay(slot, dayIndex int, on bool) {
	s, ok := w.scheduleBySlot(slot)
	if !ok {
		fmt.Println("Warning: SetScheduleDay: unknown schedule slot", slot)
		return
	}
	switch dayIndex {
	case 1:
		s.Monday = on
	case 2:
		s.Tuesday = on
	case 3:
		s.Wednesday = on
	case 4:
		s.Thursday = on
	case 5:
		s.Friday = on
	case 6:
		s.Saturday = on
	case 7:
		s.Sunday = on
	default:
		fmt.Println("Warning: SetScheduleDay: bad day index", dayIndex)
		return
	}
	w.publishScheduleUpdate(s)
}

// ParkSchedule "deletes" a slot by setting enable=false. The charger has no
// true delete; parking a schedule is just disabling it while keeping its
// fields intact.
func (w *Wallbox) ParkSchedule(slot int) {
	s, ok := w.scheduleBySlot(slot)
	if !ok {
		fmt.Println("Warning: ParkSchedule: unknown schedule slot", slot)
		return
	}
	s.Enable = false
	w.publishScheduleUpdate(s)
}

// scheduleJSONPayload is the atomic set_schedule command accepted on the
// set_schedule text entity. start/stop accept "HHMM" or "HH:MM"; days accepts
// a list of mon..sun and/or the keywords all/weekdays/weekends/none.
type scheduleJSONPayload struct {
	Slot       int      `json:"slot"`
	Enable     *bool    `json:"enable"`
	Start      string   `json:"start"`
	Stop       string   `json:"stop"`
	Days       []string `json:"days"`
	MaxCurrent *int     `json:"max_current"`
	Name       *string  `json:"name"`
}

// daysFromList maps a list of day tokens / keywords to the 7 booleans
// (Mon..Sun). Keywords: all, weekdays (Mon-Fri), weekends (Sat-Sun), none.
// Individual days: mon,tue,wed,thu,fri,sat,sun (case-insensitive). Returns
// false if any token is unrecognised, so a malformed list fails safe.
func daysFromList(days []string) (mon, tue, wed, thu, fri, sat, sun bool, ok bool) {
	set := func(m, t, w, th, f, s, su bool) {
		mon, tue, wed, thu, fri, sat, sun = m, t, w, th, f, s, su
	}
	for _, d := range days {
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "all":
			set(true, true, true, true, true, true, true)
			return mon, tue, wed, thu, fri, sat, sun, true
		case "weekdays":
			mon, tue, wed, thu, fri = true, true, true, true, true
		case "weekends":
			sat, sun = true, true
		case "none":
			set(false, false, false, false, false, false, false)
		case "mon", "monday":
			mon = true
		case "tue", "tuesday":
			tue = true
		case "wed", "wednesday":
			wed = true
		case "thu", "thursday":
			thu = true
		case "fri", "friday":
			fri = true
		case "sat", "saturday":
			sat = true
		case "sun", "sunday":
			sun = true
		default:
			return false, false, false, false, false, false, false, false
		}
	}
	return mon, tue, wed, thu, fri, sat, sun, true
}

// SetScheduleJSON applies an atomic set_schedule command for one slot. It
// parses the JSON payload, builds a complete ScheduleRow via a
// read-modify-write and publishes one schedule command (with a fresh ulid). On malformed JSON or a bad slot it logs a warning and
// does nothing, so a bad command is never published.
func (w *Wallbox) SetScheduleJSON(payload string) {
	var p scheduleJSONPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		fmt.Println("Warning: set_schedule: invalid JSON:", err)
		return
	}
	if p.Slot < 1 || p.Slot > 4 {
		fmt.Println("Warning: set_schedule: slot must be 1..4, got", p.Slot)
		return
	}

	s, ok := w.scheduleBySlot(p.Slot)
	if !ok {
		fmt.Println("Warning: set_schedule: unknown schedule slot", p.Slot)
		return
	}

	start, ok := ClockToHHMMInt(p.Start)
	if !ok {
		fmt.Println("Warning: set_schedule: bad start time:", p.Start)
		return
	}
	stop, ok := ClockToHHMMInt(p.Stop)
	if !ok {
		fmt.Println("Warning: set_schedule: bad stop time:", p.Stop)
		return
	}

	mon, tue, wed, thu, fri, sat, sun, ok := daysFromList(p.Days)
	if !ok {
		fmt.Println("Warning: set_schedule: unrecognised day in:", p.Days)
		return
	}

	s.StartTime = start
	s.StopTime = stop
	s.Monday, s.Tuesday, s.Wednesday = mon, tue, wed
	s.Thursday, s.Friday, s.Saturday, s.Sunday = thu, fri, sat, sun

	// enable defaults to true when omitted.
	s.Enable = true
	if p.Enable != nil {
		s.Enable = *p.Enable
	}
	// max_current is preserved when omitted; only an explicit value changes it.
	if p.MaxCurrent != nil {
		s.MaxCurrent = *p.MaxCurrent
	}
	if p.Name != nil {
		s.Name = *p.Name
	}

	w.publishScheduleUpdate(s)

	// Remember the accepted payload so the text entity has a real state
	// instead of sitting at unknown forever.
	w.scheduleMu.Lock()
	w.lastScheduleJSON = payload
	w.scheduleMu.Unlock()
}

// LastScheduleJSON returns the last set_schedule payload accepted by
// SetScheduleJSON, published as the text entity's state.
func (w *Wallbox) LastScheduleJSON() string {
	w.scheduleMu.Lock()
	defer w.scheduleMu.Unlock()
	return w.lastScheduleJSON
}

// ScheduleSlot returns the cached ScheduleRow for a slot (1..4), used by the
// schedule entity getters. The bool is false when the cache has no such slot
// yet (e.g. before the first RefreshData).
func (w *Wallbox) ScheduleSlot(slot int) (ScheduleRow, bool) {
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	r, ok := w.Data.Schedules[slot]
	return r, ok
}
