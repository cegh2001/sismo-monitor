package ingest

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// sgcJSEvent represents the JSON structure extracted from the web page DOM.
type sgcJSEvent struct {
	Magnitude        string `json:"magnitude"`
	Place            string `json:"place"`
	DateTime         string `json:"dateTime"`
	Depth            string `json:"depth"`
	Latitude         string `json:"latitude"`
	Longitude        string `json:"longitude"`
	LocalizacionText string `json:"localizacionText"`
	SismoID          string `json:"sismoId"`
	Nearby           string `json:"nearby"`
}

func (s *SGCScraper) parseExtractResult(jsonStr string) ([]alert.Sismo, error) {
	var logFn func(string, ...interface{})
	if s != nil {
		logFn = s.log
	}
	return parseExtractResult(jsonStr, logFn)
}

func (s *SGCScraper) validateEvents(events []alert.Sismo) error {
	return validateEvents(events)
}

// sgcExtractScript returns JavaScript that extracts filtered seismic card data.
func sgcExtractScript() string {
	return `
(function() {
	var items = document.querySelectorAll('div.item');
	var result = [];
	for (var i = 0; i < items.length; i++) {
		var el = items[i];
		if (el.offsetParent === null) continue;

		var idAttr = el.getAttribute('id') || '';
		if (!idAttr.startsWith('item')) continue;
		
		var sismoId = idAttr.substring(4);
		var contentEl = document.getElementById('item-content' + sismoId);
		
		var magEl = el.querySelector('.magnitude');
		var magnitude = magEl ? magEl.textContent.trim() : '';
		
		var placeEl = el.querySelector('.place');
		var place = placeEl ? placeEl.textContent.trim() : '';
		
		var dateEl = el.querySelector('.date-text');
		var dateTime = dateEl ? dateEl.textContent.trim() : '';
		
		var depthEl = el.querySelector('.depth');
		var depth = depthEl ? depthEl.textContent.trim() : '';
		
		var lat = '';
		var lon = '';
		var localizacionText = '';
		var nearby = '';
		
		if (contentEl) {
			var infoTexts = contentEl.querySelectorAll('.info-text');
			for (var j = 0; j < infoTexts.length; j++) {
				var txt = infoTexts[j].textContent.trim();
				if (txt.includes('Localización:') || txt.includes('Localizacion:')) {
					localizacionText = txt;
					var parts = txt.split(':');
					if (parts.length > 1) {
						var coords = parts[1].trim().split(',');
						if (coords.length === 2) {
							lat = coords[0].trim().replace('°', '').replace('?', '').trim();
							lon = coords[1].trim().replace('°', '').replace('?', '').trim();
						}
					}
				}
				if (txt.includes('Municipios cercanos:') || txt.includes('Municipios cercanos')) {
					nearby = txt;
				}
			}
		}
		
		if ((!lat || !lon) && contentEl) {
			var contentText = contentEl.textContent;
			var coordRegex = /(-?\d+\.\d+)\s*°?\s*,\s*(-?\d+\.\d+)\s*°?/;
			var match = contentText.match(coordRegex);
			if (match) {
				lat = match[1];
				lon = match[2];
			}
		}
		
		result.push({
			magnitude: magnitude,
			place: place,
			dateTime: dateTime,
			depth: depth,
			latitude: lat,
			longitude: lon,
			localizacionText: localizacionText,
			sismoId: sismoId,
			nearby: nearby
		});
	}
	return JSON.stringify(result);
})()
`
}

func parseExtractResult(jsonStr string, logFn func(string, ...interface{})) ([]alert.Sismo, error) {
	var jsEvents []sgcJSEvent
	if err := json.Unmarshal([]byte(jsonStr), &jsEvents); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	var events []alert.Sismo
	locHLV := time.FixedZone("HLV", -5*60*60) // Colombia timezone

	for _, jsEvent := range jsEvents {
		magStr := strings.TrimSuffix(jsEvent.Magnitude, "M")
		magVal, err := strconv.ParseFloat(magStr, 64)
		if err != nil {
			if logFn != nil {
				logFn("SGC: failed to parse magnitude %q: %v", jsEvent.Magnitude, err)
			}
			continue
		}

		var depthVal float64
		depthClean := strings.ToLower(strings.TrimSuffix(jsEvent.Depth, "km"))
		if depthClean != "superficial" && depthClean != "" {
			depthVal, err = strconv.ParseFloat(depthClean, 64)
			if err != nil && logFn != nil {
				logFn("SGC: failed to parse depth %q: %v", jsEvent.Depth, err)
			}
		}

		latVal, errLat := strconv.ParseFloat(jsEvent.Latitude, 64)
		lonVal, errLon := strconv.ParseFloat(jsEvent.Longitude, 64)
		if errLat != nil || errLon != nil {
			if logFn != nil {
				logFn("SGC: failed to parse coordinates (lat: %q, lon: %q): %v %v", jsEvent.Latitude, jsEvent.Longitude, errLat, errLon)
			}
			continue
		}

		var eventTime time.Time
		layouts := []string{
			"2006-01-02 15:04:05", "2006/01/02 15:04:05",
			"2006-01-02 15:04", "2006/01/02 15:04",
			"02-01-2006 15:04:05", "02/01/2006 15:04:05",
			"02-01-2006 15:04", "02/01/2006 15:04",
		}
		for _, l := range layouts {
			if t, err := time.ParseInLocation(l, jsEvent.DateTime, locHLV); err == nil {
				eventTime = t
				break
			}
		}
		if eventTime.IsZero() {
			if logFn != nil {
				logFn("SGC: failed to parse date/time %q, using now", jsEvent.DateTime)
			}
			eventTime = time.Now().In(locHLV)
		}

		hashInput := fmt.Sprintf("sgc-%s-%.3f-%.3f-%.1f", jsEvent.DateTime, latVal, lonVal, magVal)
		hasher := md5.New()
		hasher.Write([]byte(hashInput))
		eventID := "sgc-" + hex.EncodeToString(hasher.Sum(nil))[:12]

		dist := geo.DistanceToLaGuaira(latVal, lonVal)
		locStr := jsEvent.Place
		if strings.ToLower(strings.TrimSpace(locStr)) == "venezuela" && jsEvent.Nearby != "" {
			locStr = parseSpecificLocation(locStr, jsEvent.Nearby)
		}
		if locStr == "" {
			locStr = "Colombia/Venezuela Region"
		}

		gridCell := geo.GetGridCell(latVal, lonVal)
		if gridCell == "OUT_OF_BOUNDS" {
			gridCell = "REGIONAL"
		}

		events = append(events, alert.Sismo{
			ID:        eventID,
			Source:    "SGC",
			Magnitude: magVal,
			Depth:     depthVal,
			Latitude:  latVal,
			Longitude: lonVal,
			Location:  locStr,
			Time:      eventTime,
			Distance:  dist,
			GridCell:  gridCell,
		})
	}

	return events, nil
}

func validateEvents(events []alert.Sismo) error {
	for i, e := range events {
		if e.Magnitude <= 0 || e.Magnitude > 10 {
			return fmt.Errorf("event %d: implausible magnitude %.1f", i, e.Magnitude)
		}
		if e.Latitude < -90 || e.Latitude > 90 {
			return fmt.Errorf("event %d: implausible latitude %.3f", i, e.Latitude)
		}
		if e.Longitude < -180 || e.Longitude > 180 {
			return fmt.Errorf("event %d: implausible longitude %.3f", i, e.Longitude)
		}
	}
	return nil
}

func parseSpecificLocation(place string, nearby string) string {
	place = strings.TrimSpace(place)
	nearby = strings.TrimSpace(nearby)
	if nearby == "" {
		return place
	}

	prefix := "Municipios cercanos:"
	if strings.HasPrefix(nearby, prefix) {
		nearby = strings.TrimSpace(strings.TrimPrefix(nearby, prefix))
	}

	parts := strings.Split(nearby, ",")
	if len(parts) == 0 || parts[0] == "" {
		return place
	}

	firstMun := strings.TrimSpace(parts[0])
	if strings.Contains(firstMun, "( Venezuela)") {
		firstMun = strings.ReplaceAll(firstMun, "( Venezuela)", ", Venezuela")
	}
	if strings.Contains(firstMun, " a ") {
		firstMun = strings.Replace(firstMun, " a ", " (", 1) + ")"
	}
	firstMun = strings.ReplaceAll(firstMun, " ,", ",")
	firstMun = strings.Join(strings.Fields(firstMun), " ")

	return firstMun
}
