# AGENTS.md — RSSurrection

Interfaz editorial (Go + `net/http` + `html/template`) sobre una instancia
existente de FreshRSS. Sin base de datos propia: FreshRSS es la fuente de
verdad. Sin frameworks de frontend.

## Reglas duras

1. Nada de credenciales en el repo, en logs ni en el navegador. Todo por
   variables de entorno; `.env` está ignorado.
2. No inventar funcionalidad: si un botón existe, ejecuta una operación real.
3. No declarar nada terminado sin `go test ./...` y una prueba real del
   endpoint o de la pantalla.
4. Un cambio por vez, con `gofmt`, `go vet` y `go test -race` cuando aplique.
5. Las URLs de imágenes y páginas de terceros pasan por `internal/media`
   (firma HMAC + bloqueo de IPs privadas). No abrir fetch directo a URLs que
   vengan de un feed.
6. Toda escritura va por POST, con chequeo de `Origin` y límite de body.
7. Español rioplatense en la UI, sin jerga corporativa.

## Mapa

- `cmd/rssurrection` — arranque, señales, shutdown ordenado.
- `internal/config` — validación de entorno, normalización de URL.
- `internal/freshrss` — cliente Google Reader API (auth, streams, edit-tag, diag).
- `internal/media` — proxy de imágenes y `og:image` con guarda SSRF.
- `internal/regional` — feeds públicos fuera de FreshRSS (parser RSS/Atom).
- `internal/ai` — resumen vía endpoint OpenAI-compatible (opcional).
- `internal/htmlsafe` — allowlist de HTML para el cuerpo de las notas.
- `internal/web` — handlers, templates embebidos, estáticos.

## Verificación

```sh
gofmt -l . && go vet ./... && go test ./... && go test -race ./...
docker compose up -d --build && curl -fsS localhost:8080/healthz
```
