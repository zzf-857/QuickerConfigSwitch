// System startup is separate from provider settings. Like Magpie's login
// setting, it changes only after a click and redraws from the system's reply.
import { t } from './i18n.js';
import { api } from './api.js';
import { h, toast, errorToast } from './ui.js';
import { autostartPolicy } from './logic.js';

let pending = false;

async function refresh(app) {
  if (pending) return;
  pending = true;
  app.redraw();
  try { app.setState({ ...app.state, autostart: await api.autostart() }); }
  catch (e) { errorToast(e); }
  finally { pending = false; app.redraw(); }
}

async function setEnabled(app, enabled, update = false) {
  if (pending) return;
  const policy = autostartPolicy(app.state.autostart);
  if (enabled ? !policy.canEnable : !policy.canDisable) return;
  pending = true;
  app.redraw();
  try {
    const res = await api.setAutostart(enabled);
    app.setState({ ...(res.state || app.state), autostart: res.autostart });
    toast(t(update ? 'autostartUpdated' : enabled ? 'autostartEnabled' : 'autostartDisabled'));
  } catch (e) {
    errorToast(e);
    // A failed write may still have changed the system record. Read it back.
    try { app.setState({ ...app.state, autostart: await api.autostart() }); }
    catch { /* keep the last known state; the refresh button can retry */ }
  } finally { pending = false; app.redraw(); }
}

export function renderSettings(app) {
  const status = app.state.autostart || {};
  const policy = autostartPolicy(status);
  const unavailable = !policy.supported;
  const toggle = h('input', {
    type: 'checkbox', id: 'autostart-enabled', role: 'switch', checked: policy.enabled,
    disabled: pending || (policy.enabled ? !policy.canDisable : !policy.canEnable),
    'aria-describedby': 'autostart-hint',
    onchange: (e) => setEnabled(app, e.target.checked),
  });
  const stateKey = unavailable ? 'autostartUnavailable' : policy.conflict ? 'autostartConflict' : policy.enabled ? 'on' : 'off';
  return h('div', { class: 'view' },
    h('div', { class: 'view-head' }, h('h1', {}, t('tabSettings'))),
    h('section', { class: 'card settings-card', 'aria-labelledby': 'autostart-title', 'aria-busy': pending ? 'true' : 'false' },
      h('div', { class: 'card-head' },
        h('h2', { id: 'autostart-title' }, t('autostartTitle')),
        h('span', { class: `badge ${unavailable ? 'badge-muted' : policy.conflict ? 'badge-warn-soft' : policy.enabled ? 'badge-ok' : 'badge-muted'}` }, t(stateKey))),
      h('label', { class: 'check' }, toggle, t('autostartToggle')),
      h('p', { class: 'muted', id: 'autostart-hint' }, t('autostartHint')),
      h('dl', { class: 'kv settings-paths' },
        status.executable ? [h('dt', {}, t(policy.managed ? 'autostartRegisteredExecutable' : 'autostartExecutable')), h('dd', {}, h('code', {}, status.executable))] : null,
        status.path ? [h('dt', {}, t('autostartLocation')), h('dd', {}, h('code', {}, status.path))] : null),
      status.problem ? h('div', { class: 'notice notice-warn', role: 'status' }, status.problem) : null,
      policy.managed && !status.current ? h('div', { class: 'notice notice-info' }, t('autostartMoved')) : null,
      !policy.canEnable && !policy.conflict ? h('small', { class: 'muted' }, t('autostartDesktopHint')) : null,
      h('div', { class: 'actions' },
        policy.canUpdate ? h('button', { class: 'btn btn-primary', id: 'autostart-update', disabled: pending, onclick: () => setEnabled(app, true, true) }, t(policy.enabled ? 'autostartUpdate' : 'autostartUpdateEnable')) : null,
        policy.managed && !policy.enabled ? h('button', { class: 'btn', id: 'autostart-remove', disabled: pending, onclick: () => setEnabled(app, false) }, t('autostartRemove')) : null,
        h('button', { class: 'btn', id: 'autostart-refresh', disabled: pending, onclick: () => refresh(app) }, t('refresh')))));
}
