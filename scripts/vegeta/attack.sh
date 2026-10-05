#!/usr/bin/env bash
# Reproduce la referencia de Vegeta del enunciado: 50 req/s durante 30 s con
# los cuatro PDFs del corpus rotando por el objetivo y un timeout de 30 s.
#
#   ./scripts/vegeta/attack.sh
#
# Vegeta no acepta un cuerpo distinto por request, así que se genera un archivo
# de targets con una línea por PDF. Vegeta reparte los targets de forma
# round-robin, que es la rotación que pide el enunciado, y el balanceador reparte
# después entre las cinco réplicas.
#
# A partir de vegeta 12.13 `targets` dejó de ser binario y no hay subcomando
# equivalente. Se usa el formato JSON, que es el que la propia documentación
# recomienda para generar targets desde un programa: un objeto JSON por línea,
# con el cuerpo en base64.
set -euo pipefail

cd "$(dirname "$0")/../.."

TARGET="${TARGET:-https://extract.universidad.localhost}"
CORPUS="${CORPUS:-testdata}"
RATE="${RATE:-50}"
DURATION="${DURATION:-30s}"
TIMEOUT="${TIMEOUT:-30s}"
WORKERS="${WORKERS:-50}"

if ! command -v vegeta >/dev/null 2>&1; then
  echo "vegeta no está instalado: https://github.com/tsenart/vegeta#installation" >&2
  exit 1
fi

DOCS=(
  "2020-Scrum-Guide-Spanish-Latin-South-American.pdf"
  "Essential-Kanban-Condensed-Spanish.pdf"
  "Filosofia Lean.pdf"
  "scrum_manager_historias_usuario.pdf"
)

for doc in "${DOCS[@]}"; do
  [[ -f "$CORPUS/$doc" ]] || { echo "falta el corpus: $CORPUS/$doc" >&2; exit 1; }
done

# Los targets son efímeros: el PDF vive solo en memoria del proceso vegeta.
TARGETS=$(mktemp)
RESULTS=$(mktemp)
trap 'rm -f "$TARGETS" "$RESULTS"' EXIT

for doc in "${DOCS[@]}"; do
  printf '{"method":"POST","url":"%s/extract","header":{"Content-Type":["application/pdf"]},"body":"%s"}\n' \
    "$TARGET" "$(base64 -w0 "$CORPUS/$doc")" >>"$TARGETS"
done

# El certificado lo emite mkcert, que no está en el trust store del sistema.
INSECURE="${INSECURE:-true}"
TLS_FLAG=()
[[ "$INSECURE" == "true" ]] && TLS_FLAG=(-insecure)

echo "POST $TARGET/extract | $RATE req/s durante $DURATION | timeout $TIMEOUT | ${#DOCS[@]} PDFs rotando"
echo "insecure=$INSECURE  workers=$WORKERS"
echo
vegeta attack \
  -format=json \
  -rate="$RATE" -duration="$DURATION" -timeout="$TIMEOUT" \
  -max-workers="$WORKERS" "${TLS_FLAG[@]}" \
  -targets="$TARGETS" >"$RESULTS"

echo
vegeta report -type=text "$RESULTS"

# El 501 de la referencia es el código con el que vegeta reporta un timeout
# excedido; aparece junto a los códigos de estado que la API devolvió.
echo
echo "desglose por código (200 = extracción correcta, 501 = timeout excedido):"
vegeta report -type=json "$RESULTS" | tr ',' '\n' |
  grep -E '"(requests|rate|success|latencies)"|"[0-9]{3}":'