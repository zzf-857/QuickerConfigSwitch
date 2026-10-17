// Small DOM helpers: element builder, toasts, modals, the diff view.
import { t } from './i18n.js';
import { diffLines, reportLines, slotLabel, shortPath } from './logic.js';

/** h builds an element: h('div', {class: 'x', onclick: f}, child, 'text', [more]). */
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (k === 'dataset') Object.assign(el.dataset, v);
    else if (k === 'value' || k === 'checked' || k === 'selected' || k === 'disabled') el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  append(el, children);
  return el;
}

/** fill replaces el's children (nulls skipped). */
export function fill(el, ...children) {
  el.replaceChildren();
  append(el, children);
  return el;
}

/** append adds children to el, skipping null/false and flattening arrays. */
export function append(el, children) {
  for (const c of children) {
    if (c === undefined || c === null || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

let toastBox;

/** toast shows a short message; kind is "ok", "warn" or "error". */
export function toast(msg, kind = 'ok', ms) {
  if (!toastBox) {
    toastBox = h('div', { class: 'toasts', role: 'status', 'aria-live': 'polite' });
    document.body.append(toastBox);
  }
  const close = () => { el.classList.add('leaving'); setTimeout(() => el.remove(), 180); };
  const el = h('div', { class: `toast toast-${kind}` },
    h('span', { class: 'toast-msg' }, msg),
    h('button', { class: 'icon-btn', 'aria-label': t('close'), onclick: close }, '×'));
  toastBox.append(el);
  setTimeout(close, ms ?? (kind === 'error' ? 8000 : 4000));
}

export function errorToast(e) {
  toast(e && e.message ? e.message : t('offline'), 'error');
}

const FOCUSABLE = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * modal opens a dialog. body is an element; actions are buttons. Returns
 * {close, el}. Esc and the backdrop close it (calling onClose); focus is
 * kept inside and given back after.
 */
export function modal({ title, body, actions = [], onClose, wide }) {
  const before = document.activeElement;
  const titleID = `m${Math.random().toString(36).slice(2)}`;
  const box = h('div', { class: `modal${wide ? ' modal-wide' : ''}`, role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': titleID },
    h('div', { class: 'modal-head' },
      h('h2', { id: titleID }, title),
      h('button', { class: 'icon-btn', 'aria-label': t('close'), onclick: () => close() }, '×')),
    h('div', { class: 'modal-body' }, body),
    actions.length ? h('div', { class: 'modal-foot' }, actions) : null);
  const back = h('div', { class: 'backdrop' }, box);
  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    document.removeEventListener('keydown', onKey, true);
    back.remove();
    document.body.classList.remove('modal-open');
    if (before && before.focus) before.focus();
    if (onClose) onClose();
  }
  function onKey(e) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); return; }
    if (e.key === 'Tab') {
      const f = [...box.querySelectorAll(FOCUSABLE)].filter((x) => x.offsetParent !== null);
      if (!f.length) return;
      const first = f[0]; const last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
  }
  back.addEventListener('mousedown', (e) => { if (e.target === back) close(); });
  document.addEventListener('keydown', onKey, true);
  document.body.classList.add('modal-open');
  document.body.append(back);
  const auto = box.querySelector('[autofocus]') || box.querySelector('.modal-foot .btn-primary') || box.querySelector(FOCUSABLE);
  if (auto) auto.focus();
  return { close, el: box };
}

/** confirmDialog asks a yes/no question; resolves true on confirm. */
export function confirmDialog(message, { okText = t('confirm'), danger = false } = {}) {
  return new Promise((resolve) => {
    let answer = false;
    const ok = h('button', { class: `btn ${danger ? 'btn-danger' : 'btn-primary'}`, onclick: () => { answer = true; m.close(); } }, okText);
    const cancel = h('button', { class: 'btn', onclick: () => m.close() }, t('cancel'));
    const m = modal({ title: t('confirm'), body: h('p', {}, message), actions: [cancel, ok], onClose: () => resolve(answer) });
    ok.focus();
  });
}

/** diffView renders a unified diff with +/- colouring. */
export function diffView(diff) {
  const pre = h('pre', { class: 'diff', tabindex: '0' });
  for (const l of diffLines(diff)) {
    pre.append(h('span', { class: `dl dl-${l.kind}` }, l.text || ' '), '\n');
  }
  return pre;
}

/** reportView lists what a switch back to official did per file. */
export function reportView(report) {
  const lines = reportLines(report);
  if (!lines.length) return null;
  return h('ul', { class: 'report' }, lines.map((f) => h('li', {},
    h('code', { class: 'path', title: f.path }, shortPath(f.path)),
    h('span', { class: 'report-items' }, f.items.map((it) => h('span', { class: `tag tag-${it.kind === 'left' ? 'warn' : 'muted'}` },
      t(`report_${it.kind}`), it.slots.length ? `：${it.slots.map(slotLabel).join(', ')}` : ''))))));
}

/**
 * busy disables a button while p runs. The label stays (a spinner covers
 * it, so the width doesn't change); label is announced to screen readers.
 */
export async function busy(btn, label, p) {
  btn.disabled = true;
  btn.setAttribute('aria-busy', 'true');
  if (label) btn.setAttribute('aria-label', label);
  try {
    return await p();
  } finally {
    btn.disabled = false;
    btn.removeAttribute('aria-busy');
    btn.removeAttribute('aria-label');
  }
}
