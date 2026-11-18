# ntfy.sh — Notificaciones Push Gratuitas

## Qué es ntfy

[ntfy](https://ntfy.sh) es un servicio HTTP pub-sub de notificaciones push, open source (Apache 2.0 / GPLv2), con servidor público gratuito y apps mobile para Android e iOS. Funciona igual que Gotify: publicás via HTTP POST y recibís en tu celu. La diferencia es que **no necesitás hostear nada** — el servidor público `ntfy.sh` es gratis para uso no abusivo.

## Free Tier — Sin registro

| Límite | Valor |
|---|---|
| Mensajes diarios | 500 (por IP) |
| Emails diarios | 5 |
| Temas reservados | 0 (el topic name es tu secreto) |
| Registro | No necesario |

## Conceptos clave

- **Topic**: canal de notificaciones. Elegí un nombre largo y aleatorio (ej: `sismo-monitor-a7Fx9k2mPq`). Es tu contraseña — cualquiera que lo conozca puede publicar o leer.
- **Access token**: capa extra de seguridad. Se crea desde la [web app](https://ntfy.sh/app) (Account → Create access token). Con token, incluso quien conozca el topic no puede publicar sin el token.
- **Subscription**: desde la app mobile te suscribís al topic. Las notificaciones llegan via FCM (Android) o APNs (iOS).

## Publicar mensajes

### Mínimo viable (curl)
```bash
curl -d "Backup successful" ntfy.sh/sismo-monitor-a7Fx9k2mPq
```

### Con título y prioridad
```bash
curl \
  -H "Title: 🚨 Alerta Sísmica" \
  -H "Priority: high" \
  -H "Tags: warning,skull" \
  -d "Magnitud: 5.8 Mw — Ubicación: Los Teques" \
  ntfy.sh/sismo-monitor-a7Fx9k2mPq
```

### Con access token (recomendado)
```bash
curl \
  -H "Authorization: Bearer tk_tu_token_aqui" \
  -H "Title: Reporte Gemma" \
  -d "Contenido del reporte en markdown..." \
  ntfy.sh/sismo-monitor-a7Fx9k2mPq
```

## API desde Go

```go
func sendNtfy(topic, title, message string, priority int) error {
    url := "https://ntfy.sh/" + topic
    req, err := http.NewRequest("POST", url, strings.NewReader(message))
    if err != nil {
        return err
    }
    req.Header.Set("Title", title)
    req.Header.Set("Priority", fmt.Sprintf("%d", priority))
    req.Header.Set("Tags", "sismo")
    // req.Header.Set("Authorization", "Bearer "+accessToken) // si usás token

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    return nil
}
```

## Prioridades

Las prioridades en ntfy van de 1 (mínima) a 5 (máxima). También acepta strings:

| Prioridad | String | Efecto en Android |
|---|---|---|
| 1-2 | `min`, `low` | Sin sonido ni vibración |
| 3 | `default` | Sonido y vibración default |
| 4 | `high` | Sonido alto, vibración fuerte |
| 5 | `urgent`, `max` | Sonido máximo, bypass DND |

## Markdown

Para habilitar markdown en el body de la notificación, usar el header `X-Markdown: yes` o `Content-Type: text/markdown`:

```bash
curl \
  -H "Title: Reporte Gemma" \
  -H "Content-Type: text/markdown" \
  -d "## Resumen\n\n**Magnitud:** 5.2 Mw\n\n[Ver fuente](https://usgs.gov)" \
  ntfy.sh/sismo-monitor-a7Fx9k2mPq
```

## Comparación con Gotify

| | Gotify | ntfy.sh |
|---|---|---|
| Hosting | Self-hosted | Servidor público gratuito |
| API | JSON POST a `/message` | Texto plano POST a `/{topic}` |
| Auth | Header `X-Gotify-Key` | Topic name como secreto, o `Authorization: Bearer` |
| Markdown | Via extras `client::display.contentType` | Nativo con header `Content-Type: text/markdown` |
| Prioridad | 0-10 numérico | 1-5 numérico o strings |
| iOS | App third-party | App oficial nativa |
| Fuera de red local | Requiere túnel/DNS público | Funciona siempre |

## Notas de uso

- **No abuses del tier gratuito.** 500 mensajes/día es generoso para uso personal. Si tu monitor de sismos manda más, considerá el plan Supporter ($5/mes, 2,500 mensajes).
- **Elegí un topic name seguro.** Mínimo 16 caracteres alfanuméricos aleatorios. No uses palabras del diccionario ni nombres obvios.
- **Podés self-hostear ntfy en el futuro.** El servidor es el mismo binario open source. Si algún día querés independizarte, migrás tu app mobile apuntando a tu propio dominio y listo.
- **Soporta attachments, actions buttons, click URLs, y phone calls** en planes pagos. Ver [docs.ntfy.sh](https://docs.ntfy.sh/publish/) para features avanzadas.
