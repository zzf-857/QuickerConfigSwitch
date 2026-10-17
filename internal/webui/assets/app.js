// Entry point: loads the state, draws the tab bar and the active view.
import { t, lang } from './i18n.js';
import { api, APIError } from './api.js';
import { h, toast } from './ui.js';
import { renderAgents } from './agents.js';
import { renderProviders } from './providers.js';
import { renderProfiles } from './profiles.js';
import { renderSettings } from './settings.js';
import { deferRefresh, relayPolicy } from './logic.js';

const TABS = [
  { id: 'agents', label: 'tabAgents', render: renderAgents },
  { id: 'providers', label: 'tabProviders', render: renderProviders },
  { id: 'profiles', label: 'tabProfiles', render: renderProfiles },
  { id: 'settings', label: 'tabSettings', render: renderSettings },
];

export const app = {
  state: null,
  tab: 'agents',
  /** setState stores a fresh state from the server and redraws. */
  setState(st) {
    if (st) this.state = st;
    stateVersion++;
    pendingState = null;
    draw();
  },
  async reload({ background = false } = {}) {
    if (background && refreshing) return;
    const version = stateVersion;
    refreshing++;
    try {
      const st = await api.state();
      // An action completed while this read was in flight; keep its newer state.
      if (version !== stateVersion) return;
      if (background && refreshDeferred()) pendingState = st;
      else if (JSON.stringify(st) !== JSON.stringify(this.state)) this.setState(st);
      setBanner(null);
    } catch (e) {
      failBanner(e);
    } finally { refreshing--; }
  },
  redraw() { draw(); },
};

let main; let banner; let footer; const tabButtons = {};
let pendingState = null; let stateVersion = 0; let refreshing = 0;

function refreshDeferred() {
  const active = document.activeElement;
  return deferRefresh(document.body.classList.contains('modal-open'), active?.tagName, active?.isContentEditable);
}

function flushRefresh() {
  if (pendingState && !refreshDeferred()) app.setState(pendingState);
}

function setBanner(msg) {
  banner.hidden = !msg;
  banner.replaceChildren(...(msg ? [h('span', {}, msg), h('button', { class: 'btn btn-small', onclick: () => app.reload() }, t('retry'))] : []));
}

function failBanner(e) {
  if (!app.state) main.replaceChildren();
  if (e instanceof APIError && e.status === 401) setBanner(t('needToken'));
  else if (e instanceof APIError && e.status === 0) setBanner(t('offline'));
  else setBanner(`${t('errorPrefix')}：${e.message}`);
}

function selectTab(id, focus) {
  app.tab = id;
  try { localStorage.setItem('qcs.tab', id); } catch { /* storage off */ }
  if (location.hash !== `#${id}`) history.replaceState(null, '', `${location.pathname}${location.search}#${id}`);
  for (const tb of TABS) {
    const b = tabButtons[tb.id];
    const on = tb.id === id;
    b.setAttribute('aria-selected', String(on));
    b.tabIndex = on ? 0 : -1;
    if (on && focus) b.focus();
  }
  draw();
}

function draw() {
  if (!main) return;
  const tab = TABS.find((x) => x.id === app.tab) || TABS[0];
  main.setAttribute('aria-labelledby', `tab-${tab.id}`);
  if (!app.state) {
    main.replaceChildren(h('p', { class: 'muted center' }, t('loading')));
    return;
  }
  const scroll = window.scrollY;
  main.replaceChildren(tab.render(app));
  window.scrollTo(0, scroll);
  footer.textContent = t('version', { v: app.state.version });
}

function init() {
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
  document.title = t('appTitle');
  // The tray panel loads /?mode=panel (about 420 px wide): compact layout.
  if (new URLSearchParams(location.search).get('mode') === 'panel') document.body.classList.add('panel');
  try {
    const saved = localStorage.getItem('qcs.tab');
    if (TABS.some((x) => x.id === saved)) app.tab = saved;
  } catch { /* storage off */ }
  const fromHash = location.hash.slice(1);
  if (TABS.some((x) => x.id === fromHash)) app.tab = fromHash;

  const bar = h('div', { class: 'tabs', role: 'tablist', 'aria-label': t('appTitle') });
  TABS.forEach((tb, i) => {
    const b = h('button', {
      class: 'tab', role: 'tab', id: `tab-${tb.id}`, 'aria-controls': 'view',
      onclick: () => selectTab(tb.id),
      onkeydown: (e) => {
        const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
        if (d) { e.preventDefault(); selectTab(TABS[(i + d + TABS.length) % TABS.length].id, true); }
      },
    }, t(tb.label));
    tabButtons[tb.id] = b;
    bar.append(b);
  });
  const header = h('header', { class: 'top' },
    h('div', { class: 'brand' }, h('span', { class: 'logo', 'aria-hidden': 'true' }, 'Q'), h('span', {}, 'QCS')),
    bar);
  banner = h('div', { class: 'banner', role: 'alert', hidden: true });
  main = h('main', { id: 'view', role: 'tabpanel', tabindex: '-1' });
  footer = h('footer', { class: 'foot muted' });
  document.body.replaceChildren(header, banner, main, footer);
  selectTab(app.tab);
  app.reload();

  // Pick up changes made elsewhere (the CLI, another window) on return.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') app.reload({ background: true });
  });
  window.addEventListener('focus', () => app.reload({ background: true }));
  document.addEventListener('focusin', () => setTimeout(flushRefresh, 0));
  document.addEventListener('focusout', () => setTimeout(flushRefresh, 0));
  setInterval(() => {
    if (document.visibilityState !== 'visible') return;
    flushRefresh();
    if (relayPolicy((pendingState || app.state)?.relay).poll) app.reload({ background: true });
  }, 3000);
  window.addEventListener('unhandledrejection', (e) => {
    toast(e.reason && e.reason.message ? e.reason.message : String(e.reason), 'error');
  });
}

init();
