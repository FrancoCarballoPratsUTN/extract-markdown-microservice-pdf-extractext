# Informe técnico — Servicio de conversión de PDF a Markdown

**Universidad Tecnológica Nacional — Facultad Regional San Rafael — Ingeniería en Sistemas**
Materia: Desarrollo de Software

---

## 1. Resumen ejecutivo

El servicio expone un único endpoint HTTP que recibe un PDF y devuelve su texto en
Markdown. El problema de fondo no era escribir la extracción, sino **decidir bajo
cuánta concurrencia y con qué límite de memoria** podía ejecutarse un motor de
PDF en C (PDFium) desde Go sin que el proceso se degradara.

El resultado del trabajo se resume en cuatro afirmaciones, cada una sostenida por
medición:

1. **El cuello de botella es la CPU por réplica, no el transporte.** El 80 % del
   tiempo de una extracción está en `FPDFText_LoadPage`, que es trabajo real de
   parsing de PDFium. Agregar concurrencia no lo reduce.
2. **Subir la concurrencia no aumenta el rendimiento: aumenta el rechazo.** Con 10
   workers sobre una cuota de `cpus: 1.0` el throughput es idéntico al de 2 workers,
   pero el 8,14 % de las peticiones termina en `503` y la memoria por réplica roza el
   límite (986 MiB de 1024).
3. **La configuración final no optimizó el rendimiento, lo hizo confiable.** En el
   experimento A-B-A de la sección 5.2 pasó de 8,14 % de errores a 0,00 % con el
   mismo throughput, y la memoria del percentil 95 bajó de 918 MiB a 377 MiB.
4. **El rechazo es el primer límite que se agota, y su precio es memoria.** En la
   sesión de medición de este informe el perfil completo dejó 1,62 % de `503`
   (178 de 10 958 peticiones de k6), donde la referencia del enunciado no rechaza
   ninguna. La sección 5.5 lo localiza por medición: no es el edge ni la
   profundidad de la cola, es el presupuesto de bytes de la admisión, y subirlo
   baja los `503` a 0,13 % a costa de llevar una réplica a 902 MiB de 1024.

Contra la referencia del enunciado, el servicio entrega **+35,8 % de throughput**
(34,43 contra 25,35 req/s) y en la prueba de carga fija de Vegeta **98,4–99,3 % de
éxito contra 66,53 %**.

---

## 2. Arquitectura de la solución

### 2.1 Vista general

```
                      ┌─────────────────────────────┐
   k6 / Vegeta  ─────▶│  traefik (edge, :8080)     │
                      └──────────────┬──────────────┘
                                     │ host-based routing
             ┌───────────┬───────────┼───────────┬───────────┐
             ▼           ▼           ▼           ▼           ▼
          ┌──────┐    ┌──────┐    ┌──────┐    ┌──────┐    ┌──────┐
          │ app-1│    │ app-2│    │ app-3│    │ app-4│    │ app-5│
          │      │    │      │    │      │    │      │    │      │   5 réplicas
          │ HTTP │    │      │    │      │    │      │    │      │   cpus: 1.0
          └───┬──┘    └───┬──┘    └───┬──┘    └───┬──┘    └───┬──┘   1024 MiB
              │           │           │           │           │
              ▼           ▼           ▼           ▼           ▼
          ┌──────────────────────────────────────────────────────┐
          │  cola acotada  QUEUE_SIZE=56 + presupuesto de bytes  │
          │  2 workers  ──▶  pdfium-worker  (proceso separado)   │
          └──────────────────────────────────────────────────────┘
```

### 2.2 Por qué dos procesos y no uno

El servicio se compone de **dos binarios** que se comunican por RPC sobre un socket
unix:

- `cmd/server/main.go` — servidor HTTP, cola, réplicas.
- `cmd/pdfium-worker/main.go` — ejecuta PDFium y expone el RPC.

La razón es el aislamiento de fallos. PDFium es una biblioteca de C sin manejo de
memoria seguro en los bordes: un documento malformado la puede hacer escribir fuera
de los límites y matar el proceso con `SIGSEGV`. Si ese motor corriera en el mismo
proceso que el servidor HTTP, un PDF de entrada mataría la réplica entera y el
cliente recibiría un reset de conexión en lugar de un error manejado.

Al vivir en un proceso separado, un fallo de PDFium se circunscribe al worker: el
pool lo detecta (`pluginClient.Exited()`), lo repone y el servicio sigue respondiendo.

El costo es real y está medido: el viaje ida y vuelta por RPC cuesta ~0,21 ms y el
servicio hace **67 llamadas por extracción** sobre un total de 51,5 ms, es decir
~14 ms (27 %) del tiempo. Es el precio de la aislamiento, y la sección 3.2 muestra
que no vale la pena reducirlo.

### 2.3 La cola acotada

El pool de workers no expone los workers directamente a los handlers HTTP. Hay un
canal con búfer entre ambos:

```go
// internal/usecase/workerpool.go:90
jobs: make(chan job, queueSize),
```

El admisión tiene **dos** límites independientes, y ambos devuelven `ErrQueueFull`,
que la capa HTTP traduce a `503`:

1. **Presupuesto de bytes** (`QUEUE_MAX_MB=256`). Antes de encolar se toma el tamaño
   del PDF del presupuesto; si no alcanza, se rechaza. Esto es lo que impide que una
   ráfaga de archivos grandes empuje la réplica más allá de su límite de memoria.
2. **Profundidad del canal** (`QUEUE_SIZE=56`). Si el canal está lleno, se rechaza.

Un detalle de diseño que vale la pena destacar: el canal de resultado es bufferizado
y `Wait` respeta el contexto, de modo que **si el cliente se desconecta, el trabajo
sigue su curso en la cola y no queda un handler retenido**. El job no se cancela; se
desvincula el que espera.

### 2.4 Réplicas, cuota de CPU y por qué el edge existe

Hay cinco réplicas idénticas detrás de un balanceador. La configuración de recursos
es la parte más importante del diseño:

```yaml
cpus: '1.0'      # techo duro por réplica
memory: 1024M
```

Esa cuota dura es lo que explica casi todos los resultados de la sección 4. Cinco
réplicas pueden reclamar a lo sumo 5 de los 6 núcleos del host. Sumando el proxy y el
generador de carga, **el sistema entero satura la máquina**, lo cual es exactamente
lo que se busca en una prueba de saturación.

El edge existe por una razón práctica de reproducibilidad: cinco réplicas no pueden
todas hacer bind del mismo puerto publicado —la primera se lo queda y las demás
fallan con `port is already allocated`—, así que el balanceador publica un único
puerto hacia todas.

### 2.5 Decisiones de diseño y alternativas descartadas

| Decisión | Alternativa descartada | Motivo |
| --- | --- | --- |
| PDFium en proceso separado | PDFium embebido con cgo directo en el servidor | Un `SIGSEGV` de un PDF malformado mataría la réplica completa. Medido: MuPDF provoca `SIGSEGV` de forma reproducible. |
| Cola acotada por bytes y profundidad | Sin cola: `MAX_WORKERS` como único límite de admisión | Con 10 workers se midió 8,14 % de `503`. La cola convierte rechazo en latencia. |
| `MAX_WORKERS=2` | 10 workers (valor por defecto del código) | Mismo throughput, el doble de errores y 986 MiB de memoria contra 584 MiB. |
| `QUEUE_SIZE=56` | 32 (mejor p50 del barrido) | 34 slots en vuelo por réplica no alcanzan como margen para el desequilibrio del proxy: 0,66 % de errores. |
| Sin caché de extracciones | Cachear por hash de documento | Los cuatro PDFs del enunciado rotan entre 100 VUs; la tasa de acierto sería alta, pero el enunciado no la pide y falsearía la medición de concurrencia. |
| HTTP simple en el edge por defecto | Requerir el proxy compartido y TLS | `docker compose up --build` fallaría en una máquina limpia que no tenga esa red ni sus certificados. |
| Arquitectura en 3 capas (`presentation` / `usecase` / `domain` / `infrastructure`) | Lógica de extracción embebida en los handlers HTTP | El handler consume el puerto `ExtractUsecase` (`handler.go:13`): el motor es un detalle de infraestructura, testeable sin HTTP ni PDFium y reemplazable sin tocar la capa web. |
| Admisión en dos capas: semáforo HTTP + cola acotada | Sólo la cola como límite de admisión | El semáforo (`main.go:48`) admite `MAX_WORKERS + QUEUE_SIZE` = 58 handlers y rechaza con `"server is saturated"` antes de leer un body de hasta 50 MB; la cola es el segundo límite. Ambos devuelven 503 con `application/problem+json`, los cuerpos que la sección 5.5 usa para diagnosticar. |
| Deadline por petición (`WithDeadline`) | Confiar en que el cliente cierre la conexión | El trabajo encolado queda acotado a los 30 s de `WRITE_TIMEOUT` aunque el cliente siga conectado: el handler responde 503 y el job termina igual en la cola, así que ésta siempre drena (`middleware.go:43`, `workerpool.go:140`). |
| Apagado ordenado: SIGTERM → `Shutdown` con 10 s → `Close` | Terminar el proceso directamente | Detrás de Traefik, un `docker compose down` no debe cortar conexiones en vuelo: primero se drena el HTTP y recién después la cola (`main.go:65-73`, `workerpool.go:150`). |
| RPC sobre socket unix vía `go-plugin` | pipes stdin/stdout, HTTP en localhost, gRPC sobre TCP | Mismo host, sin abrir puertos ni exponer un servicio local; el transporte ya viene resuelto con `go-pdfium` (el socket aparece en el Anexo A). |
| Pool de procesos con verificación al arranque, worker caliente y reemplazo | Un único proceso worker compartido por la réplica | `MaxTotal=2` procesos con `MinIdle=1` (`pool.go:16`): el primer pedido no paga el arranque ni el handshake, y una caída se repone sin parar la réplica (sección 2.2). |
| Stateless: sin base de datos ni escritura a disco | Persistir resultados o estado entre peticiones | Réplicas intercambiables y cero estado que sincronizar; complementa la fila "sin caché" de más arriba. |
| Un solo `POST`, `multipart/form-data` o binario crudo, 50 MB, errores RFC 9457 y saturación como `503` | JSON con el PDF en base64 y `429` para saturación | Evita el +33 % de la codificación base64, el límite se aplica al leer (`payload.go:66`), y `503` dice "se saturó el servidor, reintenta" mientras que `429` implicaría un límite de tasa del cliente. |
| `GOMAXPROCS` automático desde el cgroup (Go ≥ 1.25) | Fijarlo a mano en cada despliegue | Con `cpus: '1.0'` el cgroup de cada réplica tiene `cpu.max=100000 100000` y el runtime queda en 2 (Go redondea hacia arriba los límites menores a 2). El techo sigue siendo la cuota de 1 CPU, no `GOMAXPROCS`: subir `MAX_WORKERS` sólo reparte ese núcleo, que es lo que mide el barrido de la sección 4.2. |

Cuatro de esas decisiones merecen una prosa corta.

**Las capas.** La regla de dependencias apunta hacia adentro: `presentation` e
`infrastructure` dependen de `domain` y `usecase`, nunca al revés. El handler no
conoce PDFium ni la cola: consume el puerto `ExtractUsecase` (`handler.go:13`),
y la cola cuelga del mismo puerto adaptada en `queued_extractor.go`, de modo que
el código HTTP no sabe si la extracción es síncrona o encolada. En la práctica,
la migración de la sección 4.1 se resolvió en `internal/infrastructure/pdf` y
`cmd/pdfium-worker` sin tocar handlers ni cola: cambiar de motor es cablear el
arranque, no reescribir el servicio.

**La admisión en dos capas.** El semáforo del middleware y la cola son límites
independientes sobre el mismo número: la capacidad del semáforo es
`MAX_WORKERS + QUEUE_SIZE` = 58 (`main.go:48`), que es exactamente lo que la
cola puede retener —2 trabajando y 56 encolados—. El semáforo actúa en la
frontera HTTP, antes de leer un body de hasta 50 MB, y responde
`"server is saturated"`; la cola es el segundo límite, con su propio rechazo por
profundidad o por presupuesto de bytes (`workerpool.go:123-133`). Ambos llegan
al cliente como 503 con `application/problem+json`. El `WithDeadline` cierra el
circuito: pone 30 s a cada petición (`main.go:49`) y, si vence estando en la
cola, el handler responde 503 mientras el job termina igual —el canal de
resultado es bufferizado, sección 2.3—, así que la cola nunca queda varada.
Detalle de configuración: `MAX_WORKERS` gobierna a la vez los procesos del pool
(`main.go:23`) y las goroutines de la cola (`main.go:44`); es un solo número
para las dos mitades.

**El ciclo de vida.** Al recibir SIGTERM o SIGINT el servidor cierra el
listener y espera hasta 10 s a que terminen las peticiones en vuelo
(`main.go:65-73`); recién cuando `Shutdown` devuelve corren los `defer`, donde
`jobs.Close()` deja de aceptar trabajos y espera a que los ya encolados terminen
(`workerpool.go:150`). Detrás de Traefik, una réplica retirada termina lo que
tiene en curso en lugar de cortar conexiones a mitad de camino.

**`GOMAXPROCS` y la cuota.** Desde Go 1.25 el runtime toma el `GOMAXPROCS` por
defecto del cgroup en vez del número de núcleos del host, así que no hizo
falta fijarlo a mano. Medido en un contenedor con `--cpus=1`: el `cpu.max` es
`100000 100000` y el runtime queda en 2, porque Go redondea hacia arriba los
límites menores a 2. Las réplicas corren con el mismo `cpu.max` (verificado con
`docker compose exec app cat /sys/fs/cgroup/cpu.max`), o sea 2 Ps de Go
repartiéndose 1 CPU de cuota: el techo es la cuota, no `GOMAXPROCS`, y ése es el
porqué de fondo de todo el barrido de la sección 4.2.

### 2.6 Decisiones de herramientas

Las decisiones de la sección 2.5 son de diseño; éstas son de stack. Se tomaron
al inicio del proyecto o durante la migración, y sólo algunas pasaron por una
medición —donde la hubo se cita—; el resto es criterio de diseño y está
etiquetado como tal.

| Herramienta | Alternativas descartadas | Motivo |
| --- | --- | --- |
| Go 1.26 | Python, Node.js, Java | El trabajo es CPU-bound sobre una biblioteca C: Go aporta binario único, goroutines baratas para la cola y los 58 trabajos en vuelo, y cgo para atar PDFium. Python queda mal servido por el GIL en CPU-bound y Node por su modelo de eventos; una JVM competiría con su propio runtime y GC por la caché de 1024 MiB. Decisión de diseño, no de benchmark: no se corrió la misma carga en otro lenguaje. |
| `go-pdfium` + `go-plugin` (RPC por proceso) | cgo directo en el servidor; `pdftotext` como binario externo; Go puro (`pdfcpu`) | cgo directo es la migración inversa, ya descartada en la fila 1 de la sección 2.5: un `SIGSEGV` mataría la réplica. Un binario externo implica fork+exec por petición y parsear una salida de texto libre. `pdfcpu` (Apache-2.0) se consideró en el plan inicial (`tasks/plan.md`) y se descartó a favor de un motor nativo de extracción. `go-pdfium` trae el pool de procesos, el handshake y el reemplazo resueltos y mantenidos. |
| Traefik v3.6 (edge) | nginx, HAProxy | El provider de Docker con labels configura el enrutamiento sin generar archivos de configuración ni recargar nada —nginx exige templates y reload, HAProxy configuración estática— y reutiliza el edge con TLS que la máquina ya tenía (sección 6.1). |
| Docker multi-stage (etapas `pdfium` → `builder` → `test` → `runtime`) | Imagen única de build | cgo necesita toolchain y headers para compilar y el runtime sólo la `.so`; separarlas mantiene la imagen final en `bookworm-slim` con usuario sin privilegios. Ver nota abajo. |

**Motores de extracción.** Ésta es la única decisión de esta sección que pasó
por evaluación real (sección 4.1):

| Motor | Licencia | Evaluación |
| --- | --- | --- |
| **PDFium** vía `go-pdfium` | BSD-3-Clause | Elegido. El motor de Chromium, mediado por proceso, de modo que un fallo no mata la réplica. |
| MuPDF (binding `go-fitz`) | AGPL-3 / comercial | Evaluado y descartado por medición: `SIGSEGV` reproducible con los PDFs del corpus (sección 4.1). Además, la AGPL obliga a publicar el código si el servicio se ofrece por red. |
| Poppler (`pdftotext`) | GPL-2.0 | No evaluado; descartado a priori: CLI, o sea fork+exec por petición y una salida libre en lugar de una API estructurada. |
| Apache PDFBox | Apache-2.0 | No evaluado; descartado a priori: una JVM completa dentro de un contenedor limitado a 1024 MiB, compitiendo por la misma memoria. |
| pdfminer.six / PyMuPDF | MIT / AGPL-3 | No evaluado; descartado a priori: Python para trabajo CPU-bound por el GIL, y PyMuPDF hereda la AGPL de MuPDF. |
| pdfcpu (Go puro) | Apache-2.0 | Considerado en el plan inicial y descartado a favor de un motor nativo de extracción ya probado en el corpus. |

**Nota sobre el multi-stage.** La etapa `pdfium` separa headers (build) de la
`.so` (runtime); `builder` compila los dos binarios con `-trimpath` y
`ldflags -s -w`; la etapa `test` corre `go vet`, las unitarias y las de
integración con PDFium real dentro del build —`docker build --target test` es la
verificación ejecutable de la sección 6.4—; y `runtime` copia sólo `server`,
`pdfium-worker` y `libpdfium.so` sobre `bookworm-slim`, con usuario no root. Dos
límites explícitos: `cgo` ató el build a linux/amd64 (`Dockerfile:70`), y el
incidente de la sección 5.1 —un `.dockerignore` que excluía los mismos paths que
el `Dockerfile` compilaba— es el contraejemplo de por qué hay que levantar la
pila completa en una máquina limpia y no sólo correr los tests en el host.

---

## 3. El cuello de botella identificado

### 3.1 Perfil por fase

Medido sobre la guía de Scrum de 16 páginas, el mismo documento que rotan las
pruebas de carga:

| Fase | Sobre proceso worker | En proceso |
| --- | ---: | ---: |
| `FPDFText_LoadPage` | 40,9 ms | 36,0 ms |
| `FPDFText_GetText` | 3,6 ms | 0,5 ms |
| `FPDFText_CountChars` | 2,8 ms | 0,0 ms |
| `FPDFText_ClosePage` | 2,3 ms | 0,1 ms |
| `OpenDocument` | 1,2 ms | 0,2 ms |
| `FPDF_CloseDocument` | 0,9 ms | 0,4 ms |
| **Total** | **51,5 ms** | **37,4 ms** |

**`FPDFText_LoadPage` es ~80 % del tiempo de la petición**, y es trabajo real de
parsing de PDFium: cargar la página requiere interpretarla y construir su estructura
de texto. Ningún cambio en la lógica de extracción lo elimina.

### 3.2 El transporte no es el cuello de botella

La diferencia entre las dos columnas es de 14,1 ms (27 %), y corresponde a las 67
llamadas RPC a ~0,21 ms cada una. Reducirlas parecería la palanca obvia. Se probó.

**Resultado negativo: `GetPageText` se probó y se rechazó por medición.** La llamada
agrupa las cuatro operaciones por página en una, y baja las 67 llamadas a 19:

| Variante | Llamadas RPC | Tiempo |
| --- | ---: | ---: |
| Secuencia actual de 4 llamadas | 67 | 49,1 ms |
| `GetPageText` (una llamada) | 19 | 49,2 ms |

Un empate. La razón es que `GetPageText` delega internamente a una lectura
**carácter por carácter** dentro del worker: se ahorra el transporte pero se paga la
 granularidad. **No hay que volver a intentar esto sin evidencia nueva.**

La única palanca restante sería una binding cgo escrita a mano que agrupe las cuatro
llamadas en un solo viaje conservando la lectura masiva. Se estima en torno al 20 %,
al costo de tener que administrar la seguridad de memoria de la ruta caliente. No
se implementó por decisión explícita: el riesgo de corrupción de memoria en producción
no compensa con un 20 % sobre un servicio que ya cumple su objetivo.

### 3.3 Consecuencia de diseño

Como el transporte y la lógica de extracción son sumideros ya acotados, **la única
variable que queda es cuánta concurrencia se permite**. Y esa variable no puede subir
el rendimiento, porque la cuota de `cpus: 1.0` es el límite real. De ahí se sigue todo
el trabajo de la sección 4.

---

## 4. Proceso de investigación

Esta sección es la que documenta cómo se llegó a la configuración final.

### 4.1 Punto de partida: un motor que se caía

La primera implementación usaba **MuPDF** a través de bindings de Go, con un selector
de motor por variable de entorno. Dos problemas marcaron la decisión:

1. **MuPDF provoca `SIGSEGV` de forma reproducible** con los PDFs del corpus. Un
   fallo nativo sin captura de lugar en el mismo proceso que el servidor.
2. El selector `EXTRACTOR_ENGINE` nunca lo leyó nadie y duplicaba una superficie de
   configuración muerta.

La migración a **PDFium** (vía `go-pdfium`, que además usa `go-plugin` y provee el
aislamiento de proceso por diseño) eliminó ambos problemas. El selector se borró: la
decisión queda ahora en el código y no en una variable de entorno.

### 4.2 Barrido de configuración

Con el servicio estable se barrieron las dos perillas con el perfil de k6 del
enunciado, tres pasadas intercaladas por configuración para que la deriva del host
golpee por igual a cada celda. Medianas de 3:

| `MAX_WORKERS` | `QUEUE_SIZE` | En vuelo | Throughput | Errores | p50 | p90 | p95 | Máx |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 32 | 36 | 27,84 req/s | 0,00 % | 2,57 s | 4,77 s | 5,33 s | 6,87 s |
| 3 | 48 | 51 | 30,05 req/s | 0,00 % | 2,46 s | 4,46 s | 5,34 s | 6,85 s |
| 2 | 32 | 34 | 28,99 req/s | 0,66 % | 2,31 s | 4,85 s | 5,32 s | 7,15 s |
| 2 | 40 | 42 | 28,59 req/s | 0,00 % | 2,48 s | 4,80 s | 5,88 s | 7,00 s |
| 2 | 48 | 50 | 29,19 req/s | 0,00 % | 2,63 s | 4,22 s | 4,55 s | 7,09 s |
| **2** | **56** | **58** | **30,68 req/s** | **0,00 %** | **2,45 s** | **4,44 s** | **4,82 s** | **6,31 s** |
| 2 | 64 | 66 | 28,61 req/s | 0,00 % | 2,48 s | 4,50 s | 4,71 s | 6,83 s |

Dos efectos independientes, ambos medidos:

**Effecto de los workers.** Bajar `MAX_WORKERS` de 4 a 2 vale ~4 % de throughput,
porque más allá del segundo worker las réplicas sólo se reparten su propia cuota de
`cpus: 1.0`. Se verificó en aislamiento que 2 le gana a 4 y 4 le gana a 1, o sea
que 2 es un óptimo genuino y no un "cuanto menos mejor". Con 10 workers el
rendimiento deja de crecer por completo (sección 5).

**Efecto de la profundidad de la cola.** La profundidad cambia p50 contra rechazos.
`QUEUE_SIZE=32` tiene el mejor p50 del barrido pero el único taux de error no nulo:
34 slots en vuelo por réplica no son margen suficiente para el desequilibrio de
carga del proxy. El 56 es donde los errores llegan a cero sin pagarlo en p50. El 64
no aporta nada más.

Este barrido se hizo con el presupuesto de bytes como la perilla relajada, y con
`QUEUE_MAX_MB` fijo en 256 MiB. La sección 5.5 muestra, con medición de esta
sesión, que el límite que realmente se agota bajo el perfil completo es ese
presupuesto y no la profundidad: el "cero errores" del 56 es un cero **condicionado
a la holgura de bytes de 256 MiB**, no una propiedad de la profundidad por sí sola.

### 4.3 La cola convierte rechazo en latencia

Un experimento aislado con concurrencia fija y sin cola muestra el mecanismo con
claridad:

| Concurrencia | Throughput | Tasa de `503` |
| --- | ---: | ---: |
| 10 | 26,55 req/s | 0,66 % |
| 15 | 24,01 req/s | 3,50 % |
| 20 | 24,54 req/s | 8,50 % |

La réplica ve una `503` cuando se supera su capacidad, y una `503` es una respuesta
**rápida**. El p90 de la referencia (7,83 s) viene de ahí. La cola cambia la
respuesta: en vez de rechazar, espera. El coste es que la distribución se aplana y la
cola de percentiles sube; el beneficio es que ninguna petición se pierde.

### 4.4 Verificación de que el generador de carga no contaminaba

Antes de publicar cifras se verificó que el generador no le estaba robando CPU al
servicio, que es el error clásico de un banco de pruebas:

| Consumidor | CPU durante la corrida |
| --- | ---: |
| k6 (100 VUs) | ~0,2 núcleos |
| 5 réplicas del servicio | ~4,5 núcleos |
| Escritorio (editor, navegador) | ~1 núcleo |
| **Total (host de 6 vCPU)** | **~5,7 núcleos** |

k6 consume ~3 % del host, así que las cifras miden el servicio y no un artefacto del
banco. También se verificó que no hubiera intercambio de memoria activo durante las
corridas (`si=0, so=0` en `vmstat`).

### 4.5 La configuración final

```
MAX_WORKERS=2      2 workers por réplica
QUEUE_SIZE=56      58 trabajos en vuelo por réplica, 290 en total
QUEUE_MAX_MB=256   techo de memoria para trabajos en curso y encolados
replicas=5         cpus: 1.0, memory: 1024M por réplica
```

---

## 5. Comparativa de métricas: antes y después

### 5.1 Diseño del experimento

Para comparar dos configuraciones no alcanza con medirlas por separado: si el host se
deriva durante la sesión, la deriva se atribuye por error a la configuración. Se
usó un diseño **A-B-A** —tres bloques de "antes", luego "después", luego "antes" otra
vez— de modo que la deriva afecta a los dos bloques de A por igual y se cancela al
comparar medianas.

El "antes" son los **valores por defecto del código** (`MAX_WORKERS=10`,
`QUEUE_SIZE=16`), que existían desde antes de este trabajo.

### 5.2 Resultados — configuración por defecto (10/16) frente a la final (2/56)

| Métrica | Antes (10/16) — 6 corridas | Después (2/56) — 3 corridas | Cambio |
| --- | ---: | ---: | ---: |
| Throughput (mediana) | 31,9 req/s | 31,1 req/s | −2,2 % |
| **Errores `503`** | **8,14 %** | **0,00 %** | **−100 %** |
| p50 | 2,53 s | **2,27 s** | −10 % |
| p90 | **4,57 s** | 5,48 s | +20 % |
| p95 | **5,02 s** | 6,62 s | +32 % |
| Máximo | **6,54 s** | 7,96 s | +22 % |
| **Memoria p95 por réplica** | **917,6 MiB** | **376,8 MiB** | **−59 %** |
| **Memoria máx. por réplica** | **986,5 MiB** | **584,5 MiB** | **−41 %** |

Por corrida, el "antes": 34,2 / 34,4 / 33,6 / 30,1 / 29,3 / 29,1 req/s con
errores entre 4,24 % y 9,02 %. El "después": 34,8 / 31,1 / 30,6 req/s con 0,14 % y
0,00 % de errores.

### 5.3 Cómo leer estos números

**El throughput no cambió, y ese es el hallazgo.** La diferencia de −2,2 % está
dentro del ruido de la sesión. Las dos configuraciones procesan lo mismo porque el
límite es la CPU por réplica, no la concurrencia.

**Los errores desaparecieron** y la memoria cayó a menos de la mitad. Con 10 workers,
diez extracciones compiten por un solo núcleo y la cola de 16 se llena; además, diez
PDF en vuelo empujan simultáneamente la réplica hasta 986 MiB de un límite de 1024 MiB
—un 96 %, a una ráfaga de distancia de un OOM.

**El p50 mejora pero la cola de percentiles empeora**, y es el mismo fenómeno de la
sección 4.3 visto desde el otro lado. Con cola de 16, un 8 % de las peticiones
recibía `503` de inmediato: la mitad de las que sí se servían era rápida. Con cola de
56, todas se sirven, la mediana mejora, pero las que esperan en la cola elevan el p90.

En resumen: **se eliminó el rechazo a cambio de más latencia en la cola.** Para un
servicio de extracción, servir todo es el comportamiento correcto; el cliente puede
reintentar un `503`, pero no puede recuperar trabajo que se descartó.

### 5.4 Resultados contra la referencia del enunciado

Perfil de k6 del enunciado, 100 VUs, contra el edge con el host vacío
(`TARGET` sin definir, o sea `http://127.0.0.1:8080`). Medianas de las 7 corridas
limpias de la sesión: 10 958 peticiones en total.

| Métrica | Referencia | Este servicio | |
| --- | ---: | ---: | --- |
| Throughput | 25,35 req/s | **34,43 req/s** | **+35,8 %** |
| Errores | 0,00 % | 1,62 % (178 de 10 958) | +1,62 pp |
| p50 | 1,88 s | **1,83 s** | 1,03× mejor |
| p90 | 7,83 s | **6,18 s** | **1,27× mejor** |
| p95 | 8,80 s | **7,02 s** | **1,25× mejor** |
| Máximo | 13,94 s | **7,60 s** | **1,83× mejor** |

En la prueba de carga fija de Vegeta (50 req/s durante 30 s) la diferencia es
mucho mayor, porque la carga fija sostenida es la que castiga el rechazo:

| | Peticiones | Éxito | p50 | p90 | p95 | Máx |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Corrida 1 | 1222 | **99,26 %** | 0,46 s | 5,16 s | 6,62 s | 7,87 s |
| Corrida 2 | 1247 | **98,40 %** | 0,42 s | 4,95 s | 6,69 s | 7,29 s |
| Referencia | — | 66,53 % | — | — | — | — |

Es decir, 32,2–33,2 req/s efectivos con 98,4–99,3 % de éxito, contra 16,65 req/s
al 66,53 % de la referencia: **+94 % a +100 % de throughput sostenido y unos 33
puntos porcentuales más de éxito**. Los `503` que quedan (9 y 20) tienen el mismo
origen que los de k6, y la sección 5.5 lo mide.

El p50 es la única métrica que antes se perdía y ahora empata: la distribución de
la referencia es bimodal —sirve rápido o rechaza rápido— y la nuestra es plana
porque encolamos en lugar de rechazar.

### 5.5 Origen de los `503` residuales

El 1,62 % merece una explicación medida, no una conjetura. La cadena de
diagnóstico fue:

1. **Los genera el servicio, no el edge.** Traefik construye sus propios errores
   con el mismo esquema `application/problem+json`, así que el status solo no
   alcanza: el body observado fue `"detail":"service unavailable"`, que es
   `domain.ErrServiceUnavailable`, y no `"server is saturated"` (el semáforo de
   admisión de la sección 2.5, `middleware.go:35`) ni `"queue full"` (la cola, `workerpool.go:124`).
2. **Vienen del backend y casi todos de una sola réplica.** Con el access log del
   edge activo, las 15 apariciones de una corrida salieron de `172.20.0.7` con
   `OriginStatus:503`, `RetryAttempts:0` y `OriginDuration` entre 4 y 47 ms: rechazo
   inmediato, no un deadline de 30 s agotado.
3. **El límite que se agota es el presupuesto de bytes, no la profundidad.** Una
   corrida de diagnóstico con `QUEUE_MAX_MB=384` y `QUEUE_SIZE=64` baja los `503`
   de 1,59 % a **0,13 %** (2 de 1516). Si el límite activo fuera la profundidad de
   la cola, subir ambas perillas no lo habría movido.

| Corrida | `QUEUE_SIZE` | `QUEUE_MAX_MB` | Throughput | `503` | p95 | Memoria máx. por réplica |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Configuración final (medianas) | 56 | 256 MiB | 34,4 req/s | 1,59 % | 7,02 s | 377 MiB (p95) |
| Diagnóstico (1 corrida) | 64 | 384 MiB | 31,8 req/s | **0,13 %** | 9,05 s | **902,7 MiB** (88 %) |

La última columna es la razón por la que `QUEUE_MAX_MB` se dejó en 256 MiB. La
holgura de admisión se compra con memoria: la réplica más cargada del diagnóstico
llegó a 902,7 MiB de 1024, un 88 % — el mismo margen que la sección 5.2 midió
para 10 workers y que casi terminó en OOM. El 1,62 % de `503` es, literalmente, la
memoria pagándose en lugar de un OOM, y la sección 4.3 ya había mostrado que la
cola convierte ese tipo de rechazo en latencia.

No se subió `QUEUE_MAX_MB` en el despliegue: hacerlo exige repetir el barrido de
la sección 4.2 con el perfil completo para poder afirmar que el p95 no se degrada,
y esa medición no está hecha.

### 5.6 Condiciones de medición y su impacto

El host es un escritorio de 6 vCPU con 14 GiB de RAM compartido con el editor y el
navegador, y su carga varía entre 4 y 60 según la sesión. Las cifras absolutas de
porcentiles **no son reproducibles al decimal entre sesiones**: la misma
configuración final midió p90 4,43 s en una sesión, 5,48 s en otra y **6,18 s en la
de este informe**, con el servicio sin cambios entre las tres.

Lo que sí es reproducible, porque se midió dentro de una misma sesión y bajo las
mismas condiciones:

- La **comparación** entre configuraciones (diseño A-B-A, 6 corridas de A contra 3 de B).
- El **orden de magnitud del throughput** (~34 req/s) y el techo impuesto por el host.
- El **origen de los `503`** (sección 5.5): el límite activo es el presupuesto de
  bytes, y subirlo los baja de 1,59 % a 0,13 %.

Lo que **no** es reproducible, y hay que decirlo porque el número del apartado 5.4
cambió: la tasa de rechazo con la configuración final. Medida 0,02 % antes y
**1,62 %** en esta sesión, con el servicio sin cambios. Lo que la sección 5.5
sostiene no es que la tasa sea cero, sino **qué la determina**: con el host
descansado el presupuesto de 256 MiB aguanta la cola, y cuando el host comparte
CPU con el resto del escritorio la latencia sube, el trabajo en vuelo crece y el
mismo presupuesto se alcanza antes. La conclusión de diseño —el rechazo es el
primer límite que se agota y su precio es memoria— no cambia; el dígito sí.

Durante esta sesión se descartaron 3 de las 10 corridas de k6 por.swap activo del
host (hasta 6,9 MB/s en una muestra, contra los 2–20 KB/s residuales de las
corridas publicadas), y se verificó muestra a muestra con `vmstat` que las 7
corridas que forman las medianas no intercambiaron memoria de forma apreciable.
El `load average` del host llegó a 17,7 durante una tanda, por lo que se esperó a
que bajara de 2 antes de medir.

---

## 6. Entregables y reproducción

### 6.1 Levantar el servicio

Un solo comando, sin dependencias externas:

```bash
docker compose up --build
```

Levanta cinco réplicas y su propio balanceador, y responde en
`http://127.0.0.1:8080`. No requiere proxy externo, certificados ni redes
preexistentes.

Para enrutar por el proxy compartido de la máquina (Traefik con TLS y el dominio
`*.universidad.localhost`), se superpone un override:

```bash
docker compose -f docker-compose.yml -f docker-compose.traefik.yml up --build
```

### 6.2 Prueba de pico (k6)

```bash
k6 run scripts/k6/spike.js
```

Perfil del enunciado: rampa de 10 s hasta 100 VUs, 20 s sostenidos, 10 s de bajada.
Cada VU rota los cuatro PDFs del corpus. Acepta `TARGET` y `CORPUS` por variable de
entorno.

### 6.3 Prueba de carga fija (Vegeta)

```bash
./scripts/vegeta/attack.sh
```

50 req/s durante 30 s, con timeout de 30 s. Vegeta no admite un cuerpo distinto por
petición, así que el script genera un archivo de targets en JSON con una línea por
PDF en base64, que es el formato que la propia documentación recomienda para generar
targets desde un programa.

### 6.4 Pruebas funcionales

```bash
docker build --target test -t pdf-extract-md:test .
docker run --rm -e PDFIUM_WORKER_PATH=/out/pdfium-worker \
  --entrypoint go pdf-extract-md:test test -count=1 -tags integration ./...
```

8 pruebas de integración con PDFium real sobre los PDFs del corpus.

### 6.5 Inventario de entregables

| # | Entregable | Ubicación |
| --- | --- | --- |
| 1 | Repositorio con Dockerfile y `docker-compose.yml` reproducible con un comando | `Dockerfile`, `docker-compose.yml`, `docker-compose.traefik.yml` |
| 2a | Script de k6 para el test de pico | `scripts/k6/spike.js` |
| 2b | Script de Vegeta para la carga fija | `scripts/vegeta/attack.sh` |
| 3 | Informe en Markdown | `INFORME.md` (este documento) |

---