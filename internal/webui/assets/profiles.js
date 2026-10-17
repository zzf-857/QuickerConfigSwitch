// The 方案 (profiles) view: the built-in official profile and saved ones;
// save the current picks, apply one after a preview, delete.
import { t } from './i18n.js';
import { api } from './api.js';
import { h, toast, errorToast, modal, confirmDialog, diffView, reportView, busy } from './ui.js';
import { OFFICIAL, profileSummary, stepsFailed } from './logic.js';

const ui = { saving: false, name: '' };

function agentName(app, id) {
  const a = (app.state.agents || []).find((x) => x.id === id);
  return a ? a.name : id;
}

function stepView(app, s, applied) {
  const name = agentName(app, s.agent);
  const target = s.ref === OFFICIAL ? t('official') : s.ref;
  let body;
  if (s.error) body = h('div', { class: 'notice notice-error' }, t('stepError', { error: s.error }));
  else if (applied) {
    body = [
      s.result && s.result.notice ? h('div', { class: 'notice notice-info' }, s.result.notice) : null,
      s.result ? reportView(s.result.report) : null,
    ];
  } else if (s.preview.nothing) body = h('p', { class: 'muted small' }, t('previewNothing'));
  else {
    body = [
      s.preview.notice ? h('div', { class: 'notice notice-info' }, s.preview.notice) : null,
      diffView(s.preview.diff),
    ];
  }
  return h('section', { class: 'step' }, h('h3', {}, `${name} → ${target}`), body);
}

async function applyFlow(app, p, btn) {
  let res;
  try {
    res = await busy(btn, null, () => api.previewProfile(p.name));
  } catch (e) { errorToast(e); return; }
  const steps = res.steps || [];
  const allNothing = steps.every((s) => !s.error && s.preview.nothing);
  const body = h('div', { class: 'preview' },
    steps.length ? [allNothing ? null : h('small', { class: 'muted legend' }, t('diffLegend')), steps.map((s) => stepView(app, s, false))] : h('p', { class: 'muted' }, t('emptyProfile')));
  const cancel = h('button', { class: 'btn', onclick: () => m.close() }, allNothing ? t('close') : t('cancel'));
  const ok = h('button', { class: 'btn btn-primary' }, t('confirmApply'));
  ok.addEventListener('click', async () => {
    try {
      const r = await busy(ok, null, () => api.applyProfile(p.name));
      m.close();
      const failed = stepsFailed(r.steps);
      toast(t(failed ? 'appliedWithErrors' : 'applied', { name: displayName(p) }), failed ? 'warn' : 'ok');
      app.setState(r.state);
      if (failed || (r.steps || []).some((s) => s.result && (s.result.report || s.result.notice))) {
        const done = h('button', { class: 'btn btn-primary', onclick: () => rm.close() }, t('close'));
        const rm = modal({
          title: t(failed ? 'appliedWithErrors' : 'applied', { name: displayName(p) }),
          body: h('div', { class: 'preview' }, r.steps.map((s) => stepView(app, s, true))),
          actions: [done], wide: true,
        });
      }
    } catch (e) { errorToast(e); }
  });
  const m = modal({
    title: t('applyTitle', { name: displayName(p) }),
    body, wide: true, actions: allNothing ? [cancel] : [cancel, ok],
  });
}

function displayName(p) { return p.name === OFFICIAL ? t('official') : p.name; }

async function removeProfile(app, p) {
  if (!await confirmDialog(t('deleteProfileQ', { name: p.name }), { okText: t('delete'), danger: true })) return;
  try {
    const res = await api.deleteProfile(p.name);
    toast(t('profileDeleted', { name: p.name }));
    app.setState(res.state);
  } catch (e) { errorToast(e); }
}

function profileCard(app, p) {
  const builtIn = p.name === OFFICIAL;
  const applyBtn = h('button', { class: 'btn btn-small btn-primary', onclick: () => applyFlow(app, p, applyBtn) }, t('apply'));
  const rows = profileSummary(p);
  return h('section', { class: 'card profile', 'aria-label': displayName(p) },
    h('div', { class: 'card-head' },
      h('h2', {}, displayName(p)),
      builtIn ? h('span', { class: 'badge badge-muted' }, t('builtIn')) : null),
    builtIn ? h('p', { class: 'muted small' }, t('officialProfileDesc')) : null,
    !builtIn ? (rows.length ? h('ul', { class: 'picks' }, rows.map((r) => h('li', {},
      h('span', { class: 'pick-agent' }, agentName(app, r.agent)),
      h('span', { class: 'mono pick-ref' }, r.ref === OFFICIAL ? t('official') : r.ref),
      r.extra.map((x) => h('span', { class: 'chip' }, x))))) : h('p', { class: 'muted small' }, t('emptyProfile'))) : null,
    h('div', { class: 'actions' },
      applyBtn,
      builtIn ? null : h('button', { class: 'btn btn-small btn-danger-ghost', onclick: () => removeProfile(app, p) }, t('delete'))));
}

function saveBar(app) {
  if (!ui.saving) {
    return h('button', {
      class: 'btn btn-primary', id: 'save-profile',
      onclick: () => { ui.saving = true; app.redraw(); requestAnimationFrame(() => document.getElementById('profile-name')?.focus()); },
    }, t('saveCurrent'));
  }
  const input = h('input', {
    id: 'profile-name', value: ui.name, placeholder: t('profileName'), 'aria-label': t('profileName'), maxlength: '64',
    oninput: (e) => { ui.name = e.target.value; },
    onkeydown: (e) => {
      if (e.key === 'Enter') { e.preventDefault(); save(); }
      if (e.key === 'Escape') { e.preventDefault(); cancel(); }
    },
  });
  const saveBtn = h('button', { class: 'btn btn-primary', onclick: () => save() }, t('save'));
  const cancel = () => { ui.saving = false; ui.name = ''; app.redraw(); requestAnimationFrame(() => document.getElementById('save-profile')?.focus()); };
  const save = async () => {
    const name = ui.name.trim();
    if (!name) { input.focus(); return; }
    try {
      const res = await busy(saveBtn, null, () => api.saveProfile(name));
      ui.saving = false; ui.name = '';
      toast(t('profileSaved', { name }));
      app.setState(res.state);
    } catch (e) { errorToast(e); }
  };
  return h('div', { class: 'inline-form' }, input, saveBtn, h('button', { class: 'btn', onclick: cancel }, t('cancel')));
}

export function renderProfiles(app) {
  const ps = app.state.profiles || [];
  return h('div', { class: 'view' },
    h('div', { class: 'view-head' }, h('div', {}, h('h1', {}, t('profilesTitle')), h('p', { class: 'muted small' }, t('profilesHint'))), saveBar(app)),
    h('div', { class: 'grid' }, ps.map((p) => profileCard(app, p))));
}
