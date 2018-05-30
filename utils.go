package longpoll

import "time"

func MillisecondEpoch(now time.Time) int64 {
	return now.UnixNano() / int64(time.Millisecond)
}
