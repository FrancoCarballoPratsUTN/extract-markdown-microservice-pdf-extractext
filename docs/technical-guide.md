# Guía técnica: microservicio de PDF a Markdown

## Arquitectura

El servicio utiliza una arquitectura de tres capas:

- **Presentación HTTP** (`internal/presentation/http`): recibe solicitudes,
  valida el cuerpo, aplica el control de admisión y devuelve respuestas RFC 9457.
- **Casos de uso** (`internal/usecase`): coordina la extracción y administra la
  cola acotada de trabajos.
- **Infraestructura** (`internal/infrastructure/pdf`): integra PDFium mediante
  CGO y realiza la extracción en memoria.

Las dependencias apuntan hacia el dominio. La capa de casos de uso declara la
interfaz `Extractor`; la infraestructura la implementa. Así, el código de
negocio puede probarse sin depender de HTTP ni de PDFium.

## Decisiones principales

- **PDFium mediante CGO:** ofrece el rendimiento nativo requerido. El binario
  del servidor no enlaza PDFium directamente.
- **Procesos separados:** cada trabajador ejecuta PDFium como proceso
  independiente. Si PDFium falla, se reemplaza ese trabajador sin detener el
  servidor HTTP.
- **Cola acotada:** separa las solicitudes HTTP del trabajo intensivo de CPU y
  limita tanto la cantidad de trabajos como los bytes retenidos en memoria.
- **Control de admisión:** el semáforo HTTP rechaza temprano cuando la réplica
  está llena y devuelve `503 Service Unavailable`.
- **RFC 9457:** los errores de la API usan `application/problem+json`.
- **Aplicación sin estado:** la configuración proviene de variables de entorno,
  los registros se escriben en la salida estándar y no se guardan archivos
  temporales.

## Configuración y despliegue

La imagen Docker usa varias etapas para compilar el servidor y el trabajador,
ejecutar las pruebas y construir una imagen final mínima. El contenedor se
ejecuta con un usuario sin privilegios.

```bash
docker compose up --build
```

La composición local inicia cinco réplicas detrás de Traefik y publica
`127.0.0.1:8080`. Para usar una instancia de Traefik existente:

```bash
docker compose -f docker-compose.yml -f docker-compose.traefik.yml up --build
```

## Pruebas

La etapa de pruebas de la imagen ejecuta análisis estático, pruebas unitarias y
pruebas de integración contra PDFium:

```bash
docker build --target test .
```

También se pueden ejecutar las pruebas unitarias localmente con `go test ./...`.
Las pruebas de integración requieren PDFium y un binario compilado de
`pdfium-worker`; la configuración está documentada en el `README`.

## Pruebas de carga

Los scripts de `scripts/k6` y `scripts/vegeta` usan los cuatro PDF de
`tests/stress/pdfs/`. Ambos permiten sobrescribir el destino, el corpus, la tasa,
la duración y el tiempo de espera mediante variables de entorno.

```bash
k6 run scripts/k6/spike.js
./scripts/vegeta/attack.sh
```

Las mediciones, el análisis del cuello de botella y la justificación de los
valores `MAX_WORKERS=2` y `QUEUE_SIZE=56` se encuentran en
**[INFORME.md](../INFORME.md)**.
