// The Agents view: one card per agent, with a model picker and a preview
// before every switch.
import { t } from './i18n.js';
import { api } from './api.js';
import { h, append, toast, errorToast, modal, diffView, reportView, busy } from './ui.js';
import {
  OFFICIAL, modelGroups, currentRef, currentModelLabel, fastChoice, findModel, buildRequest, reportHasLeft,
  buildRelayRequest, relayPolicy,
} from './logic.js';

// What the user picked per agent, kept across redraws until a switch.
const picks = {};
// Per agent: the last doctor run and the last switch outcome.
const doctorRuns = {};
const outcomes = {};

function pickFor(a, relay) {
  if (!picks[a.id]) {
    const routing = a.id === 'codex' && relay?.provider && relay?.model;
    picks[a.id] = {
      ref: routing ? `${relay.provider}/${relay.model}` : currentRef(a),
      fast: fastChoice(a.fast), effort: a.effort || '', mode: routing ? 'relay' : 'direct', all: !!relay?.all,
    };
  }
  return picks[a.id];
}

function signInBadge(a) {
  if (!a.installed) return h('span', { class: 'badge badge-muted' }, t('notInstalled'));
  const out = [h('span', { class: 'badge badge-ok' }, t('installed'))];
  if (a.signedIn) {
    const key = `signIn_${a.signIn}`;
    out.push(h('span', { class: 'badge badge-info' }, t(['chatgpt', 'claudeai', 'apikey'].includes(a.signIn) ? key : 'signIn_other')));
  } else {
    const key = a.signIn === 'keyring' ? 'signIn_keyring' : a.signIn === 'unknown' ? 'signIn_unknown' : 'signedOut';
    out.push(h('span', { class: 'badge badge-muted' }, t(key)));
  }
  return out;
}

function currentLine(a) {
  const routing = a.mode === 'relay';
  const off = !routing && (a.mode === OFFICIAL || !a.provider);
  const chips = [];
  if (routing) chips.push(h('span', { class: 'chip' }, t('modeRelay')));
  if (!off) {
    if (a.fast === 'true') chips.push(h('span', { class: 'chip' }, 'Fast'));
    if (a.fast === 'false') chips.push(h('span', { class: 'chip chip-muted' }, `Fast ${t('off')}`));
    if (a.effort) chips.push(h('span', { class: 'chip' }, a.effort));
  }
  return h('div', { class: 'current' },
    h('span', { class: 'label' }, t('current')),
    h('strong', { class: off ? 'cur-official' : 'cur-provider' }, off ? t('official') : currentModelLabel(a) || t('relayModelInCodex')),
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

function modelSelect(app, a, pick, onChange, disabled) {
  const groups = modelGroups(app.state.providers, a.id, a.modelRefs);
  const id = `model-${a.id}`;
  const sel = h('select', { id, disabled, onchange: (e) => onChange(e.target.value) },
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

function effortControl(app, a, pick, onChange, disabled) {
  const m = findModel(app.state.providers, pick.ref);
  const efforts = (m && m.efforts) || [];
  const id = `effort-${a.id}`;
  if (pick.effort && !efforts.includes(pick.effort)) pick.effort = '';
  const sel = h('select', { id, disabled: disabled || !efforts.length, onchange: (e) => onChange(e.target.value) },
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

async function previewAndStartRelay(app, pick, btn) {
  const req = buildRelayRequest(pick.ref, pick.all);
  if (!req) return;
  let pv;
  try { pv = await busy(btn, null, () => api.previewRelay(req)); }
  catch (e) { errorToast(e); app.reload({ background: true }); return; }

  let confirmed = false;
  const body = h('div', { class: 'preview' },
    h('div', { class: 'notice notice-info' }, t(pick.all ? 'relayAllHint' : 'relayMixedHint')),
    h('p', {}, t('relayAddress'), ' ', h('code', {}, pv.url)),
    pv.expiresAt ? h('small', { class: 'muted' }, t('relayExpires', { time: new Date(pv.expiresAt).toLocaleTimeString() })) : null,
    pv.notice ? h('div', { class: 'notice notice-info' }, pv.notice) : null,
    h('small', { class: 'muted legend' }, t('diffLegend')), diffView(pv.diff));
  const cancel = h('button', { class: 'btn', onclick: () => m.close() }, t('cancel'));
  const ok = h('button', { class: 'btn btn-primary' }, t('relayStart'));
  ok.addEventListener('click', async () => {
    // Preview IDs are single-use; a double click or failed response cannot replay one.
    if (confirmed) return;
    confirmed = true;
    cancel.disabled = true;
    try {
      const res = await busy(ok, t('relayStarting'), () => api.startRelay(pv.id));
      m.close();
      delete picks.codex;
      delete doctorRuns.codex;
      delete outcomes.codex;
      toast(t('relayStarted'));
      app.setState(res.state);
    } catch (e) {
      m.close();
      errorToast(e);
      // A transport failure may leave the preview reserved. Cancel only its
      // ID; if Start already succeeded this cannot stop the running relay.
      try { await api.cancelRelay(pv.id); } catch { /* reload resolves the current owner */ }
      app.reload({ background: true });
    }
  });
  const m = modal({
    title: t('relayPreviewTitle'), body, actions: [cancel, ok], wide: true,
    onClose: async () => {
      if (confirmed) return;
      try {
        const res = await api.cancelRelay(pv.id);
        app.setState(res.state);
      } catch (e) { errorToast(e); app.reload({ background: true }); }
    },
  });
  app.reload({ background: true });
}

async function finishRelay(app, recover, btn) {
  try {
    const res = await busy(btn, null, () => recover ? api.recoverRelay() : api.stopRelay());
    const report = res.result?.report;
    const warn = reportHasLeft(report);
    const text = t(recover ? 'relayRecovered' : 'relayStopped');
    outcomes.codex = { text, report, warn };
    delete picks.codex;
    delete doctorRuns.codex;
    toast(text, warn ? 'warn' : 'ok');
    app.setState(res.state);
  } catch (e) { errorToast(e); app.reload({ background: true }); }
}

function relayView(app) {
  const relay = app.state.relay || {};
  const policy = relayPolicy(relay);
  const kind = ['stale', 'external'].includes(policy.state) ? 'warn' : ['error', 'unavailable', 'disabled'].includes(policy.state) ? 'error' : 'info';
  const stop = policy.canStop ? h('button', { class: 'btn btn-small', onclick: () => finishRelay(app, false, stop) }, t('relayStop')) : null;
  const recover = policy.canRecover ? h('button', { class: 'btn btn-small', onclick: () => finishRelay(app, true, recover) }, t('relayRecover')) : null;
  return h('div', { class: `notice notice-${kind} relay-status`, role: 'status' },
    h('strong', {}, t(`relay_${policy.state}`)),
    relay.url ? h('div', {}, h('code', {}, relay.url)) : null,
    relay.provider && relay.model ? h('div', {}, `${relay.provider}/${relay.model}`, ' · ', t(relay.all ? 'relayAll' : 'relayMixed')) : null,
    relay.error ? h('div', { class: 'notice-text' }, relay.error) : null,
    policy.state === 'external' ? h('p', {}, t('relayExternalHint')) : null,
    policy.state === 'stale' ? h('p', {}, t('relayStaleHint')) : null,
    policy.owned ? h('p', {}, t('relayOwnedHint')) : null,
    stop || recover ? h('div', { class: 'actions' }, stop, recover) : null);
}

function modeControl(pick, disabled, onChange) {
  const id = 'mode-codex';
  const select = h('select', { id, disabled, onchange: (e) => onChange(e.target.value) },
    h('option', { value: 'direct' }, t('modeDirect')),
    h('option', { value: 'relay' }, t('modeRelay')));
  select.value = pick.mode;
  return h('div', { class: 'field' }, h('label', { for: id }, t('connectionMode')), select);
}

function agentCard(app, a) {
  const codex = a.id === 'codex';
  const policy = relayPolicy(app.state.relay);
  const pick = pickFor(a, app.state.relay);
  const card = h('section', { class: 'card agent', 'aria-labelledby': `agent-${a.id}` });
  const redraw = () => card.replaceWith(agentCard(app, a));
  const isOff = pick.ref === OFFICIAL;
  const routing = codex && pick.mode === 'relay';
  const locked = codex && policy.owned;
  const switchBtn = h('button', {
    class: 'btn btn-primary', disabled: locked || (routing && (!policy.canPreview || !buildRelayRequest(pick.ref, pick.all))),
    onclick: () => routing ? previewAndStartRelay(app, pick, switchBtn) : previewAndSwitch(app, a, pick, switchBtn),
  }, t(routing ? 'relayPreview' : 'previewSwitch'));
  const checkBtn = h('button', { class: 'btn', onclick: () => runDoctor(app, a, checkBtn) }, t('check'));
  append(card, [
    h('div', { class: 'card-head' },
      h('h2', { id: `agent-${a.id}` }, a.name),
      h('div', { class: 'badges' }, signInBadge(a))),
    currentLine(a),
    codex ? h('small', { class: 'muted' }, t('codexClientsHint')) : null,
    codex && !routing ? h('div', { class: 'notice notice-info' }, t('codexDirectHint')) : null,
    a.id === 'claude-desktop' ? h('div', { class: 'notice notice-info' }, t('claudeDesktopHint')) : null,
    a.detail ? h('div', { class: 'detail muted' }, a.detail) : null,
    a.error ? h('div', { class: 'notice notice-error' }, a.error) : null,
    codex && policy.state !== 'idle' ? relayView(app) : null,
    driftBox(a),
    h('div', { class: 'controls' },
      codex ? modeControl(pick, locked, (v) => {
        pick.mode = v;
        if (v === 'relay' && pick.ref === OFFICIAL) pick.ref = modelGroups(app.state.providers, 'codex')[0]?.models[0]?.ref || OFFICIAL;
        redraw(); focusLater('mode-codex');
      }) : null,
      modelSelect(app, a, pick, (v) => { pick.ref = v; redraw(); focusLater(`model-${a.id}`); }, locked),
      routing ? [
        h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: pick.all, disabled: locked, onchange: (e) => { pick.all = e.target.checked; redraw(); } }), t('relayAll')),
        h('small', { class: 'muted' }, t(pick.all ? 'relayAllHint' : 'relayMixedHint')),
        h('small', { class: 'muted' }, t('relaySettingsHint')),
        isOff ? h('small', { class: 'muted' }, t('relayChooseModel')) : null,
      ] : null,
      h('div', { class: 'controls-row' },
        codex ? fastControl(a, pick, isOff || routing || locked, (v) => { pick.fast = v; }) : null,
        a.id !== 'claude-desktop' ? effortControl(app, a, pick, (v) => { pick.effort = v; }, routing || locked) : null)),
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
