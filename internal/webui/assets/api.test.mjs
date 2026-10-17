import test from 'node:test';
import assert from 'node:assert/strict';
import { api, APIError } from './api.js';

test('relay requests use same-origin JSON and carry only the approved preview ID', async (ctx) => {
  const calls = [];
  ctx.mock.method(globalThis, 'fetch', async (path, options) => {
    calls.push({ path, ...options, body: JSON.parse(options.body) });
    return { ok: true, json: async () => ({ state: { relay: { state: 'idle' } } }) };
  });
  await api.previewRelay({ provider: 'cpa', model: 'gpt-5', all: false });
  await api.startRelay('approved-preview');
  await api.cancelRelay('cancelled-preview');
  await api.stopRelay();
  await api.recoverRelay();
  assert.deepEqual(calls.map((c) => [c.path, c.body]), [
    ['/api/relay/preview', { provider: 'cpa', model: 'gpt-5', all: false }],
    ['/api/relay/start', { id: 'approved-preview' }],
    ['/api/relay/cancel', { id: 'cancelled-preview' }],
    ['/api/relay/stop', {}],
    ['/api/relay/recover', {}],
  ]);
  for (const call of calls) {
    assert.equal(call.method, 'POST');
    assert.equal(call.credentials, 'same-origin');
    assert.equal(call.headers['Content-Type'], 'application/json');
  }
});

test('an expired or unavailable relay preview surfaces the server error', async (ctx) => {
  ctx.mock.method(globalThis, 'fetch', async () => ({
    ok: false, status: 409, statusText: 'Conflict', json: async () => ({ error: 'relay preview expired' }),
  }));
  await assert.rejects(api.startRelay('expired'), (error) => error instanceof APIError && error.status === 409 && error.message === 'relay preview expired');
});

test('autostart inspection is read-only and writes an explicit boolean on selection', async (ctx) => {
  const calls = [];
  ctx.mock.method(globalThis, 'fetch', async (path, options) => {
    calls.push({ path, ...options });
    return { ok: true, json: async () => ({ supported: true, enabled: false }) };
  });
  await api.autostart();
  await api.setAutostart(true);
  await api.setAutostart(false);
  assert.deepEqual(calls.map((c) => [c.path, c.method, c.body]), [
    ['/api/autostart', 'GET', undefined],
    ['/api/autostart', 'PUT', '{"enabled":true}'],
    ['/api/autostart', 'PUT', '{"enabled":false}'],
  ]);
  for (const call of calls) assert.equal(call.credentials, 'same-origin');
});
