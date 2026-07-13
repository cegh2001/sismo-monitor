package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// ---- parseFamilyLocations ----

func TestParseFamilyLocations_Empty(t *testing.T) {
	if got := parseFamilyLocations(""); len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestParseFamilyLocations_Single(t *testing.T) {
	got := parseFamilyLocations("10.60,-66.93,LaGuaira")
	if len(got) != 1 {
		t.Fatalf("expected 1 location, got %d (%v)", len(got), got)
	}
	if got[0].Name != "LaGuaira" || got[0].Lat != 10.60 || got[0].Lon != -66.93 {
		t.Errorf("unexpected location: %+v", got[0])
	}
}

func TestParseFamilyLocations_Multiple(t *testing.T) {
	got := parseFamilyLocations("10.60,-66.93,LaGuaira;10.48,-66.90,Caracas;10.63,-71.64,Maracaibo")
	if len(got) != 3 {
		t.Fatalf("expected 3 locations, got %d (%v)", len(got), got)
	}
	wantNames := []string{"LaGuaira", "Caracas", "Maracaibo"}
	for i, n := range wantNames {
		if got[i].Name != n {
			t.Errorf("location %d: expected name %q, got %q", i, n, got[i].Name)
		}
	}
	if got[2].Lat != 10.63 || got[2].Lon != -71.64 {
		t.Errorf("location 2 coords wrong: %+v", got[2])
	}
}

func TestParseFamilyLocations_MalformedSkipped(t *testing.T) {
	// missing name, invalid lat, invalid lon
	got := parseFamilyLocations("10.60,-66.93,LaGuaira;notanumber,foo,BadOne;10.48,-66.90,Caracas")
	if len(got) != 2 {
		t.Fatalf("expected 2 valid locations (1 skipped), got %d (%v)", len(got), got)
	}
	if got[0].Name != "LaGuaira" || got[1].Name != "Caracas" {
		t.Errorf("unexpected locations: %v", got)
	}
}

func TestParseFamilyLocations_WhitespaceTolerated(t *testing.T) {
	got := parseFamilyLocations(" 10.60 , -66.93 , LaGuaira ; 10.48 , -66.90 , Caracas ")
	if len(got) != 2 {
		t.Fatalf("expected 2 locations with whitespace, got %d (%v)", len(got), got)
	}
	if got[0].Name != "LaGuaira" || got[1].Name != "Caracas" {
		t.Errorf("unexpected locations after whitespace trim: %v", got)
	}
}

// ---- FastPath.Process gate logic ----

func newTestFastPath(t *testing.T, magThreshold float64, rateLimit time.Duration, enabled bool, familyLocs string) (*FastPath, *int32) {
	t.Helper()
	var sendCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sendCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-test"}`))
	}))
	t.Cleanup(ts.Close)

	logger := func(format string, args ...interface{}) {}

	notifier := alert.NewPushoverNotifier("app-token-test", "user-key-test", logger)
	notifier.SetAPIURL(ts.URL)

	fp := &FastPath{
		enabled:      enabled,
		magThreshold: magThreshold,
		rateLimit:    rateLimit,
		familyLocs:   parseFamilyLocations(familyLocs),
		notifier:     notifier,
		logger:       logger,
	}
	return fp, &sendCount
}

func inBoxEvent() alert.Sismo {
	return alert.Sismo{
		ID:        "test-evt",
		Source:    "EMSC",
		Magnitude: 5.0,
		Depth:     10.0,
		Latitude:  10.5,
		Longitude: -66.9,
		Location:  "VENEZUELA",
		Time:      time.Now(),
		GridCell:  geo.GetGridCell(10.5, -66.9),
	}
}

func TestFastPath_DisabledDoesNotDispatch(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 1*time.Hour, false, "10.60,-66.93,LaGuaira")
	fp.Process(inBoxEvent())

	if atomic.LoadInt32(count) != 0 {
		t.Errorf("expected 0 dispatches when disabled, got %d", *count)
	}
}

func TestFastPath_OutOfBoxDoesNotDispatch(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 1*time.Hour, true, "10.60,-66.93,LaGuaira")
	evt := inBoxEvent()
	evt.Latitude = 35.0
	evt.Longitude = 135.0
	evt.GridCell = geo.GetGridCell(35.0, 135.0)

	fp.Process(evt)

	if atomic.LoadInt32(count) != 0 {
		t.Errorf("expected 0 dispatches out of box, got %d", *count)
	}
}

func TestFastPath_BelowMagnitudeDoesNotDispatch(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 1*time.Hour, true, "10.60,-66.93,LaGuaira")
	evt := inBoxEvent()
	evt.Magnitude = 3.0

	fp.Process(evt)

	if atomic.LoadInt32(count) != 0 {
		t.Errorf("expected 0 dispatches below threshold, got %d", *count)
	}
}

func TestFastPath_InBoxAboveMagDispatches(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 1*time.Hour, true, "10.60,-66.93,LaGuaira")
	fp.Process(inBoxEvent())

	if atomic.LoadInt32(count) != 1 {
		t.Errorf("expected 1 dispatch in-box above threshold, got %d", *count)
	}
}

func TestFastPath_AtThresholdDispatches(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 1*time.Hour, true, "10.60,-66.93,LaGuaira")
	evt := inBoxEvent()
	evt.Magnitude = 4.5 // exactly at threshold (>=)

	fp.Process(evt)

	if atomic.LoadInt32(count) != 1 {
		t.Errorf("expected dispatch at threshold (>=), got %d dispatches", *count)
	}
}

func TestFastPath_RateLimitCooldownSuppresses(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 10*time.Second, true, "10.60,-66.93,LaGuaira")

	// First call: dispatches
	fp.Process(inBoxEvent())
	// Second call within cooldown: suppressed
	fp.Process(inBoxEvent())

	if got := atomic.LoadInt32(count); got != 1 {
		t.Errorf("expected 1 dispatch (cooldown suppresses second), got %d", got)
	}
}

func TestFastPath_DisabledExitsEarlyOnMessage(t *testing.T) {
	// Verify the disabled path does not even compute a Sismo distance
	// (defensive against accidentally doing work on a hot path).
	var logLines []string
	logger := func(format string, args ...interface{}) {
		logLines = append(logLines, format)
	}
	notifier := alert.NewPushoverNotifier("app-token-test", "user-key-test", logger)
	fp := &FastPath{
		enabled:      false,
		magThreshold: 4.5,
		rateLimit:    1 * time.Hour,
		familyLocs:   parseFamilyLocations("10.60,-66.93,LaGuaira"),
		notifier:     notifier,
		logger:       logger,
	}

	fp.Process(inBoxEvent())
	if len(logLines) != 0 {
		t.Errorf("expected no log lines when disabled, got %v", logLines)
	}
}

// ---- FastPath.Start loop ----

func TestFastPath_StartExitsOnContextDone(t *testing.T) {
	fp, _ := newTestFastPath(t, 4.5, 1*time.Hour, true, "10.60,-66.93,LaGuaira")
	ctx, cancel := context.WithCancel(context.Background())
	fastOut := make(chan alert.Sismo, 1)

	done := make(chan struct{})
	go func() {
		fp.Start(ctx, fastOut)
		close(done)
	}()

	// Give the goroutine a moment to enter the select.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// good — Start returned
	case <-time.After(1 * time.Second):
		t.Fatal("FastPath.Start did not exit within 1s of ctx.Done()")
	}
}

func TestFastPath_StartProcessesEvents(t *testing.T) {
	fp, count := newTestFastPath(t, 4.5, 1*time.Hour, true, "10.60,-66.93,LaGuaira")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fastOut := make(chan alert.Sismo, 1)
	fastOut <- inBoxEvent()

	done := make(chan struct{})
	go func() {
		fp.Start(ctx, fastOut)
		close(done)
	}()

	// Wait for dispatch
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(count) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if atomic.LoadInt32(count) != 1 {
		t.Errorf("expected 1 dispatch via Start loop, got %d", *count)
	}
	cancel()
	<-done
}

// ---- Body content ----

func TestFastPath_DispatchedMessageContainsEarlyWarningAndFamilyETA(t *testing.T) {
	var capturedTitle, capturedMessage string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		capturedTitle = r.FormValue("title")
		capturedMessage = r.FormValue("message")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-eta"}`))
	}))
	defer ts.Close()

	logger := func(format string, args ...interface{}) {}
	notifier := alert.NewPushoverNotifier("app-token-test", "user-key-test", logger)
	notifier.SetAPIURL(ts.URL)

	fp := &FastPath{
		enabled:      true,
		magThreshold: 4.5,
		rateLimit:    1 * time.Hour,
		familyLocs:   parseFamilyLocations("10.60,-66.93,LaGuaira;10.48,-66.90,Caracas"),
		notifier:     notifier,
		logger:       logger,
	}

	fp.Process(inBoxEvent())

	if !strings.Contains(capturedTitle, "[EARLY WARNING]") {
		t.Errorf("expected [EARLY WARNING] in title, got %q", capturedTitle)
	}
	if !strings.Contains(capturedTitle, "M5.0") {
		t.Errorf("expected magnitude in title, got %q", capturedTitle)
	}
	if !strings.Contains(capturedMessage, "LaGuaira") {
		t.Errorf("expected LaGuaira in body, got %q", capturedMessage)
	}
	if !strings.Contains(capturedMessage, "Caracas") {
		t.Errorf("expected Caracas in body, got %q", capturedMessage)
	}
	if !strings.Contains(capturedMessage, "Onda P") {
		t.Errorf("expected P-wave ETA in body, got %q", capturedMessage)
	}
	if !strings.Contains(capturedMessage, "Onda S") {
		t.Errorf("expected S-wave ETA in body, got %q", capturedMessage)
	}
	if !strings.Contains(capturedMessage, "pending classification") {
		t.Errorf("expected 'pending classification' disclaimer in body, got %q", capturedMessage)
	}
}
