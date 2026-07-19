# Plan de Implementación: Integración de Gemma 4 31B IT + Google Search Grounding para Análisis en Lenguaje Natural

> **Verificado contra Gemini API — Julio 2026.** Modelo, SDK y Search Grounding confirmados funcionales en Google AI Studio.

## 1. Visión General

Dotar a `sismo-monitor` de un sintetizador narrativo automático basado en el modelo **gemma-4-31b-it** (Gemini API) equipado con **Google Search Grounding**.

El sistema no utiliza IA para predecir sismos — esa tarea es exclusiva del motor determinista en Go. Responde a la activación de triggers de alerta (*Inestabilidad Cortical*, *Enjambres*, *Estrés Crítico*) generando explicaciones contextualizadas en lenguaje natural con verificación de fuentes en tiempo real vía búsqueda web.

### Por qué Gemma 4 (no Gemini)

Gemma 4 es un modelo abierto (*open-weight*) construido sobre tecnología Gemini. Google AI Studio lo ofrece con **Search Grounding habilitado por defecto** justamente para compensar su menor base de conocimiento respecto a Gemini, permitiéndole buscar y citar fuentes en tiempo real. Además:

- **Costo:** Free tier — sin costo de input, output ni context caching.
- **Razonamiento:** Soporta `ThinkingConfig` con nivel `HIGH` para análisis sismológico encadenado.
- **Latencia:** Adecuada para reportes asíncronos (no bloquea el ciclo de eventos).

> ⚠️ **Free tier notice:** Los datos enviados al free tier de Gemma 4 pueden ser utilizados para mejorar productos de Google. Para un monitor de sismos que ingiere datos públicos de USGS/EMSC/FUNVISIS esto no representa un riesgo de privacidad.

---

## 2. Arquitectura de Integración

```
 [ Sismo / Telemetría ]
          │
          ▼
┌───────────────────────────┐
│ Motor Determinista en Go  │
│ (GapAnalyzer / Predictive)│
└─────────┬─────────────────┘
          │
          ▼
┌───────────────────────────┐
│  Coordinator              │
│  (cmd/monitor/            │
│   coordinator.go)         │
│                           │
│  EmitGapSnapshot()        │
│  └─ shouldNotifyOnTrans() │ ← Trigger de fase detectado
│     └─ GemmaSynthesizer   │
└─────────┬─────────────────┘
          │ SynthesisRequest
          ▼
┌───────────────────────────┐
│  GemmaSynthesizer Client  │
│  (internal/llm/gemma.go)  │
│                           │
│  • prompt.go (templates)  │
│  • types.go  (structs)    │
└─────────┬─────────────────┘
          │ genai.GenerateContent + GoogleSearch tool
          ▼
┌───────────────────────────┐
│  Gemini API               │
│  Model: gemma-4-31b-it    │
│  Tool:  GoogleSearch      │
│  Think: HIGH              │
└─────────┬─────────────────┘
          │ Reporte Narrativo + Citations
          ├──────────────────────────┐
          ▼                          ▼
┌───────────────────┐      ┌──────────────────────┐
│ Pushover Notifier │      │ TUI — Bitácora       │
│ (Alerta + Resumen │      │ Narrativa (lipgloss) │
│  + Citations)     │      │ vertical scroll      │
└───────────────────┘      └──────────────────────┘
```

**Punto de integración:** `cmd/monitor/coordinator.go` → `EmitGapSnapshot()`. El Coordinator ya detecta transiciones de fase (`shouldNotifyOnTransition`) y dispara notificaciones Pushover. El `GemmaSynthesizer` se invoca en ese mismo punto, enriqueciendo la notificación con el reporte narrativo generado por Gemma 4.

---

## 3. SDK y Dependencias

### 3.1. Go SDK

```bash
go get google.golang.org/genai
```

**SDK actual a julio 2026.** El SDK legacy `google.generativeai` (Python) y `@google/generative-ai` (JS) están deprecados. En Go se usa `google.golang.org/genai`.

### 3.2. Patrón de invocación con Search Grounding

```go
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    Backend: genai.BackendGeminiAPI,
    APIKey:  os.Getenv("GEMINI_API_KEY"),
})

tools := []*genai.Tool{
    {GoogleSearch: &genai.GoogleSearch{}},
}

config := &genai.GenerateContentConfig{
    Tools: tools,
    ThinkingConfig: &genai.ThinkingConfig{
        ThinkingLevel: genai.Ptr[string]("HIGH"),
    },
}

result, err := client.Models.GenerateContent(ctx, "gemma-4-31b-it", contents, config)
```

### 3.3. ThinkingConfig

El nivel `HIGH` de razonamiento es crítico para reportes sismológicos. Permite a Gemma 4 encadenar causalidad física (transferencia de esfuerzos de Coulomb, ley de Omori, Gutenberg-Richter) con contexto histórico obtenido vía Search Grounding.

### 3.4. Manejo de Citations

La respuesta de Search Grounding incluye anotaciones de tipo `url_citation` con título y URL de cada fuente consultada. El sintetizador debe:

1. Extraer las citations del `result.Candidates[0].Content.Parts`
2. Incluirlas como referencias al final del reporte Pushover
3. Renderizarlas en el panel de TUI como enlaces cliqueables

---

## 4. Configuración

### 4.1. Variable de entorno

Agregar a `.env`:

```env
GEMINI_API_KEY=tu_api_key_de_google_ai_studio
```

### 4.2. Campo en `internal/config/config.go`

```go
type Config struct {
    // ... existing fields ...
    GeminiAPIKey string
}

func Load() *Config {
    // ...
    return &Config{
        // ... existing fields ...
        GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
    }
}
```

Patrón idéntico a como ya se maneja `PUSHOVER_APP_TOKEN` y `PUSHOVER_USER_KEY`.

---

## 5. Rate Limiting de Gemini API

El free tier de Gemma 4 tiene cuotas por minuto y por día. Para un monitor de sismos que podría disparar múltiples transiciones de fase en cadena durante un enjambre:

| Mecanismo | Valor sugerido |
|---|---|
| Cooldown por celda | 10 minutos (independiente del cooldown de notificación de 30 min) |
| Cooldown global | 3 segundos entre llamadas a la API |
| Máx. reportes/día | 50 (vía contador interno reseteable) |

Estos límites se implementan en el propio `GemmaSynthesizer`, no en el Coordinator, para mantener la separación de responsabilidades.

---

## 6. Clasificación de Reportes

El sintetizador genera dos tipos de diagnósticos en lenguaje natural según la gravedad y el contexto histórico:

### A. Reporte de Confirmación (Caso Malo / Alerta Escalada)

- **Criterios de Activación:**
  - Ruptura de silencio en falla principal sin antecedentes históricos recientes de disipación.
  - Enjambre superficial con magnitud ascendente ($M \ge 4.5$) en segmentos trabados.
  - $b$-value significativamente bajo ($b < 0.6$).
- **Contenido del Reporte:**
  - Resumen técnico accesible de la deformación acumulada.
  - Identificación del segmento afectado (San Sebastián, El Pilar, Boconó).
  - Recomendaciones de monitoreo reforzado e instrucciones para el operador.
  - Citations de fuentes web que corroboren actividad reciente en la zona.

### B. Reporte de Calma y Reajuste (Caso Bueno / Transferencia de Esfuerzos Normalizada)

- **Criterios de Activación:**
  - Activación de enjambres moderados/bajos tras grandes eventos históricos (p. ej., reajuste estático de Coulomb tras el doblete telúrico de junio 2026 de $M_w 7.2 / 7.5$).
  - Liberación progresiva y continuada de micro-sismicidad en bloques adyacentes.
- **Contenido del Reporte:**
  - Explicación física de transferencia de esfuerzo estático (Coulomb).
  - Contextualización histórica vía **Google Search Grounding** (verificación de secuencias y antecedentes en la web en tiempo real).
  - Mensaje claro de tranquilidad sobre disipación controlada vs. silencio latente.
  - Citations de artículos o reportes de FUNVISIS/USGS que respalden el contexto.

---

## 7. Estructura de Componentes en Go

### 7.1. Paquete `internal/llm`

| Archivo | Responsabilidad |
|---|---|
| `gemma.go` | Cliente HTTP vía SDK `google.golang.org/genai`. Inicializa conexión, maneja rate limiting, invoca `GenerateContent` con Search Grounding. |
| `prompt.go` | Plantillas de prompt con formato JSON del evento + instrucciones de persona sismológica + `recent_historical_context`. |
| `types.go` | Structs Go: `SynthesisRequest`, `SynthesisResponse`, `Citation`, `ReportType`. |

### 7.2. Struct `SynthesisRequest`

Puente entre el Coordinator y el sintetizador:

```go
type SynthesisRequest struct {
    TriggerType  string       // "INESTABILIDAD_CORTICAL", "ENJAMBRE_PRECURSOR", "REPLICAS_POST_MAINSHOCK"
    FaultName    string       // "Falla de El Pilar", "Falla de San Sebastián", "Falla de Boconó"
    CellID       string       // "G_22_12"
    BValue       float64      // Gutenberg-Richter b-value actual de la celda
    WeightedEnergy float64    // Energía sísmica ponderada por profundidad (DepthWeight)
    DynamicRate  float64      // Tasa EWMA de sismicidad (eventos/día)
    Mainshock    Sismo        // Evento principal disparador
    RecentEvents []Sismo      // Eventos en la celda (últimas 48h) para contexto de enjambre
    Phase        SwarmPhase   // Fase actual de la celda (Precursor, Replicas, Atencion)
}
```

### 7.3. Struct `SynthesisResponse`

```go
type SynthesisResponse struct {
    ReportType   string      // "CONFIRMACION" o "CALMA_REAJUSTE"
    Summary      string      // Resumen ejecutivo (1-2 líneas, para Pushover title)
    Body         string      // Cuerpo completo del reporte narrativo
    Citations    []Citation  // Fuentes web consultadas por Search Grounding
    ModelUsed    string      // "gemma-4-31b-it"
    GeneratedAt  time.Time
}

type Citation struct {
    Title string
    URL   string
}
```

### 7.4. Payload enviado a Gemma 4 (formato prompt)

El prompt se construye en `prompt.go` combinando los datos estructurados del `SynthesisRequest` con instrucciones de persona:

```json
{
  "trigger_type": "INESTABILIDAD_CORTICAL",
  "fault_name": "Falla de El Pilar",
  "cell_id": "G_22_12",
  "weighted_energy": 1.45e12,
  "b_value": 0.78,
  "dynamic_rate": 3.2,
  "mainshock": {
    "magnitude": 4.8,
    "depth_km": 8.7,
    "location": "Near Coast of Venezuela",
    "timestamp": "2026-07-19T04:54:00Z"
  },
  "swarm_events_48h": 14,
  "max_swarm_mag": 3.6,
  "recent_historical_context": "Doblete sísmico de junio de 2026 (M7.2 y M7.5 en sistema Morón/Boconó)"
}
```

La plantilla de sistema instruye a Gemma 4 a:
- Actuar como sismólogo especialista en tectónica del Caribe y Venezuela.
- Usar **Google Search** para verificar datos de FUNVISIS, USGS, EMSC y medios venezolanos.
- Clasificar el reporte como `CONFIRMACION` o `CALMA_REAJUSTE`.
- Incluir citations de todas las fuentes consultadas.

---

## 8. Visualización y Notificación

### 8.1. Notificador Pushover (`internal/alert/notifier.go`)

- **Nuevo método:** `SendSynthesisReport(report SynthesisResponse)` — envía el reporte como mensaje Pushover con:
  - `title`: `[Gemma 4] {ReportType} — {FaultName}`
  - `message`: Resumen ejecutivo + citations en formato HTML (`<a href="...">`)
  - `priority`: 1 para CONFIRMACION, 0 para CALMA_REAJUSTE
- **Límite de 1024 caracteres** de Pushover respetado: si el cuerpo excede, se trunca con `… [ver TUI para reporte completo]`.

### 8.2. Interfaz TUI

- **Nuevo panel:** `Bitácora Gemma` — lista vertical con los últimos N reportes generados.
- Usa `lipgloss` para estilizar: título en negrita, citations en color tenue, separadores entre reportes.
- **Nuevo mensaje Bubbletea:** `MsgGemmaReport` con el `SynthesisResponse` completo.
- Se emite desde el Coordinator al canal TUI existente (`c.tuiChan`).

---

## 9. Fases de Implementación

### Fase 1 — Cliente `internal/llm`
- Crear `internal/llm/gemma.go` con `GemmaSynthesizer`:
  - `NewGemmaSynthesizer(apiKey string) *GemmaSynthesizer`
  - `Synthesize(ctx, req SynthesisRequest) (SynthesisResponse, error)`
  - Rate limiting interno (cooldown por celda, cooldown global, contador diario)
- Crear `internal/llm/types.go` con `SynthesisRequest`, `SynthesisResponse`, `Citation`
- Crear `internal/llm/prompt.go` con templates de sistema + payload
- Agregar `GEMINI_API_KEY` a `.env` y `internal/config/config.go`
- **Pruebas:** `gemma_test.go` con mock HTTP server simulando respuestas de la API

### Fase 2 — Integración con Coordinator
- Modificar `cmd/monitor/coordinator.go`:
  - Agregar campo `gemma *llm.GemmaSynthesizer` al struct `Coordinator`
  - En `EmitGapSnapshot()`, después de `shouldNotifyOnTransition`, construir `SynthesisRequest` y llamar a `gemma.Synthesize()`
  - Emitir `MsgGemmaReport` al canal TUI
  - Pasar `SynthesisResponse` al notificador
- Modificar `NewCoordinator` para aceptar el synthesizer como dependencia
- **Pruebas:** `coordinator_test.go` — verificar que el synthesizer se invoca en transiciones de fase

### Fase 3 — Pushover + TUI
- Agregar `SendSynthesisReport(report SynthesisResponse)` al `PushoverNotifier`
- Formatear mensaje con HTML para citations cliqueables
- Agregar `MsgGemmaReport` al paquete `internal/tui`
- Crear panel `Bitácora Gemma` en la TUI usando `lipgloss`
- **Pruebas:** verificar formato HTML dentro del límite de 1024 chars, renderizado TUI

### Fase 4 — Pruebas de integración y escenarios
- Test de escenario CONFIRMACION: celda bloqueada + 3 eventos M≥2.0 en 12h → Gemma 4 genera reporte de alerta con citations
- Test de escenario CALMA_REAJUSTE: réplicas post-mainshock (M7.2) → Gemma 4 contextualiza como transferencia de esfuerzos normal
- Test de rate limiting: 3 síntesis en rápida sucesión → solo la primera se ejecuta, las otras esperan cooldown
- Test de fallback: API de Gemini no disponible → el sistema sigue funcionando, solo omite el reporte narrativo
