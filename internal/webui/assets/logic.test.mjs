import test from 'node:test';
import assert from 'node:assert/strict';
import * as L from './logic.js';
import { pickLang, t, setLang, strings } from './i18n.js';

const providers = [
  { id: 'cpa', name: 'CPA', apis: { responses: true, anthropic: 'http://x' }, models: [{ id: 'gpt-5.5', efforts: ['low', 'high'], fast: true }, { id: 'claude-x' }] },
  { id: 'chat', name: 'Chat', apis: { chat: true }, models: [{ id: 'm' }] },
  { id: 'ant', name: 'Ant', apis: { anthropic: 'http://a' }, models: [{ id: 'opus' }] },
  { id: 'empty', name: 'E', apis: { responses: true } },
];

test('modelGroups filters by what the agent needs', () => {
  assert.deepEqual(L.modelGroups(providers, 'codex').map((g) => g.provider.id), ['cpa']);
  assert.deepEqual(L.modelGroups(providers, 'claude').map((g) => g.provider.id), ['cpa', 'ant']);
  assert.equal(L.modelGroups(providers, 'codex')[0].models[0].ref, 'cpa/gpt-5.5');
});

test('refs and requests', () => {
  assert.deepEqual(L.parseRef('cpa/openai/gpt-5'), { provider: 'cpa', model: 'openai/gpt-5' });
  assert.equal(L.parseRef('official'), null);
  assert.equal(L.findModel(providers, 'cpa/gpt-5.5').efforts.length, 2);
  assert.equal(L.currentRef({ mode: 'direct', provider: 'cpa', model: 'x' }), 'cpa/x');
  assert.equal(L.currentRef({ mode: 'official' }), 'official');
  assert.deepEqual(L.buildRequest('codex', 'cpa/x', 'on', 'high'), { agent: 'codex', ref: 'cpa/x', fast: true, effort: 'high' });
  assert.deepEqual(L.buildRequest('codex', 'cpa/x', 'keep', ''), { agent: 'codex', ref: 'cpa/x' });
  assert.deepEqual(L.buildRequest('claude', 'cpa/x', 'off', ''), { agent: 'claude', ref: 'cpa/x' });
  assert.deepEqual(L.buildRequest('codex', 'official', 'on', 'high'), { agent: 'codex', ref: 'official' });
  assert.equal(L.fastChoice('false'), 'off');
  assert.equal(L.fastChoice(''), 'keep');
});

test('diffLines classifies lines', () => {
  const d = '--- a/x\n+++ b/x\n@@ -1 +1 @@\n ctx\n-old\n+new\n* write generated file x (3 bytes)\n';
  assert.deepEqual(L.diffLines(d).map((l) => l.kind), ['file', 'file', 'hunk', 'ctx', 'del', 'add', 'gen']);
  assert.deepEqual(L.diffLines(''), []);
});

test('reportLines', () => {
  const r = { files: [{ path: 'a', exact: true, kept: ['k'] }, { path: 'b', reverted: ['x'], left: ['y'] }] };
  assert.deepEqual(L.reportLines(r).map((f) => f.items.map((i) => i.kind)), [['exact', 'kept'], ['reverted', 'left']]);
  assert.equal(L.reportHasLeft(r), true);
  assert.equal(L.reportHasLeft(null), false);
});

test('form round trip', () => {
  const p = { id: 'cpa', name: 'CPA', baseURL: 'http://x/v1', apiKey: 'sk-abcd…wxyz', hasKey: true, apis: { responses: true }, headers: { A: '1' }, models: [{ id: 'm', efforts: ['low', 'high'], fast: true }] };
  const f = L.providerToForm(p);
  assert.equal(f.apiKey, '');
  assert.equal(f.maskedKey, 'sk-abcd…wxyz');
  assert.equal(f.models[0].efforts, 'low, high');
  const back = L.formToProvider(f);
  assert.equal('apiKey' in back, false, 'an untouched key is not sent');
  assert.deepEqual(back.models, [{ id: 'm', fast: true, efforts: ['low', 'high'] }]);
  assert.deepEqual(back.headers, { A: '1' });
  assert.equal('anthropic' in back.apis, false);
  f.apiKey = 'new';
  f.headers.push({ name: ' ', value: 'x' });
  assert.equal(L.formToProvider(f).apiKey, 'new');
  assert.deepEqual(L.formToProvider(f).headers, { A: '1' });
});

test('helpers', () => {
  assert.deepEqual(L.parseEfforts('low, medium，high high'), ['low', 'medium', 'high']);
  assert.deepEqual(L.mergeModels([{ id: 'a', fast: true }], [{ id: 'a' }, { id: 'b', efforts: ['x'] }]).map((m) => [m.id, m.efforts ?? '']), [['a', ''], ['b', 'x']]);
  assert.equal(L.defaultFast('openai/gpt-5'), true);
  assert.equal(L.defaultFast('o3-mini'), true);
  assert.equal(L.defaultFast('claude'), false);
  assert.equal(L.validProviderID('cpa-2'), true);
  assert.equal(L.validProviderID('2cpa'), false);
  assert.deepEqual(L.profileSummary({ agents: [{ agent: 'codex', ref: 'cpa/x', fast: true, effort: 'high' }] })[0].extra, ['Fast', 'high']);
  assert.equal(L.stepsFailed([{}, { error: 'x' }]), true);
});

test('i18n', () => {
  assert.equal(pickLang(['zh-CN']), 'zh');
  assert.equal(pickLang(['zh-TW', 'en']), 'zh');
  assert.equal(pickLang(['en-US']), 'en');
  assert.equal(pickLang(['fr-FR']), 'en');
  assert.equal(pickLang([]), 'zh');
  setLang('en');
  assert.equal(t('previewSwitch'), 'Preview & switch');
  assert.equal(t('apiChat'), strings.zh.apiChat, 'missing en key falls back to zh');
  setLang('zh');
  assert.equal(t('switched', { agent: 'Codex', target: 'cpa/x' }), 'Codex 已切换到 cpa/x');
  for (const k of Object.keys(strings.en)) assert.ok(k in strings.zh, `en key ${k} has no zh`);
});

test('slot and path labels', () => {
  assert.equal(L.slotLabel('toml-top:model'), 'model');
  assert.equal(L.slotLabel('toml-table:model_providers.qcs-cpa'), 'model_providers.qcs-cpa');
  assert.equal(L.slotLabel('file'), 'file');
  assert.equal(L.shortPath('/home/u/.codex/config.toml'), '.codex/config.toml');
  assert.equal(L.shortPath('C:\\\\Users\\\\u\\\\.claude\\\\settings.json'), '.claude/settings.json');
});

test('freeID', () => {
  assert.equal(L.freeID('cpa', [{ id: 'x' }]), 'cpa');
  assert.equal(L.freeID('cpa', [{ id: 'cpa' }, { id: 'cpa-2' }]), 'cpa-3');
});
