package parameter

import "time"

// TrafficSample is one reading of cumulative WAN octet counters from the CPE.
type TrafficSample struct {
	RecordedAt    time.Time
	BytesSent     int64
	BytesReceived int64
}

// TrafficRatePoint is the average bitrate over the interval ending at Until
// (from the previous sample to this one). Rx/Tx are in bits per second.
type TrafficRatePoint struct {
	Until       time.Time `json:"until"`
	DurationSec float64   `json:"duration_sec"`
	RxBps       float64   `json:"rx_bps"`
	TxBps       float64   `json:"tx_bps"`
	Valid       bool      `json:"valid"`
}

// TrafficSamplesToRatePoints converts cumulative counter samples into average
// rates between consecutive samples. Counter resets (new < old) yield Valid=false
// and zero rates for that segment.
func TrafficSamplesToRatePoints(samples []TrafficSample) []TrafficRatePoint {
	if len(samples) < 2 {
		return nil
	}
	out := make([]TrafficRatePoint, 0, len(samples)-1)
	for i := 1; i < len(samples); i++ {
		prev := samples[i-1]
		cur := samples[i]
		dt := cur.RecordedAt.Sub(prev.RecordedAt).Seconds()
		if dt <= 0 {
			continue
		}
		ds := cur.BytesSent - prev.BytesSent
		dr := cur.BytesReceived - prev.BytesReceived
		valid := ds >= 0 && dr >= 0
		p := TrafficRatePoint{
			Until:       cur.RecordedAt,
			DurationSec: dt,
			Valid:       valid,
		}
		if valid {
			p.TxBps = float64(ds) * 8 / dt
			p.RxBps = float64(dr) * 8 / dt
		}
		out = append(out, p)
	}
	return out
}
