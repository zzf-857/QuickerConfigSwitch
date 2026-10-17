// The JSON API. Every call sends JSON (the server requires the content
// type on changes); the token travels in the HttpOnly cookie set on the
// first page load.

export class APIError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

async function call(method, path, body) {
  const opt = { method, headers: { Accept: 'application/json' }, credentials: 'same-origin' };
  if (method !== 'GET') {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body ?? {});
  }
  let res;
  try {
    res = await fetch(path, opt);
  } catch (e) {
    throw new APIError('', 0);
  }
  let data = null;
  try { data = await res.json(); } catch { /* empty or not JSON */ }
  if (!res.ok) throw new APIError((data && data.error) || `${res.status} ${res.statusText}`, res.status);
  return data;
}

const enc = encodeURIComponent;

export const api = {
  state: () => call('GET', '/api/state'),
  preview: (req) => call('POST', '/api/preview', req),
  use: (req) => call('POST', '/api/use', req),
  doctor: (agent) => call('GET', `/api/doctor?agent=${enc(agent)}`),
  templates: () => call('GET', '/api/templates'),
  addProvider: (p) => call('POST', '/api/providers', p),
  updateProvider: (id, p) => call('PUT', `/api/providers/${enc(id)}`, p),
  removeProvider: (id) => call('DELETE', `/api/providers/${enc(id)}`),
  fetchModels: (id) => call('POST', `/api/providers/${enc(id)}/fetch`),
  saveProfile: (name) => call('POST', '/api/profiles', { name }),
  deleteProfile: (name) => call('DELETE', `/api/profiles/${enc(name)}`),
  previewProfile: (name) => call('POST', `/api/profiles/${enc(name)}/preview`),
  applyProfile: (name) => call('POST', `/api/profiles/${enc(name)}/apply`),
  scanImport: () => call('GET', '/api/import'),
  doImport: (c, id) => call('POST', '/api/import', { index: c.index, id, source: c.source, origin: c.origin }),
};
