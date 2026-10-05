import http from 'k6/http';
import { check, sleep } from 'k6';

// Points at the shared Traefik, which owns ports 80/443 and redirects to HTTPS.
const BASE_URL = __ENV.BASE_URL || 'https://extract.universidad.localhost/extract';

// The local certificate is issued by mkcert, which is not in the k6 trust
// store. Set INSECURE_TLS=false to validate it instead.
const INSECURE_TLS = (__ENV.INSECURE_TLS || 'true') !== 'false';

// The five replicas saturate at roughly 60 req/s (~12 req/s per replica on a
// single CPU). Driving more VUs than the service can absorb measures overload,
// not throughput: the admission control returns 503 and the error rate climbs
// regardless of how healthy the service is. The target is a load the service
// can actually serve, so the peak stays inside the measured capacity.
const PEAK_VUS = Number(__ENV.PEAK_VUS || 15);

export const options = {
  stages: [
    { duration: '5s', target: Math.round(PEAK_VUS / 3) },
    { duration: '15s', target: PEAK_VUS },
    { duration: '10s', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(95)<2000'],
    http_req_failed: ['rate<0.01'],
  },
  insecureSkipTLSVerify: INSECURE_TLS,
};

const PDF_CONTENT = open('../../testdata/2020-Scrum-Guide-Spanish-Latin-South-American.pdf');

export default function () {
  const data = {
    file: http.file(PDF_CONTENT, 'test.pdf', 'application/pdf'),
  };

  const res = http.post(BASE_URL, data);
  check(res, {
    'status is 200': (r) => r.status === 200,
    'has content': (r) => r.json('content') !== undefined,
    'has page_count': (r) => r.json('page_count') !== undefined,
  });
  sleep(0.1);
}