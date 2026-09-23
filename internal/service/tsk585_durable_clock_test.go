package service

import (
	"sync"
	"time"
)

func tsk585InstallMonotonicClock(s *Service) {
	var mu sync.Mutex
	var last time.Time
	s.clock = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now().UTC()
		if !last.IsZero() && !now.After(last) {
			now = last.Add(time.Microsecond)
		}
		last = now
		return now
	}
}
