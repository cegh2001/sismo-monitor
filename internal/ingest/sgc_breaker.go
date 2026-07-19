package ingest

import (
	"fmt"
	"time"
)

// Circuit breaker states.
const (
	cbClosed   = "closed"
	cbOpen     = "open"
	cbHalfOpen = "half_open"
)

func (s *SGCScraper) recordFailure() {
	s.consecutiveFails++
	s.log("SGC circuit breaker: %d/%d consecutive failures", s.consecutiveFails, s.maxFails)
	if s.consecutiveFails >= s.maxFails {
		s.cbState = cbOpen
		s.cbOpenedAt = time.Now()
		s.log("SGC circuit breaker: OPEN — pausing requests for %v. Check SGC website for UI changes.", s.cooldownPeriod)
	}
}

func (s *SGCScraper) resetCircuitBreaker() {
	if s.cbState == cbHalfOpen {
		s.log("SGC circuit breaker: CLOSED — test request succeeded")
	} else if s.consecutiveFails > 0 {
		s.log("SGC circuit breaker: reset after %d failures", s.consecutiveFails)
	}
	s.consecutiveFails = 0
	s.cbState = cbClosed
}

func (s *SGCScraper) checkCircuitBreaker() error {
	switch s.cbState {
	case cbOpen:
		if time.Since(s.cbOpenedAt) < s.cooldownPeriod {
			return fmt.Errorf("circuit breaker OPEN: cooling down (%v remaining)",
				s.cooldownPeriod-time.Since(s.cbOpenedAt).Round(time.Second))
		}
		s.cbState = cbHalfOpen
		s.log("SGC circuit breaker: HALF_OPEN — attempting test request")
	case cbHalfOpen:
		// test attempt in progress
	}
	return nil
}
