package alert

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"time"
)

// FaultProjection holds the predictive analytics for a fault/grid cell.
type FaultProjection struct {
	GridCell           string    `json:"grid_cell"`
	FaultName          string    `json:"fault_name"`
	BValue             float64   `json:"b_value"`
	MainshockMag       float64   `json:"mainshock_mag"`
	MainshockTime      time.Time `json:"mainshock_time"`
	BathMaxReplica     float64   `json:"bath_max_replica"`
	OmoriReplicaRate   float64   `json:"omori_replica_rate"`   // expected rate at now
	ExpectedReplicas24 float64   `json:"expected_replicas_24"` // expected replicas in next 24 hours
	EventCount         int       `json:"event_count"`
}

// GetFaultName returns the fault name associated with a given latitude and longitude.
func GetFaultName(lat, lon float64) string {
	if lat < 5.0 || lat > 13.0 || lon < -73.0 || lon > -59.0 {
		return "Falla Desconocida"
	}
	if lon < -69.0 {
		return "Falla de Boconó"
	} else if lon >= -69.0 && lon < -65.5 {
		return "Falla de San Sebastián"
	} else {
		return "Falla de El Pilar"
	}
}

// CalculateBValue computes the Gutenberg-Richter b-value for a slice of magnitudes using MLE.
// Mc is the completeness magnitude (e.g. 2.5), and deltaM is the magnitude bin size (e.g. 0.1).
func CalculateBValue(magnitudes []float64, Mc float64, deltaM float64) float64 {
	var sum float64
	var count int
	for _, m := range magnitudes {
		if m >= Mc {
			sum += m
			count++
		}
	}

	if count == 0 {
		return 0.0
	}

	avgM := sum / float64(count)
	denominator := avgM - (Mc - deltaM/2.0)
	if denominator <= 0 {
		return 0.0
	}

	return math.Log10(math.E) / denominator
}

// AnalyzeGridCell computes the Gutenberg-Richter, Omori, and Bath metrics for a specific grid cell.
// It accepts only the sismos belonging to the cell, avoiding scanning the entire history.
func AnalyzeGridCell(cell string, cellSismos []Sismo, now time.Time) FaultProjection {
	var magnitudes []float64
	var mainshock Sismo
	hasMainshock := false

	for _, s := range cellSismos {
		magnitudes = append(magnitudes, s.Magnitude)
		if s.Magnitude >= 4.5 {
			if !hasMainshock || s.Magnitude > mainshock.Magnitude {
				mainshock = s
				hasMainshock = true
			}
		}
	}

	proj := FaultProjection{
		GridCell:   cell,
		EventCount: len(cellSismos),
	}

	if len(cellSismos) > 0 {
		proj.FaultName = GetFaultName(cellSismos[0].Latitude, cellSismos[0].Longitude)
	} else {
		proj.FaultName = "Falla Desconocida"
	}

	// Gutenberg-Richter b-value (Mc = 2.5, deltaMb = 0.1)
	proj.BValue = CalculateBValue(magnitudes, 2.5, 0.1)

	// Omori & Bath Calculations
	if hasMainshock {
		proj.MainshockMag = mainshock.Magnitude
		proj.MainshockTime = mainshock.Time

		// Bath's Law
		proj.BathMaxReplica = mainshock.Magnitude - 1.2
		if proj.BathMaxReplica < 0 {
			proj.BathMaxReplica = 0
		}

		// Count replicas (events in the same grid cell after mainshock)
		var replicas []Sismo
		for _, s := range cellSismos {
			if s.Time.After(mainshock.Time) {
				replicas = append(replicas, s)
			}
		}

		// Omori Law parameters: c = 0.5 hours, p = 1.0
		cVal := 0.5 // in hours
		pVal := 1.0

		// Calculate elapsed time from mainshock to now in hours
		tElapsed := now.Sub(mainshock.Time).Hours()
		if tElapsed < 0 {
			tElapsed = 0.0
		}

		nObs := float64(len(replicas))

		var k float64
		if tElapsed > 0.05 {
			k = nObs / math.Log((tElapsed+cVal)/cVal)
		} else {
			k = nObs
		}

		if k < 0 {
			k = 0
		}

		proj.OmoriReplicaRate = k / math.Pow(tElapsed+cVal, pVal)
		proj.ExpectedReplicas24 = k * math.Log((tElapsed+24.0+cVal)/(tElapsed+cVal))
	}

	return proj
}

// ComputeProjections calculates projections for all active grid cells present in the given sismos slice.
// Sismos are grouped by cell first in O(N) to optimize overall computational complexity.
func ComputeProjections(sismos []Sismo, now time.Time) []FaultProjection {
	grouped := make(map[string][]Sismo)
	for _, s := range sismos {
		if s.GridCell != "" && s.GridCell != "OUT_OF_BOUNDS" {
			grouped[s.GridCell] = append(grouped[s.GridCell], s)
		}
	}

	var cells []string
	for cell := range grouped {
		cells = append(cells, cell)
	}
	sort.Strings(cells)

	var projections []FaultProjection
	for _, cell := range cells {
		proj := AnalyzeGridCell(cell, grouped[cell], now)
		projections = append(projections, proj)
	}

	return projections
}

// LoadHistoricalSismos reads the historical sismos from database file.
func LoadHistoricalSismos(dbPath string) ([]Sismo, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return nil, err
	}
	var sismos []Sismo
	if err := json.Unmarshal(data, &sismos); err != nil {
		return nil, err
	}
	return sismos, nil
}
