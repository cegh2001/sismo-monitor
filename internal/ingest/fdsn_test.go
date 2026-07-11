package ingest

import (
	"strings"
	"testing"
	"time"

	"sismo-monitor/internal/geo"
)

const testQuakeML = `<?xml version="1.0" encoding="utf-8"?>
<q:quakemodel xmlns:q="http://quakeml.org/xmlns/quakeml/1.2" xmlns="http://quakeml.org/xmlns/bed/1.2">
  <eventParameters publicID="smi:local/eventParameters">
    <!-- Event 1: Inside Venezuelan box, standard format -->
    <event publicID="smi:sismo.sgc.gov.co/event/sgc2026abc">
      <description>
        <text>Frontera Colombo-Venezolana</text>
        <type>region name</type>
      </description>
      <origin publicID="smi:sismo.sgc.gov.co/origin/origin1">
        <time>
          <value>2026-07-11T12:34:56.000Z</value>
        </time>
        <latitude>
          <value>10.45</value>
        </latitude>
        <longitude>
          <value>-72.30</value>
        </longitude>
        <depth>
          <value>15500</value> 
        </depth>
      </origin>
      <magnitude publicID="smi:sismo.sgc.gov.co/magnitude/mag1">
        <mag>
          <value>4.8</value>
        </mag>
        <type>Mw</type>
      </magnitude>
      <preferredOriginID>smi:sismo.sgc.gov.co/origin/origin1</preferredOriginID>
      <preferredMagnitudeID>smi:sismo.sgc.gov.co/magnitude/mag1</preferredMagnitudeID>
    </event>
    <!-- Event 2: Outside Venezuelan box (lat=2.0) - should be skipped in ParseXML -->
    <event publicID="smi:sismo.sgc.gov.co/event/sgc2026xyz">
      <origin publicID="smi:sismo.sgc.gov.co/origin/origin2">
        <time>
          <value>2026-07-11T12:00:00Z</value>
        </time>
        <latitude>
          <value>2.00</value>
        </latitude>
        <longitude>
          <value>-74.00</value>
        </longitude>
        <depth>
          <value>10000</value>
        </depth>
      </origin>
      <magnitude publicID="smi:sismo.sgc.gov.co/magnitude/mag2">
        <mag>
          <value>3.5</value>
        </mag>
      </magnitude>
    </event>
  </eventParameters>
</q:quakemodel>`

func TestParseQuakeML(t *testing.T) {
	r := strings.NewReader(testQuakeML)
	qml, err := ParseQuakeML(r)
	if err != nil {
		t.Fatalf("ParseQuakeML failed: %v", err)
	}

	if len(qml.EventParameters.Events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(qml.EventParameters.Events))
	}

	ev1 := qml.EventParameters.Events[0]
	if ev1.PublicID != "smi:sismo.sgc.gov.co/event/sgc2026abc" {
		t.Errorf("Expected event publicID smi:sismo.sgc.gov.co/event/sgc2026abc, got %s", ev1.PublicID)
	}

	orig1 := ev1.GetPreferredOrigin()
	if orig1 == nil {
		t.Fatal("Expected non-nil preferred origin")
	}
	if orig1.Latitude.Value != 10.45 {
		t.Errorf("Expected lat 10.45, got %f", orig1.Latitude.Value)
	}
	if orig1.Depth.Value != 15500 {
		t.Errorf("Expected depth 15500, got %f", orig1.Depth.Value)
	}

	mag1 := ev1.GetPreferredMagnitude()
	if mag1 == nil {
		t.Fatal("Expected non-nil preferred magnitude")
	}
	if mag1.Mag.Value != 4.8 {
		t.Errorf("Expected mag 4.8, got %f", mag1.Mag.Value)
	}
}

func TestFDSNClientParseXML(t *testing.T) {
	client := NewFDSNClient("Colombia (SGC)", "http://mockurl", 60*time.Second, nil, nil)
	r := strings.NewReader(testQuakeML)

	events, err := client.ParseXML(r)
	if err != nil {
		t.Fatalf("ParseXML failed: %v", err)
	}

	// Only Event 1 is within Venezuelan bounding box (lat: 5.0 to 13.0, lon: -73.0 to -59.0)
	// Event 2 is lat=2.0 which is out of bounds, so it must be filtered out.
	if len(events) != 1 {
		t.Fatalf("Expected 1 event (within Venezuela), got %d", len(events))
	}

	e := events[0]
	if e.Source != "Colombia (SGC)" {
		t.Errorf("Expected source Colombia (SGC), got %s", e.Source)
	}
	if e.Magnitude != 4.8 {
		t.Errorf("Expected magnitude 4.8, got %f", e.Magnitude)
	}
	// Depth should be converted from meters (15500) to kilometers (15.5)
	if e.Depth != 15.5 {
		t.Errorf("Expected depth 15.5, got %f", e.Depth)
	}
	if e.Latitude != 10.45 {
		t.Errorf("Expected latitude 10.45, got %f", e.Latitude)
	}
	if e.Longitude != -72.30 {
		t.Errorf("Expected longitude -72.30, got %f", e.Longitude)
	}
	if e.Location != "Frontera Colombo-Venezolana" {
		t.Errorf("Expected Location 'Frontera Colombo-Venezolana', got %q", e.Location)
	}

	expectedCell := geo.GetGridCell(10.45, -72.30)
	if e.GridCell != expectedCell {
		t.Errorf("Expected GridCell %s, got %s", expectedCell, e.GridCell)
	}

	// Check unique ID generation: fdsn-colombiasgc-sgc2026abc
	if e.ID != "fdsn-colombiasgc-sgc2026abc" {
		t.Errorf("Expected ID 'fdsn-colombiasgc-sgc2026abc', got %q", e.ID)
	}
}
