# RSSurrection

Interfaz editorial para una instalación existente de **FreshRSS**. No es un
agregador: los feeds, artículos, estados de lectura y favoritos viven en
FreshRSS. RSSurrection es la capa de lectura.

> RSS isn't dead. The open web lives on.

## Qué hace hoy

- Portada a pantalla completa con las noticias de tu FreshRSS, con gestos:
  vertical cambia de noticia, horizontal cambia de sección, toque abre,
  toque largo guarda en favoritos.
- Mosaico de cuatro tarjetas con proporción arrastrable (se recuerda en el
  dispositivo).
- Lector con tipografía de diario, contenido saneado y link al original.
- Guardados, secciones por categoría, búsqueda por medios regionales.
- Resumen "de qué trata" con IA, sin spoilers (opcional).
- Temas: auto, claro, sepia y alto contraste.
- PWA instalable con service worker.

## Requisitos

- Go 1.27 (o Docker).
- Una instancia de FreshRSS con la API habilitada y una contraseña de API
  (Perfil → API).

## Arranque rápido

```sh
cp .env.example .env
$EDITOR .env          # FRESHRSS_BASE_URL, FRESHRSS_USER, FRESHRSS_API_PASSWORD
docker compose up -d --build
curl -fsS localhost:8080/healthz
```

En desarrollo, sin Docker:

```sh
set -a; . ./.env; set +a
go run ./cmd/rssurrection
```

## Variables

| Variable | Default | Para qué |
|---|---|---|
| `FRESHRSS_BASE_URL` | — | Host, subpath o URL completa del API. Se normaliza a `/api/greader.php`. |
| `FRESHRSS_USER` | — | Usuario de FreshRSS. |
| `FRESHRSS_API_PASSWORD` | — | Contraseña **de API**, no la del login. |
| `RSSURRECTION_LISTEN_ADDR` | `:8080` | Dirección de escucha. |
| `RSSURRECTION_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `FRESHRSS_TIMEOUT` | `10s` | Timeout por request al API. |
| `FRESHRSS_MAX_RESPONSE_BYTES` | `8 MiB` | Límite de respuesta. |
| `RSSURRECTION_AI_BASE_URL` | — | Endpoint OpenAI-compatible. |
| `RSSURRECTION_AI_MODEL` | — | Modelo para el resumen. |
| `RSSURRECTION_AI_API_KEY` | — | Clave del modelo. Sin clave, no hay resumen. |
| `RSSURRECTION_REGIONAL_FEEDS` | lista AR | `Nombre|URL,...` o `none`. |
| `RSSURRECTION_SIGNING_SECRET` | derivado | Firma URLs de medios. |

## Seguridad

- Las credenciales solo existen en el entorno del proceso. Nunca llegan al
  navegador, a los logs ni al repositorio.
- Todas las escrituras son `POST`, con chequeo de `Origin` y body limitado.
- Las imágenes y las páginas de terceros pasan por un proxy con URLs firmadas
  y bloqueo de IPs privadas (SSRF).
- El contenedor corre sin privilegios, con `read_only` y `cap_drop: ALL`.
- Para exponerlo a Internet hace falta TLS y autenticación propia: hoy el
  proyecto **no** trae login.

## Estado

Etapas 0 a 3 completas, 4 y 5 parciales. Ver `PLAN.md`.
Los tests cubren cliente FreshRSS, proxy de medios, parser regional, sanitizador
de HTML y configuración.
