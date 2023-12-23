package ratelimit

import (
	"math"
	"time"
)

type DeltaRateLimit struct {
	lastTime    time.Time
	lastValue   float64
	interval    time.Duration
	valueChange float64
}

func NewDeltaRateLimit(interval time.Duration, valueChange float64) *DeltaRateLimit {
	return &DeltaRateLimit{
		interval:    interval * time.Second,
		valueChange: valueChange,
	}
}

func (c *DeltaRateLimit) Allow(value float64) bool {
	now := time.Now()

	if math.Abs(value-c.lastValue) < c.valueChange {
		if now.Sub(c.lastTime) < c.interval {
			return false
		}
	}

	c.lastTime = now
	c.lastValue = value

	return true
}
