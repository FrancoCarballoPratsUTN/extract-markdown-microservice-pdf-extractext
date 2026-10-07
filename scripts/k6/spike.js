// Perfil de carga del enunciado: 10 s de rampa hasta 100 VUs, 20 s sostenidos
// y 10 s de bajada a 0. Cada VU rota los cuatro PDFs del corpus.
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';

// http_req_failed no distingue un 503 de un reset, y esa distinción es justo lo
// que decide si la cola está dimensionada bien.
const notOK = new Counter('resp_not_ok');

const HOST = __ENV.TARGET || 'http://127.0.0.1:8080';
// k6 resuelve open() contra el directorio del script, no contra el cwd, así que
// el default sube dos niveles desde scripts/k6. Acepta también una ruta absoluta.
const CORPUS = __ENV.CORPUS || '../../tests/stress/pdfs';

const DOCS = [
  '2020-Scrum-Guide-Spanish-Latin-South-American.pdf',
  'Essential-Kanban-Condensed-Spanish.pdf',
  'Filosofia Lean.pdf',
  'scrum_manager_historias_usuario.pdf',
];

const payloads = DOCS.map((name) => open(`${CORPUS}/${name}`, 'b'));

export const options = {
  scenarios: {
    ramping: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: 100 },
        { duration: '20s', target: 100 },
        { duration: '10s', target: 0 },
      ],
    },
  },
  thresholds: {},
  discardResponseBodies: false,
  insecureSkipTLSVerify: __ENV.INSECURE_TLS !== 'false',
};

export default function () {
  const body = payloads[exec.scenario.iterationInTest % payloads.length];

  const res = http.post(`${HOST}/extract`, body, {
    headers: { 'Content-Type': 'application/pdf' },
    timeout: '60s',
  });

  if (res.status !== 200) {
    notOK.add(1, { status: res.status });
  }

  check(res, {
    'status is 200': (r) => r.status === 200,
    'returns markdown content': (r) => r.status !== 200 || String(r.body).includes('content'),
  });
}