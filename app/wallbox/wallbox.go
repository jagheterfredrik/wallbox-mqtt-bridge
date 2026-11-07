package wallbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

type DataCache struct {
	// Fields polled from the 'wallbox' MySQL database.
	SQL struct {
		Lock                     int     `db:"lock"`
		ChargingEnable           int     `db:"charging_enable"`
		MaxChargingCurrent       int     `db:"max_charging_current"`
		HaloBrightness           int     `db:"halo_brightness"`
		CumulativeAddedEnergy    float64 `db:"cumulative_added_energy"`
		AddedRange               float64 `db:"added_range"`
		ActiveSessionEnergyTotal float64 `db:"active_session_energy_total"`
		DcaAccumulatedEnergy     float64 `db:"dca_accumulated_energy"`
	}

	// Fields come from the legacy 'state' Redis hash (pre-6.6 firmware).
	RedisState struct {
		SessionState   int     `redis:"session.state"`
		ControlPilot   int     `redis:"ctrlPilot"`
		S2open         int     `redis:"S2open"`
		ScheduleEnergy float64 `redis:"scheduleEnergy"`
	}

	// Fields come from the legacy 'm2w' Redis hash (pre-6.6 firmware).
	RedisM2W struct {
		ChargerStatus              int     `redis:"tms.charger_status"`
		Line1Power                 float64 `redis:"tms.line1.power_watt.value"`
		Line2Power                 float64 `redis:"tms.line2.power_watt.value"`
		Line3Power                 float64 `redis:"tms.line3.power_watt.value"`
		Line1Current               float64 `redis:"tms.line1.current_amp.value"`
		Line2Current               float64 `redis:"tms.line2.current_amp.value"`
		Line3Current               float64 `redis:"tms.line3.current_amp.value"`
		PowerBoostLine1Power       float64 `redis:"PBO.line1.power.value"`
		PowerBoostLine2Power       float64 `redis:"PBO.line2.power.value"`
		PowerBoostLine3Power       float64 `redis:"PBO.line3.power.value"`
		PowerBoostLine1Current     float64 `redis:"PBO.line1.current.value"`
		PowerBoostLine2Current     float64 `redis:"PBO.line2.current.value"`
		PowerBoostLine3Current     float64 `redis:"PBO.line3.current.value"`
		PowerBoostCumulativeEnergy float64 `redis:"PBO.energy_wh.value"`
		TempL1                     float64 `redis:"tms.line1.temp_deg.value"`
		TempL2                     float64 `redis:"tms.line2.temp_deg.value"`
		TempL3                     float64 `redis:"tms.line3.temp_deg.value"`
	}

	// StringKeys holds values read from 'wallbox:' Redis GET keys.
	// These are present across all firmware versions and serve as a fallback.
	StringKeys struct {
		StateMachineState string // wallbox:wallboxsmachine::state        e.g. "A1"
		ControlPilot      string // wallbox:wallboxsmachine::controlpilot  e.g. "A1"
	}

	// Schedules holds the current charging schedules read from the
	// 'wallbox.schedules' table, keyed by schedule_id (slot 1..4). It is
	// refreshed every poll alongside the other SQL reads so the schedule
	// entities reflect the charger's actual state, including changes made
	// from the Wallbox app. Writes never touch this table directly; they go
	// through the schedule_manager Redis command (see publishScheduleUpdate).
	Schedules map[int]ScheduleRow
}

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
}

// M2WMeasurement captures the valid flag and value from 6.8+ pub/sub M2W payloads.
type M2WMeasurement struct {
	Valid bool    `json:"valid"`
	Value float64 `json:"value"`
}

// m2wLineData captures per-phase readings (power, current, voltage, temperature)
// from the EVENT_FIRMWARE_AND_METER_READINGS payload in /wbx/micro2wallbox/events.
type m2wLineData struct {
	PowerW   M2WMeasurement `json:"power_W"`
	CurrentA M2WMeasurement `json:"current_A"`
	VoltageV M2WMeasurement `json:"voltage_V"`
	TempDeg  M2WMeasurement `json:"temp_deg"`
}

// PubSubData caches the most recent values received from all three Redis pub/sub
// channels. It is protected by Wallbox.mu and written only from the subscription
// goroutine; readers must hold mu.RLock.
type PubSubData struct {
	// --- /wbx/micro2wallbox/events (6.8+ firmware) ---
	// Payload: EVENT_FIRMWARE_AND_METER_READINGS which combines both meter readings in one payload.
	// { "body": { "internal_read": { "line_1": { "power_W": {"valid":true,"value":4489.6}, ... }, ... },
	//            "external_read": { "line_1": { "power_W": {"valid":true,"value":2665}, ... }, ... },
	//            "external_meter_status": "Detected" } }
	Internal [3]m2wLineData
	External [3]m2wLineData
	// From EVENT_EXTERNAL_METER_STATUS or EVENT_FIRMWARE_AND_METER_READINGS
	ExternalMeterStatus string

	// --- /wbx/telemetry/events (6.6+ firmware) ---
	// Payload: EVENT_SENSORS_MEASURED
	// { "body": { "sensors": [ {"id": "SENSOR_CONTROL_PILOT_STATUS", "value": 194}, ... ] } }
	ControlPilotStatus      int        // SENSOR_CONTROL_PILOT_STATUS
	StateMachine            int        // SENSOR_STATE_MACHINE
	InternalMeterEnergy     float64    // SENSOR_INTERNAL_METER_ENERGY
	InternalVoltage         [3]float64 // SENSOR_INTERNAL_METER_VOLTAGE_L1/L2/L3
	InternalCurrent         [3]float64 // SENSOR_INTERNAL_METER_CURRENT_L1/L2/L3
	Temp                    [3]float64 // SENSOR_TEMP_L1/L2/L3
	ExternalVoltage         [3]float64 // SENSOR_DCA_VOLTAGE_L1/L2/L3
	ExternalCurrent         [3]float64 // SENSOR_DCA_CURRENT_L1/L2/L3
	ExternalMeterStatusCode int        // SENSOR_EXTERNAL_METER_STATUS (int fallback; 2 = Detected)

	// --- /wbx/charger_state_machine/events (6.6+ firmware) ---
	// Payload: EVENT_CHARGING_SESSION
	// { "body": { "EnergyData": { "EnergyTotal": 4817, ... } } }
	SessionEnergy   float64 // EVENT_CHARGING_SESSION
	HasSessionEvent bool    // distinguishes "no event yet" from a legitimate 0 Wh
}

type Wallbox struct {
	redisClient *redis.Client
	sqlClient   *sqlx.DB
	Data        DataCache
	ChargerType string `db:"charger_type"`
	PartNum     string `db:"part_number"`

	IncludePowerBoost bool
	mu                sync.RWMutex // Protects PubSub, HasSensorData, HasMeterData, and the Has*Data granular flags
	dataMu            sync.RWMutex // Protects Data, HasStateHash, HasM2WHash
	PubSub            PubSubData
	HasSensorData     bool // true once EVENT_SENSORS_MEASURED received on /wbx/telemetry/events
	HasMeterData      bool // true once EVENT_FIRMWARE_AND_METER_READINGS received on /wbx/micro2wallbox/events

	// Granular flags set only when the *specific* sensor IDs for each measurement
	// group are actually received in a telemetry event. Pre-6.6 firmware may emit
	// an EVENT_SENSORS_MEASURED that contains only SENSOR_STATE_MACHINE /
	// SENSOR_CONTROL_PILOT_STATUS, leaving InternalCurrent, InternalVoltage and
	// Temp at zero. Without these flags, chargingCurrentL / chargingPowerL /
	// temperatureL would return 0 instead of falling through to the m2w hash.
	HasCurrentData         bool // true once SENSOR_INTERNAL_METER_CURRENT_L* received
	HasVoltageData         bool // true once SENSOR_INTERNAL_METER_VOLTAGE_L* received
	HasTempData            bool // true once SENSOR_TEMP_L* received
	HasExternalCurrentData bool // true once SENSOR_DCA_CURRENT_L* received
	HasExternalVoltageData bool // true once SENSOR_DCA_VOLTAGE_L* received

	// HasStateHash and HasM2WHash indicate whether the "state" and "m2w" Redis
	// hashes actually exist. They were present pre-6.6 and replaced by pub/sub
	// events in 6.6+. Always check these flags before interpreting values from
	// Data.RedisState or Data.RedisM2W to avoid treating zero defaults as real data.
	HasStateHash bool
	HasM2WHash   bool

	// Updates receives a signal whenever a pub/sub event writes new data into
	// PubSub. Buffered with capacity 1 so the goroutine never blocks: if a
	// publish is already pending the signal is coalesced.
	Updates chan struct{}

	// cancelSubscriptions cancels the context passed to the Redis pub/sub
	// goroutine, signalling it to stop reconnecting and exit cleanly.
	// Using a context rather than a chan struct{} means it works correctly
	// across multiple reconnect iterations (a closed channel cannot be reused).
	cancelSubscriptions context.CancelFunc

	// scheduleReqCounter supplies a monotonically increasing suffix for the
	// request_id of each CMD_UPDATE_SCHEDULE command (e.g. "MQTTBRIDGE#5").
	// Guarded by scheduleMu.
	scheduleReqCounter int
	scheduleMu         sync.Mutex
}

func New() *Wallbox {
	var w Wallbox

	var err error
	w.sqlClient, err = sqlx.Connect("mysql", "root:fJmExsJgmKV7cq8H@tcp(127.0.0.1:3306)/wallbox")
	if err != nil {
		panic(err)
	}

	query := "select SUBSTRING_INDEX(part_number, '-', 1) AS charger_type, part_number from charger_info;"
	if err := w.sqlClient.Get(&w, query); err != nil {
		fmt.Println("Warning: failed to read charger info from DB:", err)
	}

	w.redisClient = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	w.Updates = make(chan struct{}, 1)

	return &w
}

// notifyUpdate sends a non-blocking signal on Updates so that the bridge's
// event loop can publish changed values immediately after a pub/sub event.
// The buffer-1 channel coalesces bursts: if a notification is already pending
// the new one is dropped rather than blocking the pub/sub goroutine.
func (w *Wallbox) notifyUpdate() {
	select {
	case w.Updates <- struct{}{}:
	default:
	}
}

func getRedisFields(obj any) []string {
	var result []string
	val := reflect.ValueOf(obj)
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		result = append(result, field.Tag.Get("redis"))
	}

	return result
}

// parseHexState parses a hex state string (e.g. "A1" or "0xA1") into the
// corresponding integer (e.g. 161). The "0x" prefix is optional. Returns 0 on
// any error or empty input.
func parseHexState(s string) int {
	if s == "" {
		return 0
	}
	s = strings.TrimPrefix(s, "0x")
	v, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		return 0
	}
	return int(v)
}

func (w *Wallbox) RefreshData() {
	ctx := context.Background()

	w.dataMu.Lock()
	defer w.dataMu.Unlock()

	// --- state hash (pre-6.6) ---
	stateExists, _ := w.redisClient.Exists(ctx, "state").Result()
	w.HasStateHash = stateExists > 0
	if w.HasStateHash {
		stateRes := w.redisClient.HMGet(ctx, "state", getRedisFields(w.Data.RedisState)...)
		if stateRes.Err() != nil {
			fmt.Println("Redis HMGet 'state' error (skipping):", stateRes.Err())
		} else if err := stateRes.Scan(&w.Data.RedisState); err != nil {
			fmt.Println("Redis Scan 'state' error (skipping):", err)
		}
	}

	// --- m2w hash (pre-6.6) ---
	m2wExists, _ := w.redisClient.Exists(ctx, "m2w").Result()
	w.HasM2WHash = m2wExists > 0
	if w.HasM2WHash {
		m2wRes := w.redisClient.HMGet(ctx, "m2w", getRedisFields(w.Data.RedisM2W)...)
		if m2wRes.Err() != nil {
			fmt.Println("Redis HMGet 'm2w' error (skipping):", m2wRes.Err())
		} else if err := m2wRes.Scan(&w.Data.RedisM2W); err != nil {
			fmt.Println("Redis Scan 'm2w' error (skipping):", err)
		}
	}

	// --- wallbox: string GET keys (present in all firmware versions) ---
	// Reset first so stale values from a previous poll don't linger.
	w.Data.StringKeys.StateMachineState = ""
	w.Data.StringKeys.ControlPilot = ""

	vals, _ := w.redisClient.MGet(ctx,
		"wallbox:wallboxsmachine::state",
		"wallbox:wallboxsmachine::controlpilot",
	).Result()
	if len(vals) >= 2 {
		if v, ok := vals[0].(string); ok {
			w.Data.StringKeys.StateMachineState = v
		}
		if v, ok := vals[1].(string); ok {
			w.Data.StringKeys.ControlPilot = v
		}
	}

	// --- SQL ---
	query := "SELECT " +
		"  `wallbox_config`.`charging_enable`," +
		"  `wallbox_config`.`lock`," +
		"  `wallbox_config`.`max_charging_current`," +
		"  `wallbox_config`.`halo_brightness`," +
		"  (SELECT `charged_energy` FROM `power_outage_values` LIMIT 1) AS cumulative_added_energy," +
		"  IF(`active_session`.`unique_id` != 0," +
		"    FLOOR(`active_session`.`charged_range`)," +
		"    COALESCE(`latest_session`.`charged_range`, 0)) AS added_range," +
		"  IF(`active_session`.`unique_id` != 0," +
		"    `active_session`.`energy_total`," +
		"    0) AS active_session_energy_total " +
		"FROM `wallbox_config`," +
		"    (SELECT `unique_id`, `charged_range`, `energy_total` FROM `active_session` LIMIT 1) AS `active_session`" +
		"    LEFT JOIN (SELECT `charged_range` FROM `session` ORDER BY `id` DESC LIMIT 1) AS `latest_session` ON 1=1"
	if err := w.sqlClient.Get(&w.Data.SQL, query); err != nil {
		fmt.Println("SQL RefreshData error (skipping):", err)
	}

	if w.IncludePowerBoost {
		var dcaEnergy float64
		// dca_values can be legitimately empty, so don't panic on error.
		err := w.sqlClient.QueryRow("SELECT `acumulated_energy` FROM `dca_values` ORDER BY `id` DESC LIMIT 1").Scan(&dcaEnergy)
		if err == nil {
			w.Data.SQL.DcaAccumulatedEnergy = dcaEnergy
		}
	}

	// --- schedules ---
	// Read the 4 fixed schedule slots so the schedule entities reflect the
	// charger's actual state (including changes made from the Wallbox app).
	var rows []ScheduleRow
	scheduleQuery := "SELECT `schedule_id`, `enable`, `monday`, `tuesday`, " +
		"`wednesday`, `thursday`, `friday`, `saturday`, `sunday`, " +
		"`start_time`, `stop_time`, `max_current`, `name`, `ulid` " +
		"FROM `schedules` ORDER BY `schedule_id`"
	if err := w.sqlClient.Select(&rows, scheduleQuery); err != nil {
		fmt.Println("SQL RefreshData schedules error (skipping):", err)
	} else {
		schedules := make(map[int]ScheduleRow, len(rows))
		for _, r := range rows {
			schedules[r.ScheduleID] = r
		}
		w.Data.Schedules = schedules
	}
}

// ReadSchedules returns the 4 schedule slots ordered by schedule_id, read
// directly from the 'wallbox.schedules' table. This is used by the schedule
// Setters to base a read-modify-write on the charger's latest state.
func (w *Wallbox) ReadSchedules() []ScheduleRow {
	var rows []ScheduleRow
	query := "SELECT `schedule_id`, `enable`, `monday`, `tuesday`, " +
		"`wednesday`, `thursday`, `friday`, `saturday`, `sunday`, " +
		"`start_time`, `stop_time`, `max_current`, `name`, `ulid` " +
		"FROM `schedules` ORDER BY `schedule_id`"
	if err := w.sqlClient.Select(&rows, query); err != nil {
		fmt.Println("SQL ReadSchedules error:", err)
	}
	return rows
}

// scheduleBySlot returns the current ScheduleRow for the given slot (1..4),
// reading the latest state from MySQL. The bool is false when the slot cannot
// be read, so callers can fail safe rather than publish a command built from
// zero values.
func (w *Wallbox) scheduleBySlot(slot int) (ScheduleRow, bool) {
	for _, r := range w.ReadSchedules() {
		if r.ScheduleID == slot {
			return r, true
		}
	}
	return ScheduleRow{}, false
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

// scheduleUpdateCommand is the CMD_UPDATE_SCHEDULE message published to the
// schedule_manager on /wbx/schedule_manager/cmd. The field set and ordering
// mirror the command captured from the charger's own app.
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

// publishScheduleUpdate marshals a CMD_UPDATE_SCHEDULE command from the given
// row and publishes it on /wbx/schedule_manager/cmd. The firmware applies it
// live and persists it to MySQL, so this is the ONLY write path for schedules
// (the schedules table is never written directly). A FRESH ulid is generated
// for every update — reusing the stored one makes consumers treat the change
// as a duplicate. max_current of 0 is normalised to -1 ("use the charger's
// configured max"). type is sent as the captured string "Regular" and
// enabled_days as an empty array (the manager derives days from the per-day
// booleans).
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
	cmd.Body.Schedule.Name = s.Name
	cmd.Body.Schedule.EnabledDays = []int{}
	cmd.Body.Schedule.CreatedAt = time.Now().Unix()

	w.scheduleMu.Lock()
	w.scheduleReqCounter++
	reqID := fmt.Sprintf("MQTTBRIDGE#%d", w.scheduleReqCounter)
	w.scheduleMu.Unlock()

	cmd.Header.MessageID = "CMD_UPDATE_SCHEDULE"
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
	}
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
// read-modify-write and publishes one CMD_UPDATE_SCHEDULE (with a fresh ulid). On malformed JSON or a bad slot it logs a warning and
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
	// max_current defaults to -1 when omitted.
	if p.MaxCurrent != nil {
		s.MaxCurrent = *p.MaxCurrent
	} else {
		s.MaxCurrent = -1
	}
	if p.Name != nil {
		s.Name = *p.Name
	}

	w.publishScheduleUpdate(s)
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

func (w *Wallbox) SerialNumber() string {
	var serialNumber string
	if err := w.sqlClient.Get(&serialNumber, "SELECT `serial_num` FROM charger_info"); err != nil {
		fmt.Println("Warning: failed to read serial number:", err)
	}
	return serialNumber
}

func (w *Wallbox) FirmwareVersion() string {
	var firmware string
	err := w.sqlClient.Get(&firmware, "SELECT `software_version` FROM `charger_info` LIMIT 1")
	if err == nil && firmware != "" {
		return firmware
	}
	return "unknown"
}

func (w *Wallbox) PartNumber() string {
	return w.PartNum
}

func (w *Wallbox) UserId() string {
	var userId string
	if err := w.sqlClient.QueryRow("SELECT `user_id` FROM `users` WHERE `user_id` != 1 ORDER BY `user_id` DESC LIMIT 1").Scan(&userId); err != nil {
		fmt.Println("Warning: failed to read user ID:", err)
	}
	return userId
}

func (w *Wallbox) AvailableCurrent() int {
	var availableCurrent int
	if err := w.sqlClient.QueryRow("SELECT `max_avbl_current` FROM `state_values` ORDER BY `id` DESC LIMIT 1").Scan(&availableCurrent); err != nil {
		fmt.Println("Warning: failed to read available current:", err)
	}
	return availableCurrent
}

func sendToPosixQueue(path, data string) {
	pathBytes := append([]byte(path), 0)
	mq := mqOpen(pathBytes)

	event := []byte(data)
	padding := max(1024-len(event), 0)
	eventPaddedBytes := append(event, bytes.Repeat([]byte{0x00}, padding)...)

	mqTimedsend(mq, eventPaddedBytes)
	mqClose(mq)
}

func (w *Wallbox) SetLocked(lock int) {
	w.RefreshData()
	w.dataMu.RLock()
	currentLock := w.Data.SQL.Lock
	w.dataMu.RUnlock()
	if lock == currentLock {
		return
	}
	if w.ChargerType == "CPB1" {
		if _, err := w.sqlClient.Exec("UPDATE `wallbox_config` SET `lock`=?", lock); err != nil {
			fmt.Println("SQL error in SetLocked:", err)
		}
	} else if lock == 1 {
		sendToPosixQueue("WALLBOX_MYWALLBOX_WALLBOX_LOGIN", "EVENT_REQUEST_LOCK")
	} else {
		userId := w.UserId()
		sendToPosixQueue("WALLBOX_MYWALLBOX_WALLBOX_LOGIN", "EVENT_REQUEST_LOGIN#"+userId+".000000")
	}
}

func (w *Wallbox) SetChargingEnable(enable int) {
	w.RefreshData()
	if enable == w.ChargingEnable() {
		return
	}
	if enable == 1 {
		sendToPosixQueue("WALLBOX_MYWALLBOX_WALLBOX_STATEMACHINE", "EVENT_REQUEST_USER_ACTION#1.000000")
	} else {
		sendToPosixQueue("WALLBOX_MYWALLBOX_WALLBOX_STATEMACHINE", "EVENT_REQUEST_USER_ACTION#2.000000")
	}
}

func (w *Wallbox) SetMaxChargingCurrent(current int) {
	if _, err := w.sqlClient.Exec("UPDATE `wallbox_config` SET `max_charging_current`=?", current); err != nil {
		fmt.Println("SQL error in SetMaxChargingCurrent:", err)
	}
}

func (w *Wallbox) SetHaloBrightness(brightness int) {
	if _, err := w.sqlClient.Exec("UPDATE `wallbox_config` SET `halo_brightness`=?", brightness); err != nil {
		fmt.Println("SQL error in SetHaloBrightness:", err)
	}
}

// cpConnected returns true when a CP status integer indicates a car is
// physically on the cable. States B1 and above (connected, paused, charging,
// locked, error) all have a car attached; only A1/A2 do not.
func cpConnected(cp int) bool {
	switch cp {
	case 0xB1, 0xB2, 0xC1, 0xC2, 0xC3, 0xD1, 0xD2: // 177,178,193,194,195,209,210
		return true
	default:
		return false
	}
}

// cpCharging returns true when the CP status indicates the relay is closed
// (S2 conducting). Only C1/C2/C3 (193/194/195) indicate active current flow.
func cpCharging(cp int) bool {
	return cp == 0xC1 || cp == 0xC2 || cp == 0xC3
}

// statusFromStateCode maps a state machine integer to a human-readable status
// string via stateOverrides → wallboxStatusCodes. Returns ("", false) if the
// code is not mapped.
func statusFromStateCode(state int) (string, bool) {
	if idx, ok := stateOverrides[state]; ok && idx >= 0 && idx < len(wallboxStatusCodes) {
		return wallboxStatusCodes[idx], true
	}
	return "", false
}

// effectiveStateMachine returns the best available state machine integer,
// working through the priority chain:
//  1. pub/sub sensor data (HasSensorData)
//  2. state Redis hash field (zero-guarded)
//  3. wallbox:wallboxsmachine::state string key
//
// Returns 0 when no source has a usable value.
func (w *Wallbox) effectiveStateMachine() int {
	w.mu.RLock()
	hasTelemetry := w.HasSensorData
	liveSM := w.PubSub.StateMachine
	w.mu.RUnlock()

	if hasTelemetry && liveSM > 0 {
		return liveSM
	}

	w.dataMu.RLock()
	defer w.dataMu.RUnlock()

	if state := w.Data.RedisState.SessionState; state > 0 {
		return state
	}
	if state := parseHexState(w.Data.StringKeys.StateMachineState); state > 0 {
		return state
	}
	return 0
}

// effectiveCP returns the best available CP status integer, working through
// the priority chain:
//  1. pub/sub sensor data (HasSensorData)
//  2. state Redis hash field (zero-guarded, so HasStateHash not needed)
//  3. wallbox:wallboxsmachine::controlpilot string key
//
// Returns 0 when no source has a usable value.
func (w *Wallbox) effectiveCP() int {
	w.mu.RLock()
	hasSensorData := w.HasSensorData
	liveCP := w.PubSub.ControlPilotStatus
	w.mu.RUnlock()

	if hasSensorData && liveCP > 0 {
		return liveCP
	}

	w.dataMu.RLock()
	defer w.dataMu.RUnlock()

	if redisCP := w.Data.RedisState.ControlPilot; redisCP > 0 {
		return redisCP
	}
	if cp := parseHexState(w.Data.StringKeys.ControlPilot); cp > 0 {
		return cp
	}
	return 0
}

func (w *Wallbox) CableConnected() int {
	// Priority 1–3: use CP status when available (pub/sub → hash → string key).
	if cp := w.effectiveCP(); cp > 0 {
		if cpConnected(cp) {
			return 1
		}
		return 0
	}

	// Priority 4: m2w hash ChargerStatus.
	// Original logic: status 0 (Ready) or 6 (Locked) → not connected.
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()

	if w.HasM2WHash {
		status := w.Data.RedisM2W.ChargerStatus
		if status != 0 && status != 6 {
			return 1
		}
		return 0
	}

	return 0
}

func (w *Wallbox) EffectiveStatus() string {
	w.mu.RLock()
	hasSensorData := w.HasSensorData
	stateMachine := w.PubSub.StateMachine
	w.mu.RUnlock()

	// Priority 1: pub/sub telemetry (6.6+).
	if hasSensorData && stateMachine > 0 {
		if s, ok := statusFromStateCode(stateMachine); ok {
			return s
		}
		return fmt.Sprintf("Unknown (%d)", stateMachine)
	}

	w.dataMu.RLock()
	defer w.dataMu.RUnlock()

	// Priority 2: Redis hashes (pre-6.6).
	// stateOverrides lookup on SessionState=0 (absent hash) is a no-op, so
	// HasStateHash is not needed; HasM2WHash guards the whole block so that a
	// missing m2w hash (ChargerStatus=0) doesn't silently return "Ready".
	if w.HasM2WHash {
		tmsStatus := w.Data.RedisM2W.ChargerStatus
		if override, ok := stateOverrides[w.Data.RedisState.SessionState]; ok {
			tmsStatus = override
		}
		if tmsStatus >= 0 && tmsStatus < len(wallboxStatusCodes) {
			return wallboxStatusCodes[tmsStatus]
		}
	}

	// Priority 3: wallbox:wallboxsmachine::state string key.
	if w.Data.StringKeys.StateMachineState != "" {
		state := parseHexState(w.Data.StringKeys.StateMachineState)
		if s, ok := statusFromStateCode(state); ok {
			return s
		}
	}

	return "Unknown"
}

func (w *Wallbox) ControlPilotStatus() string {
	if cp := w.effectiveCP(); cp > 0 {
		desc, ok := controlPilotStates[cp]
		if !ok {
			desc = "Unknown"
		}
		return fmt.Sprintf("%d: %s", cp, desc)
	}
	return "Unknown"
}

func (w *Wallbox) StateMachineState() string {
	state := w.effectiveStateMachine()
	if state > 0 {
		if desc, ok := stateMachineStates[state]; ok {
			return fmt.Sprintf("%d: %s", state, desc)
		}
		return fmt.Sprintf("%d: Unknown", state)
	}
	return "Unknown"
}

func (w *Wallbox) AddedEnergy() float64 {
	w.mu.RLock()
	hasSessionEvent := w.PubSub.HasSessionEvent
	sessionEnergy := w.PubSub.SessionEnergy
	w.mu.RUnlock()

	// Priority 1: pub/sub session event (6.6+).
	if hasSessionEvent {
		return sessionEnergy
	}

	w.dataMu.RLock()
	defer w.dataMu.RUnlock()

	// Priority 2: state hash (pre-6.6). 0 is a valid value at session start.
	if w.HasStateHash {
		return w.Data.RedisState.ScheduleEnergy
	}

	// Priority 3: SQL active session.
	return w.Data.SQL.ActiveSessionEnergyTotal
}

func (w *Wallbox) ChargingEnable() int {
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return w.Data.SQL.ChargingEnable
}

func (w *Wallbox) ChargingPower() float64 {
	return w.ChargingPowerL1() + w.ChargingPowerL2() + w.ChargingPowerL3()
}

// chargingPowerL returns the charger's power draw on one phase (0-indexed).
//
// When HasMeterData is true but the measurement is marked invalid (e.g. phase not in
// use on a single-phase unit), return 0 rather than falling through to the hash.
func (w *Wallbox) chargingPowerL(phase int) float64 {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasVoltageData := w.HasVoltageData
	hasCurrentData := w.HasCurrentData
	internal := w.PubSub.Internal[phase].PowerW
	voltage := w.PubSub.InternalVoltage[phase]
	current := w.PubSub.InternalCurrent[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if internal.Valid {
			return internal.Value
		}
		return 0
	}
	if hasVoltageData && hasCurrentData {
		return voltage * current
	}
	// 6.6- legacy Redis hash; only used when neither M2W nor telemetry active.
	// If the hash is absent, all fields are zero, so no HasM2WHash guard needed.
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return [3]float64{
		w.Data.RedisM2W.Line1Power,
		w.Data.RedisM2W.Line2Power,
		w.Data.RedisM2W.Line3Power,
	}[phase]
}

func (w *Wallbox) ChargingPowerL1() float64 { return w.chargingPowerL(0) }
func (w *Wallbox) ChargingPowerL2() float64 { return w.chargingPowerL(1) }
func (w *Wallbox) ChargingPowerL3() float64 { return w.chargingPowerL(2) }

// chargingCurrentL — same HasMeterData-invalid-means-zero semantics as chargingPowerL.
func (w *Wallbox) chargingCurrentL(phase int) float64 {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasCurrentData := w.HasCurrentData
	internal := w.PubSub.Internal[phase].CurrentA
	current := w.PubSub.InternalCurrent[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if internal.Valid {
			return internal.Value
		}
		return 0
	}
	if hasCurrentData {
		return current
	}
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return [3]float64{
		w.Data.RedisM2W.Line1Current,
		w.Data.RedisM2W.Line2Current,
		w.Data.RedisM2W.Line3Current,
	}[phase]
}

func (w *Wallbox) ChargingCurrentL1() float64 { return w.chargingCurrentL(0) }
func (w *Wallbox) ChargingCurrentL2() float64 { return w.chargingCurrentL(1) }
func (w *Wallbox) ChargingCurrentL3() float64 { return w.chargingCurrentL(2) }

func (w *Wallbox) CumulativeAddedEnergy() float64 {
	w.mu.RLock()
	energy := w.PubSub.InternalMeterEnergy
	w.mu.RUnlock()

	if energy > 0 {
		// The charged_energy fallback below is an unsigned int, so truncate to
		// whole Wh to keep this monotonic when it switches between the two.
		return math.Floor(energy)
	}
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return w.Data.SQL.CumulativeAddedEnergy
}

func (w *Wallbox) MaxChargingCurrent() int {
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return w.Data.SQL.MaxChargingCurrent
}

// ReadingState says whether a measurement can be shown.
type ReadingState int

const (
	// ReadingOK: the value is a real reading.
	ReadingOK ReadingState = iota
	// ReadingMissing: no source has delivered the measurement yet, e.g. right
	// after startup, before the first telemetry event.
	ReadingMissing
	// ReadingInvalid: the meter readings event flags the measurement invalid.
	ReadingInvalid
)

// TemperatureReading — internal temperature per phase, with the same source
// priority as chargingPowerL: the meter readings event, then telemetry, then
// the legacy m2w hash.
func (w *Wallbox) TemperatureReading(phase int) (float64, ReadingState) {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasTempData := w.HasTempData
	internal := w.PubSub.Internal[phase].TempDeg
	temp := w.PubSub.Temp[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if internal.Valid {
			return internal.Value, ReadingOK
		}
		return 0, ReadingInvalid
	}
	if hasTempData {
		return temp, ReadingOK
	}
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	if !w.HasM2WHash {
		return 0, ReadingMissing
	}
	return [3]float64{
		w.Data.RedisM2W.TempL1,
		w.Data.RedisM2W.TempL2,
		w.Data.RedisM2W.TempL3,
	}[phase], ReadingOK
}

// temperatureL — TemperatureReading as a plain value, 0 when there is none.
func (w *Wallbox) temperatureL(phase int) float64 {
	v, _ := w.TemperatureReading(phase)
	return v
}

func (w *Wallbox) TemperatureL1() float64 { return w.temperatureL(0) }
func (w *Wallbox) TemperatureL2() float64 { return w.temperatureL(1) }
func (w *Wallbox) TemperatureL3() float64 { return w.temperatureL(2) }

// voltageL — internal (charger) voltage per phase.
// Same HasMeterData-invalid-means-zero semantics as chargingPowerL, and the
// same source priority as powerBoostVoltageL: the 6.8+ meter readings event
// carries internal_read.line_N.voltage_V, so prefer it over the 6.6+
// SENSOR_INTERNAL_METER_VOLTAGE_L* telemetry. Without the HasMeterData branch
// these sensors read 0 on firmware that emits the meter readings event but not
// the voltage sensor IDs, even though every sibling accessor works.
func (w *Wallbox) VoltageReading(phase int) (float64, ReadingState) {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasVoltageData := w.HasVoltageData
	internal := w.PubSub.Internal[phase].VoltageV
	voltage := w.PubSub.InternalVoltage[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if internal.Valid {
			return internal.Value, ReadingOK
		}
		return 0, ReadingInvalid
	}
	if hasVoltageData {
		return voltage, ReadingOK
	}
	return 0, ReadingMissing
}

// voltageL — VoltageReading as a plain value, 0 when there is none.
func (w *Wallbox) voltageL(phase int) float64 {
	v, _ := w.VoltageReading(phase)
	return v
}

func (w *Wallbox) VoltageL1() float64 { return w.voltageL(0) }
func (w *Wallbox) VoltageL2() float64 { return w.voltageL(1) }
func (w *Wallbox) VoltageL3() float64 { return w.voltageL(2) }

// powerBoostPowerL — external (house) power per phase.
// Same HasMeterData-invalid-means-zero semantics as chargingPowerL.
func (w *Wallbox) powerBoostPowerL(phase int) float64 {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasExtVoltage := w.HasExternalVoltageData
	hasExtCurrent := w.HasExternalCurrentData
	external := w.PubSub.External[phase].PowerW
	voltage := w.PubSub.ExternalVoltage[phase]
	current := w.PubSub.ExternalCurrent[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if external.Valid {
			return external.Value
		}
		return 0
	}
	if hasExtVoltage && hasExtCurrent {
		return voltage * current
	}
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return [3]float64{
		w.Data.RedisM2W.PowerBoostLine1Power,
		w.Data.RedisM2W.PowerBoostLine2Power,
		w.Data.RedisM2W.PowerBoostLine3Power,
	}[phase]
}

func (w *Wallbox) PowerBoostPowerL1() float64 { return w.powerBoostPowerL(0) }
func (w *Wallbox) PowerBoostPowerL2() float64 { return w.powerBoostPowerL(1) }
func (w *Wallbox) PowerBoostPowerL3() float64 { return w.powerBoostPowerL(2) }

func (w *Wallbox) powerBoostCurrentL(phase int) float64 {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasExtCurrent := w.HasExternalCurrentData
	external := w.PubSub.External[phase].CurrentA
	current := w.PubSub.ExternalCurrent[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if external.Valid {
			return external.Value
		}
		return 0
	}
	if hasExtCurrent {
		return current
	}
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return [3]float64{
		w.Data.RedisM2W.PowerBoostLine1Current,
		w.Data.RedisM2W.PowerBoostLine2Current,
		w.Data.RedisM2W.PowerBoostLine3Current,
	}[phase]
}

func (w *Wallbox) PowerBoostCurrentL1() float64 { return w.powerBoostCurrentL(0) }
func (w *Wallbox) PowerBoostCurrentL2() float64 { return w.powerBoostCurrentL(1) }
func (w *Wallbox) PowerBoostCurrentL3() float64 { return w.powerBoostCurrentL(2) }

// PowerBoostVoltageReading — external (house) voltage per phase.
func (w *Wallbox) PowerBoostVoltageReading(phase int) (float64, ReadingState) {
	w.mu.RLock()
	hasMeterData := w.HasMeterData
	hasExtVoltage := w.HasExternalVoltageData
	external := w.PubSub.External[phase].VoltageV
	voltage := w.PubSub.ExternalVoltage[phase]
	w.mu.RUnlock()

	if hasMeterData {
		if external.Valid {
			return external.Value, ReadingOK
		}
		return 0, ReadingInvalid
	}
	if hasExtVoltage {
		return voltage, ReadingOK
	}
	return 0, ReadingMissing
}

// powerBoostVoltageL — PowerBoostVoltageReading as a plain value, 0 when
// there is none.
func (w *Wallbox) powerBoostVoltageL(phase int) float64 {
	v, _ := w.PowerBoostVoltageReading(phase)
	return v
}

func (w *Wallbox) PowerBoostVoltageL1() float64 { return w.powerBoostVoltageL(0) }
func (w *Wallbox) PowerBoostVoltageL2() float64 { return w.powerBoostVoltageL(1) }
func (w *Wallbox) PowerBoostVoltageL3() float64 { return w.powerBoostVoltageL(2) }

func (w *Wallbox) ExternalMeterStatus() string {
	w.mu.RLock()
	hasM2W := w.HasMeterData
	externalMeterStatus := w.PubSub.ExternalMeterStatus
	hasSensorData := w.HasSensorData
	telemExternalMeterStatus := w.PubSub.ExternalMeterStatusCode
	w.mu.RUnlock()

	// Best: string from EVENT_EXTERNAL_METER_STATUS or EVENT_FIRMWARE_AND_METER_READINGS (6.8+).
	if hasM2W && externalMeterStatus != "" {
		return externalMeterStatus
	}
	// Fallback: SENSOR_EXTERNAL_METER_STATUS int (6.6+; 2 = Detected).
	if hasSensorData && telemExternalMeterStatus > 0 {
		if telemExternalMeterStatus == 2 {
			return "Detected"
		}
		return fmt.Sprintf("Code %d", telemExternalMeterStatus)
	}
	return ""
}

func (w *Wallbox) PowerBoostCumulativeEnergy() float64 {
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	if w.Data.RedisM2W.PowerBoostCumulativeEnergy > 0 {
		return w.Data.RedisM2W.PowerBoostCumulativeEnergy
	}
	return w.Data.SQL.DcaAccumulatedEnergy
}

func (w *Wallbox) M2WStatus() int {
	sm := w.effectiveStateMachine()
	if sm > 0 {
		return sm
	}
	// Fallback: m2w hash (pre-6.6) returns the 0–18 status index.
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	if w.HasM2WHash {
		return w.Data.RedisM2W.ChargerStatus
	}
	return 0
}

func (w *Wallbox) S2Open() int {
	if cp := w.effectiveCP(); cp > 0 {
		if cpCharging(cp) {
			return 0
		}
		return 1
	}
	w.dataMu.RLock()
	defer w.dataMu.RUnlock()
	return w.Data.RedisState.S2open
}

func (w *Wallbox) StartRedisSubscriptions() {
	channels := []string{
		"/wbx/telemetry/events",
		"/wbx/micro2wallbox/events",
		"/wbx/charger_state_machine/events",
	}

	ctx, cancel := context.WithCancel(context.Background())
	w.cancelSubscriptions = cancel

	go func() {
		for {
			// (Re)subscribe. On the first iteration this is the initial
			// connection; on subsequent iterations it is a reconnect after
			// an unexpected Redis drop.
			pubsub := w.redisClient.Subscribe(ctx, channels...)

			for msg := range pubsub.Channel() {
				switch msg.Channel {
				case "/wbx/telemetry/events":
					w.processTelemetryEvent(msg.Payload)
				case "/wbx/micro2wallbox/events":
					w.processMicro2WallboxEvent(msg.Payload)
				case "/wbx/charger_state_machine/events":
					w.processSessionUpdateEvent(msg.Payload)
				}
			}

			// Channel drained. Check whether we were asked to stop.
			if ctx.Err() != nil {
				// StopRedisSubscriptions cancelled the context — exit cleanly.
				pubsub.Close()
				return
			}

			// Unexpected close (Redis restart, network drop, etc.).
			// Clear the live-data flags immediately so the main loop falls back
			// to SQL polling and stops broadcasting frozen PubSub values.
			// Then wait briefly and loop to re-subscribe, restoring real-time
			// updates without requiring a full process restart.
			fmt.Println("Warning: Redis pub/sub closed unexpectedly — clearing live-data flags and reconnecting in 5s")
			w.mu.Lock()
			w.HasSensorData = false
			w.HasMeterData = false
			w.HasCurrentData = false
			w.HasVoltageData = false
			w.HasTempData = false
			w.HasExternalCurrentData = false
			w.HasExternalVoltageData = false
			w.mu.Unlock()
			pubsub.Close()

			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				// continue loop → re-subscribe
			}
		}
	}()
}

func (w *Wallbox) StopRedisSubscriptions() {
	if w.cancelSubscriptions != nil {
		w.cancelSubscriptions()
	}
}

// processTelemetryEvent handles /wbx/telemetry/events.
// Payload example:
// {"body":{"sensors":[{"id":"SENSOR_CONTROL_PILOT_STATUS","value":194},...]},"header":{"message_id":"EVENT_SENSORS_MEASURED",...}}
func (w *Wallbox) processTelemetryEvent(payload string) {
	var event struct {
		Body struct {
			Sensors []struct {
				ID    string  `json:"id"`
				Value float64 `json:"value"`
			} `json:"sensors"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.HasSensorData = true

	for _, s := range event.Body.Sensors {
		switch s.ID {
		case "SENSOR_CONTROL_PILOT_STATUS":
			w.PubSub.ControlPilotStatus = int(s.Value)
		case "SENSOR_STATE_MACHINE":
			w.PubSub.StateMachine = int(s.Value)
		case "SENSOR_TEMP_L1":
			w.PubSub.Temp[0] = s.Value
			w.HasTempData = true
		case "SENSOR_TEMP_L2":
			w.PubSub.Temp[1] = s.Value
			w.HasTempData = true
		case "SENSOR_TEMP_L3":
			w.PubSub.Temp[2] = s.Value
			w.HasTempData = true
		case "SENSOR_INTERNAL_METER_ENERGY":
			w.PubSub.InternalMeterEnergy = s.Value
		case "SENSOR_EXTERNAL_METER_STATUS":
			w.PubSub.ExternalMeterStatusCode = int(s.Value)

		case "SENSOR_INTERNAL_METER_VOLTAGE_L1":
			w.PubSub.InternalVoltage[0] = s.Value
			w.HasVoltageData = true
		case "SENSOR_INTERNAL_METER_VOLTAGE_L2":
			w.PubSub.InternalVoltage[1] = s.Value
			w.HasVoltageData = true
		case "SENSOR_INTERNAL_METER_VOLTAGE_L3":
			w.PubSub.InternalVoltage[2] = s.Value
			w.HasVoltageData = true
		case "SENSOR_INTERNAL_METER_CURRENT_L1":
			w.PubSub.InternalCurrent[0] = s.Value
			w.HasCurrentData = true
		case "SENSOR_INTERNAL_METER_CURRENT_L2":
			w.PubSub.InternalCurrent[1] = s.Value
			w.HasCurrentData = true
		case "SENSOR_INTERNAL_METER_CURRENT_L3":
			w.PubSub.InternalCurrent[2] = s.Value
			w.HasCurrentData = true

		case "SENSOR_DCA_VOLTAGE_L1":
			w.PubSub.ExternalVoltage[0] = s.Value
			w.HasExternalVoltageData = true
		case "SENSOR_DCA_VOLTAGE_L2":
			w.PubSub.ExternalVoltage[1] = s.Value
			w.HasExternalVoltageData = true
		case "SENSOR_DCA_VOLTAGE_L3":
			w.PubSub.ExternalVoltage[2] = s.Value
			w.HasExternalVoltageData = true
		case "SENSOR_DCA_CURRENT_L1":
			w.PubSub.ExternalCurrent[0] = s.Value
			w.HasExternalCurrentData = true
		case "SENSOR_DCA_CURRENT_L2":
			w.PubSub.ExternalCurrent[1] = s.Value
			w.HasExternalCurrentData = true
		case "SENSOR_DCA_CURRENT_L3":
			w.PubSub.ExternalCurrent[2] = s.Value
			w.HasExternalCurrentData = true
		}
	}
	w.notifyUpdate()
}

// processMicro2WallboxEvent handles /wbx/micro2wallbox/events.
// Payload examples:
// EVENT_EXTERNAL_METER_STATUS: {"body":{"status":"Detected"},"header":{...}}
// EVENT_FIRMWARE_AND_METER_READINGS: {"body":{"internal_read":{"line_1":{"power_W":{"valid":true,"value":4489.6},...},...},...},...}
func (w *Wallbox) processMicro2WallboxEvent(payload string) {
	var event struct {
		Header struct {
			MessageID string `json:"message_id"`
		} `json:"header"`
		Body struct {
			// EVENT_EXTERNAL_METER_STATUS
			Status string `json:"status"`
			// EVENT_FIRMWARE_AND_METER_READINGS
			ExternalMeterStatus string `json:"external_meter_status"`
			InternalRead        struct {
				Line1 m2wLineData `json:"line_1"`
				Line2 m2wLineData `json:"line_2"`
				Line3 m2wLineData `json:"line_3"`
			} `json:"internal_read"`
			ExternalRead struct {
				Line1 m2wLineData `json:"line_1"`
				Line2 m2wLineData `json:"line_2"`
				Line3 m2wLineData `json:"line_3"`
			} `json:"external_read"`
		} `json:"body"`
	}

	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return
	}

	switch event.Header.MessageID {
	case "EVENT_EXTERNAL_METER_STATUS":
		w.mu.Lock()
		w.PubSub.ExternalMeterStatus = event.Body.Status
		w.mu.Unlock()
		w.notifyUpdate()

	case "EVENT_FIRMWARE_AND_METER_READINGS":
		w.mu.Lock()
		defer w.mu.Unlock()
		w.HasMeterData = true

		b := event.Body
		w.PubSub.Internal[0] = b.InternalRead.Line1
		w.PubSub.Internal[1] = b.InternalRead.Line2
		w.PubSub.Internal[2] = b.InternalRead.Line3

		w.PubSub.External[0] = b.ExternalRead.Line1
		w.PubSub.External[1] = b.ExternalRead.Line2
		w.PubSub.External[2] = b.ExternalRead.Line3

		if b.ExternalMeterStatus != "" {
			w.PubSub.ExternalMeterStatus = b.ExternalMeterStatus
		}
		w.notifyUpdate()
	}
}

// processSessionUpdateEvent handles /wbx/charger_state_machine/events.
// Payload example:
// {"body":{"EnergyData":{"EnergyTotal":4817,...}},"header":{"message_id":"EVENT_CHARGING_SESSION",...}}
func (w *Wallbox) processSessionUpdateEvent(payload string) {
	var event struct {
		Header struct {
			MessageID string `json:"message_id"`
		} `json:"header"`
		Body struct {
			EnergyData struct {
				EnergyTotal float64 `json:"EnergyTotal"`
			} `json:"EnergyData"`
		} `json:"body"`
	}

	if err := json.Unmarshal([]byte(payload), &event); err != nil || event.Header.MessageID != "EVENT_CHARGING_SESSION" {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.PubSub.SessionEnergy = event.Body.EnergyData.EnergyTotal
	w.PubSub.HasSessionEvent = true
	w.notifyUpdate()
}
