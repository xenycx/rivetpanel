package metrics

import "time"

// SpikeEvent is what one observation changed.
type SpikeEvent int

const (
	SpikeNone   SpikeEvent = iota
	SpikeOpened            // a spike started (at StartedAt)
	SpikeClosed            // the open spike ended (at EndedAt)
)

// SpikeDetector decides when host CPU is spiking. It is fed one reading
// every few seconds (5 s in the panel). A spike opens when Need consecutive
// readings are at or above Threshold and closes once readings have stayed
// below Threshold-Hysteresis for CloseAfter.
type SpikeDetector struct {
	Threshold  float64       // percent, default 80
	Need       int           // consecutive readings, default 2
	Hysteresis float64       // default 10
	CloseAfter time.Duration // default 30s

	above      int
	firstAbove time.Time
	open       bool
	startedAt  time.Time
	belowSince time.Time
	endedAt    time.Time
}

func (d *SpikeDetector) defaults() (float64, int, float64, time.Duration) {
	th, need, hy, ca := d.Threshold, d.Need, d.Hysteresis, d.CloseAfter
	if th <= 0 {
		th = 80
	}
	if need <= 0 {
		need = 2
	}
	if hy <= 0 {
		hy = 10
	}
	if ca <= 0 {
		ca = 30 * time.Second
	}
	return th, need, hy, ca
}

// Observe feeds one reading taken at t.
func (d *SpikeDetector) Observe(t time.Time, cpu float64) SpikeEvent {
	th, need, hy, ca := d.defaults()
	if !d.open {
		if cpu >= th {
			if d.above == 0 {
				d.firstAbove = t
			}
			d.above++
			if d.above >= need {
				d.open, d.startedAt, d.belowSince, d.above = true, d.firstAbove, time.Time{}, 0
				return SpikeOpened
			}
		} else {
			d.above = 0
		}
		return SpikeNone
	}
	if cpu >= th-hy {
		d.belowSince = time.Time{}
		return SpikeNone
	}
	if d.belowSince.IsZero() {
		d.belowSince = t
	}
	if t.Sub(d.belowSince) >= ca {
		d.open, d.endedAt = false, d.belowSince
		d.belowSince = time.Time{}
		return SpikeClosed
	}
	return SpikeNone
}

// Open reports whether a spike is in progress.
func (d *SpikeDetector) Open() bool { return d.open }

// StartedAt is when the current (or last) spike started.
func (d *SpikeDetector) StartedAt() time.Time { return d.startedAt }

// EndedAt is when the last closed spike ended: the first reading below the
// closing level.
func (d *SpikeDetector) EndedAt() time.Time { return d.endedAt }
