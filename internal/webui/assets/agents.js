// The Agents view: one card per agent, with a model picker and a preview
// before every switch.
import { t } from './i18n.js';
import { api } from './api.js';
import { h, append, toast, errorToast, modal, diffView, reportView, busy } from './ui.js';
import {
  OFFICIAL, modelGroups, currentRef, fastChoice, findModel, buildRequest, reportHasLeft,
} from './logic.js';

// What the user picked per agent, kept across redraws until a switch.
const picks = {};
// Per agent: the last doctor run and the last switch outcome.
const doctorRuns = {};
const outcomes = {};

function pickFor(a) {
  if (!picks[a.id]) picks[a.id] = { ref: currentRef(a), fast: fastChoice(a.fast), effort: a.effort || '' };
  return picks[a.id];
}

function signInBadge(a) {
  if (!a.installed) return h('span', { class: 'badge badge-muted' }, t('notInstalled'));
  const out = [h('span', { class: 'badge badge-ok' }, t('installed'))];
  if (a.signedIn) {
    const key = `signIn_${a.signIn}`;
    out.push(h('span', { class: 'badge badge-info' }, t(['chatgpt', 'claudeai', 'apikey'].includes(a.signIn) ? key : 'signIn_other')));
  } else {
    out.push(h('span', { class: 'badge badge-muted' }, t(a.signIn === 'keyring' ? 'signIn_keyring' : 'signedOut')));
  }
  return out;
}

function currentLine(a) {
  const off = a.mode === OFFICIAL || !a.provider;
  const chips = [];
  if (!off) {
    if (a.fast === 'true') chips.push(h('span', { class: 'chip' }, 'Fast'));
    if (a.fast === 'false') chips.push(h('span', { class: 'chip chip-muted' }, `Fast ${t('off')}`));
    if (a.effort) chips.push(h('span', { class: 'chip' }, a.effort));
  }
  return h('div', { class: 'current' },
    h('span', { class: 'label' }, t('current')),
    h('strong', { class: off ? 'cur-official' : 'cur-provider' }, off ? t('official') : `${a.provider}/${a.model}`),
    chips,
    a.managed ? h('span', { class: 'badge badge-warn-soft', title: t('managed') }, t('managed')) : null);
}

function driftBox(a) {
  if (!(a.drift || []).length) return null;
  return h('div', { class: 'notice notice-warn' },
    h('div', {}, t('driftTitle')),
    h('ul', {}, a.drift.map((d) => h('li', {},
      h('code', {}, d.path), d.slot ? h('code', {}, ` ${d.slot}`) : null, ` — ${t(`drift_${d.kind}`)}`))));
}

function modelSelect(app, a, pick, onChange) {
  const groups = modelGroups(app.state.providers, a.id);
  const id = `model-${a.id}`;
  const sel = h('select', { id, onchange: (e) => onChange(e.target.value) },
    h('option', { value: OFFICIAL }, t('official')),
    groups.map((g) => h('optgroup', { label: g.provider.name },
      g.models.map((m) => h('option', { value: m.ref }, m.name === m.id ? m.id : `${m.name} (${m.id})`)))));
  // The current ref may not be listed (model removed from the provider).
  if (pick.ref !== OFFICIAL && !groups.some((g) => g.models.some((m) => m.ref === pick.ref))) {
    sel.append(h('option', { value: pick.ref }, t('unknownModel', { ref: pick.ref })));
  }
  sel.value = pick.ref;
  return h('div', { class: 'field field-grow' },
    h('label', { for: id }, t('model')),
    sel,
    groups.length ? null : h('small', { class: 'muted' }, t('noModels')));
}

function fastControl(a, pick, disabled, onChange) {
  const name = `fast-${a.id}`;
  return h('fieldset', { class: 'field seg', disabled },
    h('legend', {}, t('fast')),
    h('div', { class: 'seg-group' }, [['keep', t('keepAsIs')], ['on', t('on')], ['off', t('off')]].map(([v, label]) => {
      const rid = `${name}-${v}`;
      return [
        h('input', { type: 'radio', name, id: rid, value: v, checked: pick.fast === v, onchange: () => onChange(v) }),
        h('label', { for: rid }, label),
      ];
    })));
}

function effortControl(app, a, pick, onChange) {
  const m = findModel(app.state.providers, pick.ref);
  const efforts = (m && m.efforts) || [];
  const id = `effort-${a.id}`;
  if (pick.effort && !efforts.includes(pick.effort)) pick.effort = '';
  const sel = h('select', { id, disabled: !efforts.length, onchange: (e) => onChange(e.target.value) },
    h('option', { value: '' }, efforts.length ? t('keepAsIs') : '—'),
    efforts.map((e) => h('option', { value: e }, e)));
  sel.value = pick.effort;
  return h('div', { class: 'field field-effort' }, h('label', { for: id }, t('effort')), sel);
}

function outcomeView(o) {
  if (!o) return null;
  return h('div', { class: `notice ${o.warn ? 'notice-warn' : 'notice-ok'}` },
    h('div', {}, o.text),
    o.notice ? h('div', { class: 'notice-text' }, o.notice) : null,
    reportView(o.report));
}

function doctorView(run) {
  if (!run) return null;
  if (!run.problems.length) return h('div', { class: 'notice notice-ok' }, t('doctorOK'));
  return h('ul', { class: 'problems' }, run.problems.map((p) => h('li', { class: `problem problem-${p.level}` },
    h('span', { class: 'badge' }, p.level), ' ', p.message)));
}

function targetName(ref) { return ref === OFFICIAL ? t('official') : ref; }

async function previewAndSwitch(app, a, pick, btn) {
  const req = buildRequest(a.id, pick.ref, pick.fast, pick.effort);
  let pv;
  try {
    pv = await busy(btn, null, () => api.preview(req));
  } catch (e) { errorToast(e); return; }

  const body = h('div', { class: 'preview' },
    pv.notice ? h('div', { class: 'notice notice-info' }, pv.notice) : null,
    pv.nothing ? h('p', { class: 'muted' }, t('previewNothing')) : [h('small', { class: 'muted legend' }, t('diffLegend')), diffView(pv.diff)]);
  const cancel = h('button', { class: 'btn', onclick: () => m.close() }, pv.nothing ? t('close') : t('cancel'));
  const ok = h('button', { class: 'btn btn-primary' }, t('confirmSwitch'));
  ok.addEventListener('click', async () => {
    try {
      const res = await busy(ok, t('switching'), () => api.use(req));
      m.close();
      const r = res.result || {};
      const name = a.name;
      let text;
      if (req.ref === OFFICIAL) text = r.report ? t('restored', { agent: name }) : t('nothingToDo', { agent: name });
      else text = t('switched', { agent: name, target: req.ref });
      const warn = reportHasLeft(r.report);
      outcomes[a.id] = { text, notice: r.notice, report: r.report, warn };
      delete picks[a.id];
      delete doctorRuns[a.id];
      toast(text, warn ? 'warn' : 'ok');
      app.setState(res.state);
    } catch (e) { errorToast(e); }
  });
  const m = modal({
    title: t('previewTitle', { agent: a.name, target: targetName(req.ref) }),
    body, wide: true, actions: pv.nothing ? [cancel] : [cancel, ok],
  });
}

async function runDoctor(app, a, btn) {
  try {
    const res = await busy(btn, t('checking'), () => api.doctor(a.id));
    doctorRuns[a.id] = res;
    app.redraw();
  } catch (e) { errorToast(e); }
}

function agentCard(app, a) {
  const pick = pickFor(a);
  const card = h('section', { class: 'card agent', 'aria-labelledby': `agent-${a.id}` });
  const redraw = () => card.replaceWith(agentCard(app, a));
  const isOff = pick.ref === OFFICIAL;
  const switchBtn = h('button', { class: 'btn btn-primary', onclick: () => previewAndSwitch(app, a, pick, switchBtn) }, t('previewSwitch'));
  const checkBtn = h('button', { class: 'btn', onclick: () => runDoctor(app, a, checkBtn) }, t('check'));
  append(card, [
    h('div', { class: 'card-head' },
      h('h2', { id: `agent-${a.id}` }, a.name),
      h('div', { class: 'badges' }, signInBadge(a))),
    currentLine(a),
    a.detail ? h('div', { class: 'detail muted' }, a.detail) : null,
    a.error ? h('div', { class: 'notice notice-error' }, a.error) : null,
    driftBox(a),
    h('div', { class: 'controls' },
      modelSelect(app, a, pick, (v) => { pick.ref = v; redraw(); focusLater(`model-${a.id}`); }),
      h('div', { class: 'controls-row' },
        a.id === 'codex' ? fastControl(a, pick, isOff, (v) => { pick.fast = v; }) : null,
        effortControl(app, a, pick, (v) => { pick.effort = v; }))),
    h('div', { class: 'actions' }, switchBtn, checkBtn),
    outcomeView(outcomes[a.id]),
    doctorView(doctorRuns[a.id])]);
  return card;
}

function focusLater(id) {
  requestAnimationFrame(() => { const el = document.getElementById(id); if (el) el.focus(); });
}

export function renderAgents(app) {
  const agents = app.state.agents || [];
  if (!agents.length) return h('p', { class: 'muted center' }, t('noAgents'));
  return h('div', { class: 'grid' }, agents.map((a) => agentCard(app, a)));
}
