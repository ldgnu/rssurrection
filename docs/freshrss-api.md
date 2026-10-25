# FreshRSS — API Google Reader: lo que verificamos

Verificado leyendo `p/api/greader.php` y `app/Models/Entry.php` de FreshRSS
(edge, 1.29) y probando contra una instancia real.

## Autenticación

- `POST /accounts/ClientLogin` con `Email` y `Passwd` (contraseña de API).
  Devuelve líneas `SID=` y `Auth=`.
- Cada request lleva `Authorization: GoogleLogin auth=<Auth>`.
- `GET /accounts/ClientLogin` funciona pero está deprecado y se loguea.

## Escrituras

- `GET /reader/api/0/token` → token de escritura; se manda como `T` en el POST.
- `POST /reader/api/0/edit-tag`:
  - los IDs van en **`i`** (repetido), **no** en `s`;
  - `a` agrega tag, `r` lo quita;
  - solo lee el body crudo (`php://input`), así que debe ser
    `application/x-www-form-urlencoded`.
- Tags útiles: `user/-/state/com.google/read`, `.../starred`.

## Lectura

| Endpoint | Notas |
|---|---|
| `subscription/list?output=json` | feeds con `categories[]` |
| `tag/list?output=json` | carpetas con `"type":"folder"` |
| `unread-count?output=json` | `max` + entradas por feed/carpeta/total |
| `stream/contents/<stream>` | `n` (default 20), `c`, `ot`, `nt`, `xt`, `it`, `r` |
| `stream/items/ids?s=<stream>` | IDs baratos; **decimales internos** |
| `stream/items/contents` | POST con `i` repetido |

Streams: `user/-/state/com.google/reading-list`, `.../starred`,
`user/-/state/org.freshrss/main`, `.../important`, `feed/<id>`,
`user/-/label/<nombre>`.

## Detalles que rompen clientes

- Los IDs de `stream/contents` son `tag:google.com,2005:reader/item/<hex>`;
  los de `items/ids` son **decimales**. Hay que convertir.
- `continuation` es el ID interno, no el ID público.
- `published` es epoch en segundos; `timestampUsec` y `crawlTimeMsec` son
  strings.
- El estado leído/guardado viene en `categories`, no en un booleano.
- `summary.content` se trunca a 500 KB.
- Desde **1.29.0** (PR #8131) `ot` filtra por fecha de modificación además de
  publicación: sirve para sincronizar estados sin traer todo el histórico.
- `subscription/edit` acepta `ac=edit` con `t=<título>` para renombrar.
