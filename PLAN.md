# PLAN — RSSurrection

## Hecho

- **Etapa 0** — investigación del API Google Reader sobre el código de FreshRSS
  y una instancia real. Hallazgos en `docs/freshrss-api.md`.
- **Etapa 1** — binario Go, `net/http`, plantillas embebidas, configuración
  validada, `/healthz`, `/readyz`, Dockerfile multi-stage, compose.
- **Etapa 2** — cliente FreshRSS completo (`internal/freshrss`) con tests:
  login, renovación ante 401, streams, `items/ids`, `edit-tag`, timeouts,
  límite de respuesta, errores tipados, IDs hex↔decimal.
- **Etapa 3** — portada, categorías, lector, guardados, favoritos y leído
  reales contra FreshRSS, estados vacíos y de error.
- **Etapa 4 (parcial)** — PWA instalable, manifest, iconos, service worker,
  modo oscuro, temas, gestos, navegación inferior.
- **Etapa 5 (parcial)** — proxy de imágenes y `og:image` con guarda SSRF,
  saneador de HTML, límites de body, `Origin` check, tests.

## Pendiente

- Autenticación propia (cookie `HttpOnly`, `SameSite`, CSRF) antes de exponer
  el servicio a Internet.
- Persistencia del caché de resúmenes y del orden de categorías entre
  dispositivos.
- Pruebas de carga y perfilado de memoria.
- Documento de despliegue en VPS con proxy inverso, backup y rollback.

## Decisiones

- **Sin base de datos.** FreshRSS es la fuente de verdad. El estado propio
  (tema, orden, tamaño de mosaico) vive en `localStorage`.
- **Sin framework de frontend.** HTML del servidor, CSS propio, JS mínimo.
- **Proxy de medios obligatorio**: sin él hay mixed content, hotlink y fuga de
  referrer.
- **Regionales fuera de FreshRSS** mientras no se importen como feeds; si se
  importan, la sección se elimina.
