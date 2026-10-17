// The Providers view: the list, the add/edit form with its model editor,
// and import from other tools.
import { t } from './i18n.js';
import { api } from './api.js';
import { h, fill, toast, errorToast, modal, confirmDialog, busy } from './ui.js';
import {
  formToProvider, providerToForm, freeID, mergeModels, parseEfforts, defaultFast, validProviderID,
} from './logic.js';

let templates = null;
const imports = { loaded: false, loading: false, candidates: [], errors: [] };

function apiTags(p) {
  const a = p.apis || {};
  return [
    a.responses ? h('span', { class: 'chip' }, 'Responses') : null,
    a.chat ? h('span', { class: 'chip' }, 'Chat') : null,
    a.anthropic ? h('span', { class: 'chip' }, 'Anthropic') : null,
  ];
}

function kv(label, value, cls) {
  return [h('dt', {}, label), h('dd', { class: cls }, value)];
}

async function fetchModels(app, p, btn) {
  try {
    const res = await busy(btn, t('fetching'), () => api.fetchModels(p.id));
    toast(t('fetched', { offered: res.offered, added: res.added.length }));
    app.setState(res.state);
    return res.provider;
  } catch (e) { errorToast(e); return null; }
}

async function removeProvider(app, p) {
  if (!await confirmDialog(t('deleteProviderQ', { name: p.name }), { okText: t('delete'), danger: true })) return;
  try {
    const res = await api.removeProvider(p.id);
    toast(t('providerDeleted', { name: p.name }));
    app.setState(res.state);
  } catch (e) { errorToast(e); }
}

function providerCard(app, p) {
  const fetchBtn = h('button', { class: 'btn btn-small', disabled: !p.baseURL, onclick: () => fetchModels(app, p, fetchBtn) }, t('fetchModels'));
  return h('section', { class: 'card provider', 'aria-labelledby': `prov-${p.id}` },
    h('div', { class: 'card-head' },
      h('h2', { id: `prov-${p.id}` }, p.name, ' ', h('code', { class: 'muted id' }, p.id)),
      h('div', { class: 'badges' }, apiTags(p))),
    h('dl', { class: 'kv' },
      kv(t('baseURL'), p.baseURL || '—', 'mono ellipsis'),
      p.apis && p.apis.anthropic ? kv('Anthropic', p.apis.anthropic, 'mono ellipsis') : null,
      kv(t('apiKey'), p.hasKey ? p.apiKey : t('noKey'), p.hasKey ? 'mono' : 'muted'),
      kv(t('models'), t('modelCount', { n: (p.models || []).length }))),
    h('div', { class: 'actions' },
      h('button', { class: 'btn btn-small', onclick: () => openForm(app, p) }, t('edit')),
      fetchBtn,
      h('button', { class: 'btn btn-small btn-danger-ghost', onclick: () => removeProvider(app, p) }, t('delete'))));
}

// ---- the form ----

function textField(f, key, label, attrs = {}, hint) {
  const id = `pf-${key}`;
  return h('div', { class: 'field' },
    h('label', { for: id }, label),
    h('input', { id, value: f[key], oninput: (e) => { f[key] = e.target.value; }, ...attrs }),
    hint ? h('small', { class: 'muted' }, hint) : null);
}

function checkField(f, key, label) {
  const id = `pf-${key}`;
  return h('label', { class: 'check', for: id },
    h('input', { type: 'checkbox', id, checked: f[key], onchange: (e) => { f[key] = e.target.checked; } }), label);
}

function headersEditor(f) {
  const box = h('div', { class: 'rows' });
  const draw = () => {
    fill(box,
      ...f.headers.map((row, i) => h('div', { class: 'row' },
        h('input', { 'aria-label': t('headerName'), placeholder: t('headerName'), value: row.name, oninput: (e) => { row.name = e.target.value; } }),
        h('input', { 'aria-label': t('headerValue'), placeholder: t('headerValue'), value: row.value, oninput: (e) => { row.value = e.target.value; } }),
        h('button', { type: 'button', class: 'icon-btn', 'aria-label': t('remove'), onclick: () => { f.headers.splice(i, 1); draw(); } }, '×'))),
      h('button', { type: 'button', class: 'btn btn-small btn-ghost', onclick: () => { f.headers.push({ name: '', value: '' }); draw(); box.querySelector('.row:last-of-type input')?.focus(); } }, `+ ${t('addHeader')}`));
  };
  draw();
  return h('fieldset', { class: 'group' }, h('legend', {}, t('headers')), box);
}

function modelsEditor(app, f, isNew) {
  const list = h('div', { class: 'models' });
  const addInput = h('input', { id: 'pf-newmodel', placeholder: t('modelID'), 'aria-label': t('modelID') });
  const draw = () => {
    if (!f.models.length) {
      fill(list, h('p', { class: 'muted small' }, t('noModelsYet')));
      return;
    }
    fill(list,
      h('div', { class: 'model-row model-head muted', 'aria-hidden': 'true' },
        h('span', {}, t('modelID')), h('span', {}, 'Fast'), h('span', {}, t('efforts')), h('span', {})),
      ...f.models.map((m, i) => h('div', { class: 'model-row' },
        h('code', { class: 'ellipsis', title: m.id }, m.id),
        h('input', { type: 'checkbox', 'aria-label': `${m.id} Fast`, checked: !!m.fast, onchange: (e) => { m.fast = e.target.checked; } }),
        h('input', { 'aria-label': `${m.id} ${t('efforts')}`, placeholder: 'low, medium, high', value: m.efforts || '', oninput: (e) => { m.efforts = e.target.value; } }),
        h('button', { type: 'button', class: 'icon-btn', 'aria-label': `${t('remove')} ${m.id}`, onclick: () => { f.models.splice(i, 1); draw(); } }, '×'))));
  };
  const add = () => {
    const id = addInput.value.trim();
    if (!id) return;
    if (f.models.some((m) => m.id === id)) { toast(t('modelExists', { id }), 'warn'); return; }
    f.models.push({ id, fast: f.responses && defaultFast(id), efforts: '' });
    addInput.value = '';
    draw();
    addInput.focus();
  };
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
  const fetchBtn = h('button', {
    type: 'button', class: 'btn btn-small', disabled: isNew, title: isNew ? t('fetchNeedsSave') : null,
    onclick: async () => {
      const p = await fetchModels(app, { id: f.id }, fetchBtn);
      if (p) { f.models = mergeModels(f.models, p.models); draw(); }
    },
  }, t('fetchModels'));
  draw();
  return h('fieldset', { class: 'group' },
    h('legend', {}, t('models')),
    list,
    h('div', { class: 'row add-row' }, addInput,
      h('button', { type: 'button', class: 'btn btn-small', onclick: add }, t('addModel')),
      fetchBtn));
}

async function openForm(app, existing) {
  const isNew = !existing;
  const f = providerToForm(existing);
  const fieldsBox = h('div', { class: 'form-fields' });
  const drawFields = () => {
    fill(fieldsBox,
      h('div', { class: 'form-grid' },
        textField(f, 'id', t('fieldID'), { disabled: !isNew, autocomplete: 'off', spellcheck: 'false', autofocus: isNew }, isNew ? t('fieldIDHint') : null),
        textField(f, 'name', t('fieldName'), { autocomplete: 'off', autofocus: !isNew })),
      textField(f, 'baseURL', t('baseURL'), { type: 'url', placeholder: 'http://127.0.0.1:8317/v1', spellcheck: 'false' }),
      textField(f, 'apiKey', t('apiKey'), { type: 'password', autocomplete: 'new-password', placeholder: f.hasKey ? f.maskedKey : '' },
        f.hasKey ? t('fieldKeyHintKeep', { mask: f.maskedKey }) : t('fieldKeyHintNew')),
      f.hasKey ? checkField(f, 'clearKey', t('clearKey')) : null,
      h('fieldset', { class: 'group' }, h('legend', {}, t('apis')),
        h('div', { class: 'checks' }, checkField(f, 'responses', t('apiResponses')), checkField(f, 'chat', t('apiChat'))),
        textField(f, 'anthropic', t('anthropicURL'), { type: 'url', placeholder: 'http://127.0.0.1:8317', spellcheck: 'false' })),
      headersEditor(f),
      modelsEditor(app, f, isNew));
  };

  let tplRow = null;
  if (isNew) {
    if (!templates) {
      try { templates = await api.templates(); } catch (e) { errorToast(e); templates = []; }
    }
    const sel = h('select', {
      id: 'pf-template',
      onchange: (e) => {
        const tp = templates.find((x) => x.id === e.target.value);
        const nf = providerToForm(tp || {});
        if (nf.id) nf.id = freeID(nf.id, app.state.providers);
        Object.assign(f, nf);
        drawFields();
      },
    }, h('option', { value: '' }, t('templateNone')), templates.map((tp) => h('option', { value: tp.id }, tp.name)));
    tplRow = h('div', { class: 'field' }, h('label', { for: 'pf-template' }, t('template')), sel);
  }
  drawFields();

  const form = h('form', { class: 'provider-form', novalidate: true }, tplRow, fieldsBox);
  const save = h('button', { class: 'btn btn-primary', type: 'button' }, t('save'));
  const cancel = h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('cancel'));
  const submit = async () => {
    const p = formToProvider({ ...f, models: f.models.map((x) => ({ ...x, efforts: parseEfforts(x.efforts) })) });
    if (isNew && !validProviderID(p.id)) {
      toast(`${t('fieldID')}：${t('fieldIDHint')}`, 'error');
      document.getElementById('pf-id')?.focus();
      return;
    }
    try {
      const res = await busy(save, null, () => (isNew ? api.addProvider(p) : api.updateProvider(existing.id, p)));
      m.close();
      toast(t('providerSaved', { name: res.provider.name }));
      app.setState(res.state);
    } catch (e) { errorToast(e); }
  };
  save.addEventListener('click', submit);
  form.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  const m = modal({
    title: isNew ? t('newProvider') : t('editProvider', { name: existing.name }),
    body: form, actions: [cancel, save], wide: true,
  });
}

// ---- import ----

async function scan(app, btn) {
  imports.loading = true;
  try {
    const res = await (btn ? busy(btn, t('scanning'), () => api.scanImport()) : api.scanImport());
    imports.candidates = res.candidates;
    imports.errors = res.errors;
    imports.loaded = true;
  } catch (e) { errorToast(e); } finally {
    imports.loading = false;
  }
  app.redraw();
}

function candidateRow(app, c) {
  const p = c.provider;
  const idInput = h('input', { value: p.id, 'aria-label': t('importAs'), spellcheck: 'false', class: 'id-input' });
  const btn = h('button', {
    class: 'btn btn-small btn-primary',
    onclick: async () => {
      try {
        const res = await busy(btn, null, () => api.doImport(c, idInput.value.trim()));
        toast(t('imported', { id: res.provider.id }));
        imports.loaded = false;
        app.setState(res.state);
      } catch (e) { errorToast(e); }
    },
  }, t('importBtn'));
  const src = t(`source_${c.source.replace(/-/g, '_')}`);
  return h('li', { class: 'cand' },
    h('div', { class: 'cand-main' },
      h('div', {}, h('span', { class: 'chip' }, src), ' ', h('strong', {}, p.name || p.id), ' ', h('span', { class: 'muted small' }, c.origin)),
      h('div', { class: 'mono small ellipsis muted' }, p.baseURL || (p.apis && p.apis.anthropic) || ''),
      h('div', { class: 'small' }, apiTags(p), p.hasKey ? h('span', { class: 'mono muted' }, ` ${p.apiKey}`) : null),
      c.note ? h('div', { class: 'small muted' }, c.note) : null,
      c.exists ? h('div', { class: 'small warn-text' }, t('exists', { id: c.exists })) : null),
    h('div', { class: 'cand-act' }, idInput, btn));
}

function importSection(app) {
  if (!imports.loaded && !imports.loading) scan(app);
  const btn = h('button', { class: 'btn btn-small', onclick: () => scan(app, btn) }, t('scan'));
  let body;
  if (!imports.loaded) body = h('p', { class: 'muted small' }, t('scanning'));
  else if (!imports.candidates.length) body = h('p', { class: 'muted small' }, t('noCandidates'));
  else body = h('ul', { class: 'cands' }, imports.candidates.map((c) => candidateRow(app, c)));
  return h('section', { class: 'card import', 'aria-labelledby': 'import-title' },
    h('div', { class: 'card-head' }, h('h2', { id: 'import-title' }, t('importTitle')), btn),
    body,
    imports.errors.length ? h('ul', { class: 'problems' }, imports.errors.map((e) => h('li', { class: 'problem problem-warn' }, e))) : null);
}

export function renderProviders(app) {
  const ps = app.state.providers || [];
  return h('div', { class: 'view' },
    h('div', { class: 'view-head' },
      h('h1', {}, t('providersTitle')),
      h('button', { class: 'btn btn-primary', onclick: () => openForm(app, null) }, `+ ${t('addProvider')}`)),
    ps.length ? h('div', { class: 'grid' }, ps.map((p) => providerCard(app, p))) : h('p', { class: 'muted empty' }, t('noProviders')),
    importSection(app));
}
