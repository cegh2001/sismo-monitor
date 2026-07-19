package llm

import (
	"sismo-monitor/internal/alert"
)

type ReportType = alert.ReportType

const (
	ReportConfirmacion  = alert.ReportConfirmacion
	ReportCalmaReajuste = alert.ReportCalmaReajuste
)

type Citation = alert.Citation
type SynthesisRequest = alert.SynthesisRequest
type SynthesisResponse = alert.SynthesisResponse
