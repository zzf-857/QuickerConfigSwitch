// Pure functions the views use: no DOM, no fetch, so node --test covers them.

export const OFFICIAL = 'official';

/** servesAgent says whether provider p can serve agent id. */
export function servesAgent(p, agentID) {
  const apis = p.apis || {};
  if (agentID === 'codex') return !!apis.responses;
  if (agentID === 'claude' || agentID === 'claude-desktop') return !!apis.anthropic;
  return !!(apis.responses || apis.chat || apis.anthropic);
}

/**
 * modelGroups lists, per provider that can serve the agent, the models it
 * offers: [{provider: {id,name}, models: [{ref, id, name, efforts, fast}]}].
 * Providers without models are left out. modelRefs, when supplied by an
 * adapter, restricts choices to models that client can actually configure.
 */
export function modelGroups(providers, agentID, modelRefs) {
  const out = [];
  for (const p of providers || []) {
    if (!servesAgent(p, agentID)) continue;
    const models = (p.models || []).filter((m) => !Array.isArray(modelRefs) || modelRefs.includes(`${p.id}/${m.id}`));
    if (!models.length) continue;
    out.push({
      provider: { id: p.id, name: p.name || p.id },
      models: models.map((m) => ({
        ref: `${p.id}/${m.id}`, id: m.id, name: m.name || m.id,
        efforts: m.efforts || [], fast: !!m.fast,
      })),
    });
  }
  return out;
}

/** parseRef splits "provider/model" at its first slash. */
export function parseRef(ref) {
  if (!ref || ref === OFFICIAL) return null;
  const i = ref.indexOf('/');
  if (i <= 0 || i === ref.length - 1) return null;
  return { provider: ref.slice(0, i), model: ref.slice(i + 1) };
}

/** findModel finds the model a ref names in the provider list. */
export function findModel(providers, ref) {
  const r = parseRef(ref);
  if (!r) return null;
  const p = (providers || []).find((x) => x.id === r.provider);
  return p ? (p.models || []).find((m) => m.id === r.model) || null : null;
}

/** currentRef is the ref an agent view points at now. */
export function currentRef(a) {
  if (!a || a.mode === OFFICIAL || !a.provider || !a.model) return OFFICIAL;
  return `${a.provider}/${a.model}`;
}

/** A relay preserves Codex's model; its provider is only a routing target. */
export function currentModelLabel(a) {
  return a?.mode === 'relay' ? (a.model || '') : currentRef(a);
}

/** fastValue maps the tri-state ("keep" | "on" | "off") to the request's fast. */
export function fastValue(choice) {
  if (choice === 'on') return true;
  if (choice === 'off') return false;
  return undefined;
}

/** fastChoice maps an agent view's fast ("true", "false", "") to the tri-state. */
export function fastChoice(v) {
  if (v === 'true') return 'on';
  if (v === 'false') return 'off';
  return 'keep';
}

/** buildRequest is the body of /api/preview and /api/use. */
export function buildRequest(agentID, ref, fast, effort) {
  const r = { agent: agentID, ref: ref || OFFICIAL };
  if (r.ref !== OFFICIAL) {
    const f = fastValue(fast);
    if (agentID === 'codex' && f !== undefined) r.fast = f;
    if (effort && agentID !== 'claude-desktop') r.effort = effort;
  }
  return r;
}

/** buildRelayRequest keeps direct-mode Fast/Effort out of relay previews. */
export function buildRelayRequest(ref, all) {
  const r = parseRef(ref);
  return r ? { ...r, all: !!all } : null;
}

/** relayPolicy exposes only actions owned by this process. */
export function relayPolicy(relay) {
  const state = relay?.state || 'unavailable';
  const owned = state === 'preview' || state === 'running';
  return {
    state, owned,
    canPreview: state === 'idle' || state === 'error',
    canStop: state === 'running',
    canRecover: state === 'stale',
    poll: owned,
  };
}

/** Background refreshes wait until dialogs and focused form fields are done. */
export function deferRefresh(modalOpen, activeTag, contentEditable = false) {
  return !!modalOpen || !!contentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(String(activeTag || '').toUpperCase());
}

/** Startup entries with unknown ownership can be inspected but never removed. */
export function autostartPolicy(status) {
  const supported = !!status?.supported;
  const managed = !!status?.managed;
  const conflict = !!status?.problem && !managed;
  const canEnable = supported && !!status?.canEnable && !conflict;
  return {
    supported, managed, conflict, canEnable,
    enabled: !!status?.enabled,
    canDisable: managed,
    canUpdate: managed && !status?.current && canEnable,
  };
}

/**
 * diffLines classifies each line of a unified diff for colouring:
 * "file" (---/+++), "hunk" (@@), "add", "del", "gen" ("* " summaries of
 * generated files) or "ctx".
 */
export function diffLines(diff) {
  if (!diff) return [];
  const lines = diff.replace(/\n$/, '').split('\n');
  return lines.map((text) => {
    let kind = 'ctx';
    if (text.startsWith('+++ ') || text.startsWith('--- ')) kind = 'file';
    else if (text.startsWith('@@')) kind = 'hunk';
    else if (text.startsWith('* ')) kind = 'gen';
    else if (text.startsWith('+')) kind = 'add';
    else if (text.startsWith('-')) kind = 'del';
    return { kind, text };
  });
}

/**
 * reportLines summarises a safekeep Report per file:
 * [{path, items: [{kind, slots}]}] with kind exact | removed | reverted | left | kept.
 */
export function reportLines(report) {
  const out = [];
  for (const f of (report && report.files) || []) {
    const items = [];
    if (f.exact) items.push({ kind: 'exact', slots: [] });
    if (f.removed) items.push({ kind: 'removed', slots: [] });
    for (const k of ['reverted', 'left', 'kept']) {
      if ((f[k] || []).length) items.push({ kind: k, slots: f[k] });
    }
    out.push({ path: f.path, items });
  }
  return out;
}

/** reportHasLeft: some slot was changed by someone else and left alone. */
export function reportHasLeft(report) {
  return ((report && report.files) || []).some((f) => (f.left || []).length > 0);
}

/** parseEfforts turns "low, medium,high" into ["low","medium","high"]. */
export function parseEfforts(s) {
  return String(s || '').split(/[,，\s]+/).map((x) => x.trim()).filter(Boolean)
    .filter((x, i, a) => a.indexOf(x) === i);
}

/** headersToRows / rowsToHeaders convert between the map and editable rows. */
export function headersToRows(h) {
  return Object.entries(h || {}).map(([name, value]) => ({ name, value }));
}

export function rowsToHeaders(rows) {
  const h = {};
  for (const r of rows || []) {
    const n = (r.name || '').trim();
    if (n) h[n] = r.value || '';
  }
  return Object.keys(h).length ? h : undefined;
}

/**
 * formToProvider builds the provider body from the form's fields. apiKey
 * is sent only when typed: the server keeps the stored key for "".
 */
export function formToProvider(f) {
  const p = {
    id: (f.id || '').trim(),
    name: (f.name || '').trim(),
    baseURL: (f.baseURL || '').trim(),
    apis: {
      responses: !!f.responses,
      chat: !!f.chat,
      anthropic: (f.anthropic || '').trim(),
    },
    models: (f.models || []).map((m) => {
      const o = { id: m.id };
      if (m.name && m.name !== m.id) o.name = m.name;
      if (m.fast) o.fast = true;
      const e = Array.isArray(m.efforts) ? m.efforts : parseEfforts(m.efforts);
      if (e.length) o.efforts = e;
      if (m.context) o.context = m.context;
      if (m.images) o.images = true;
      return o;
    }),
  };
  if (!p.apis.anthropic) delete p.apis.anthropic;
  if (f.apiKey) p.apiKey = f.apiKey;
  if (f.clearKey) p.clearKey = true;
  const h = rowsToHeaders(f.headers);
  if (h) p.headers = h;
  return p;
}

/** providerToForm is the form state for editing p (or a template). */
export function providerToForm(p) {
  p = p || {};
  const apis = p.apis || {};
  return {
    id: p.id || '', name: p.name || '', baseURL: p.baseURL || '', apiKey: '',
    hasKey: !!p.hasKey, maskedKey: p.apiKey || '', clearKey: false,
    responses: !!apis.responses, chat: !!apis.chat, anthropic: apis.anthropic || '',
    headers: headersToRows(p.headers),
    models: (p.models || []).map((m) => ({ ...m, efforts: (m.efforts || []).join(', ') })),
  };
}

/** mergeModels adds the models of `incoming` the form doesn't list yet. */
export function mergeModels(current, incoming) {
  const have = new Set((current || []).map((m) => m.id));
  const out = [...(current || [])];
  for (const m of incoming || []) {
    if (!have.has(m.id)) {
      have.add(m.id);
      out.push({ ...m, efforts: Array.isArray(m.efforts) ? m.efforts.join(', ') : (m.efforts || '') });
    }
  }
  return out;
}

/** defaultFast mirrors provider.DefaultFast for models added by hand. */
export function defaultFast(id) {
  let seg = id.includes('/') ? id.slice(id.lastIndexOf('/') + 1) : id;
  seg = seg.toLowerCase();
  return seg.startsWith('gpt-') || /^o[0-9]/.test(seg);
}

/** validProviderID mirrors provider.ValidID. */
export function validProviderID(id) {
  return /^[a-z][a-z0-9-]{0,31}$/.test(id || '');
}

/** profileRefs is "codex → cpa/gpt-5.5" lines for a profile. */
export function profileSummary(profile) {
  return (profile.agents || []).map((r) => {
    const extra = [];
    if (r.fast === true) extra.push('Fast');
    if (r.effort) extra.push(r.effort);
    return { agent: r.agent, ref: r.ref, extra };
  });
}

/** stepsFailed: any step of a profile apply/preview has an error. */
export function stepsFailed(steps) {
  return (steps || []).some((s) => s.error);
}

/** slotLabel drops a slot's kind prefix: "toml-top:model" → "model". */
export function slotLabel(slot) {
  return String(slot).replace(/^[a-z][a-z-]*:/, '');
}

/** shortPath keeps a path's last two parts: ".codex/config.toml". */
export function shortPath(path) {
  const parts = String(path || '').split(/[\\/]/).filter(Boolean);
  return parts.slice(-2).join('/');
}

/** freeID is id, or id-2, id-3, … when taken by one of the providers. */
export function freeID(id, providers) {
  const taken = new Set((providers || []).map((p) => p.id));
  if (!taken.has(id)) return id;
  for (let i = 2; ; i++) if (!taken.has(`${id}-${i}`)) return `${id}-${i}`;
}
