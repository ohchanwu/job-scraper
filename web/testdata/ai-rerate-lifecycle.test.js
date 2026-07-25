'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const script = fs.readFileSync(path.join(__dirname, '..', 'ai-rerate.js'), 'utf8');
const activeCopy = 'AI로 다시 분석하는 중이에요 — 여러 공고를 한 번에 살펴보고 있어요. ☕';
const completedCopy = 'AI 평가가 완료됐어요. 새로운 평가 결과를 반영했습니다.';

class Storage {
  constructor() { this.values = new Map(); }
  get length() { return this.values.size; }
  key(index) { return Array.from(this.values.keys())[index] || null; }
  getItem(key) { return this.values.has(key) ? this.values.get(key) : null; }
  setItem(key, value) { this.values.set(key, String(value)); }
  removeItem(key) { this.values.delete(key); }
}

class Element {
  constructor(tagName, registry) {
    this.tagName = tagName;
    this.registry = registry;
    this.children = [];
    this.listeners = new Map();
    this.parentNode = null;
    this.dataset = {};
    this.disabled = false;
    this.hidden = false;
    this._id = '';
    this._text = '';
  }
  set id(value) { this._id = value; }
  get id() { return this._id; }
  set textContent(value) {
    this._text = String(value);
    this.children = [];
    if (this.id === 'rerate-log' && value === '') {
      this.registry.delete('rerate-status');
      this.registry.delete('rerate-progress');
    }
  }
  get textContent() {
    return this._text + this.children.map((child) => child.textContent || '').join('');
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    if (child.id) this.registry.set(child.id, child);
    return child;
  }
  removeChild(child) {
    this.children = this.children.filter((candidate) => candidate !== child);
    if (child.id) this.registry.delete(child.id);
    child.parentNode = null;
    return child;
  }
  addEventListener(name, listener) {
    if (!this.listeners.has(name)) this.listeners.set(name, []);
    this.listeners.get(name).push(listener);
  }
  dispatch(name, event = {}) {
    for (const listener of this.listeners.get(name) || []) listener(event);
  }
}

function response(status) {
  return { ok: true, status: 200, json: async () => status };
}

let tokenCounter = 0;

function makePage({ storage, state = null, navigationType = 'navigate' }) {
  const registry = new Map();
  const button = new Element('button', registry);
  button.id = 'rerate';
  button.dataset.surface = 'archive';
  const log = new Element('div', registry);
  log.id = 'rerate-log';
  const activity = new Element('span', registry);
  activity.id = 'rerate-activity';
  activity.hidden = true;
  registry.set(button.id, button);
  registry.set(log.id, log);
  registry.set(activity.id, activity);

  const windowListeners = new Map();
  const history = {
    state,
    replaceState(next) { this.state = next; }
  };
  const location = {
    reloads: 0,
    reload() { this.reloads++; }
  };
  const timers = new Map();
  let nextTimer = 1;
  const sources = [];
  const fetchQueue = [];
  const fetchCalls = [];

  class MockEventSource {
    constructor(url) {
      this.url = url;
      this.closed = false;
      this.listeners = new Map();
      sources.push(this);
    }
    addEventListener(name, listener) {
      if (!this.listeners.has(name)) this.listeners.set(name, []);
      this.listeners.get(name).push(listener);
    }
    emit(name, data) {
      for (const listener of this.listeners.get(name) || []) listener({ data: String(data) });
    }
    close() { this.closed = true; }
  }

  function fetch(url, options = {}) {
    fetchCalls.push({ url, options });
    const queued = fetchQueue.shift();
    if (!queued) return Promise.reject(new Error('no queued fetch response'));
    if (queued.kind === 'immediate') return Promise.resolve(response(queued.status));
    return new Promise((resolve, reject) => {
      queued.resolve = (status) => resolve(response(status));
      if (options.signal) {
        options.signal.addEventListener('abort', () => {
          const error = new Error('aborted');
          error.name = 'AbortError';
          reject(error);
        }, { once: true });
      }
    });
  }

  const document = {
    title: 'jobcron test',
    visibilityState: 'visible',
    getElementById(id) { return registry.get(id) || null; },
    createElement(tagName) { return new Element(tagName, registry); },
    createTextNode(text) { return { textContent: String(text), parentNode: null }; }
  };
  const window = {
    crypto: { randomUUID: () => `entry-token-${String(++tokenCounter).padStart(8, '0')}` },
    addEventListener(name, listener) {
      if (!windowListeners.has(name)) windowListeners.set(name, []);
      windowListeners.get(name).push(listener);
    }
  };
  const context = {
    window,
    document,
    history,
    location,
    performance: { getEntriesByType: () => [{ type: navigationType }] },
    sessionStorage: storage,
    EventSource: MockEventSource,
    AbortController,
    fetch,
    encodeURIComponent,
    JSON,
    Date,
    Math,
    Object,
    String,
    Boolean,
    setTimeout(listener) {
      const id = nextTimer++;
      timers.set(id, listener);
      return id;
    },
    clearTimeout(id) { timers.delete(id); }
  };
  vm.runInNewContext(script, context, { filename: 'ai-rerate.js' });

  return {
    button,
    history,
    location,
    sources,
    fetchCalls,
    queueStatus(status) { fetchQueue.push({ kind: 'immediate', status }); },
    deferStatus() {
      const deferred = { kind: 'deferred', resolve: null };
      fetchQueue.push(deferred);
      return deferred;
    },
    dispatchWindow(name, event = {}) {
      for (const listener of windowListeners.get(name) || []) listener(event);
    },
    click() { button.dispatch('click'); },
    inject(id, text) {
      const node = new Element('p', registry);
      node.id = id;
      node.textContent = text;
      log.appendChild(node);
    },
    text(id) { return registry.get(id)?.textContent || ''; },
    has(id) { return registry.has(id); },
    timerCount() { return timers.size; }
  };
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await new Promise((resolve) => setImmediate(resolve));
}

async function main() {
  const storage = new Storage();
  storage.setItem('jobcron:rerate-owner:archive:1', 'stale-entry-owner');
  storage.setItem('jobcron:rerate-notice:archive', JSON.stringify({
    entry_token: 'legacy-entry',
    run_id: '1',
    message: 'legacy notice'
  }));
  const running = {
    run_id: 1,
    run_token: 'process-a-run-1',
    owner_entry: '',
    state: 'running',
    status: activeCopy,
    progress: '공고 2/7 분석 중...'
  };

  // The server receives ownership in the request even when navigation happens
  // before the client can receive the SSE run event.
  const initiating = makePage({ storage });
  assert.equal(storage.getItem('jobcron:rerate-owner:archive:1'), null);
  assert.equal(storage.getItem('jobcron:rerate-notice:archive'), null);
  const ownerToken = initiating.history.state.jobcronRerateEntry;
  running.owner_entry = ownerToken;
  initiating.click();
  assert.match(initiating.sources[0].url, new RegExp(`entry=${ownerToken}$`));
  initiating.dispatchWindow('pagehide');

  const restoredOwner = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  restoredOwner.queueStatus(running);
  restoredOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(restoredOwner.button.disabled, true);
  assert.equal(restoredOwner.text('rerate-status'), activeCopy);
  assert.equal(restoredOwner.text('rerate-progress'), running.progress);

  // A different entry on the same surface cannot adopt the run.
  const nonOwner = makePage({ storage, navigationType: 'back_forward' });
  nonOwner.queueStatus(running);
  nonOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(nonOwner.button.disabled, false);
  assert.equal(nonOwner.has('rerate-status'), false);
  assert.equal(nonOwner.has('rerate-progress'), false);
  assert.equal(nonOwner.location.reloads, 0);

  // A status response that resolves after pagehide cannot mutate or reschedule.
  const late = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  const deferred = late.deferStatus();
  late.dispatchWindow('pageshow');
  await Promise.resolve();
  late.dispatchWindow('pagehide');
  deferred.resolve(running);
  await flush();
  assert.equal(late.has('rerate-status'), false);
  assert.equal(late.has('rerate-progress'), false);
  assert.equal(late.location.reloads, 0);
  assert.equal(late.timerCount(), 0);

  // Idle restoration clears stale BFCache status and progress.
  const idle = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  idle.inject('rerate-status', 'stale status');
  idle.inject('rerate-progress', 'stale progress');
  idle.queueStatus({ state: 'idle' });
  idle.dispatchWindow('pageshow');
  await flush();
  assert.equal(idle.has('rerate-status'), false);
  assert.equal(idle.has('rerate-progress'), false);

  // Failed state is shown once, then the unique run token clears it as handled.
  const failedStatus = { ...running, state: 'failed', message: '검토용 실패 상태' };
  const failed = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  failed.inject('rerate-progress', 'stale progress');
  failed.queueStatus(failedStatus);
  failed.dispatchWindow('pageshow');
  await flush();
  assert.equal(failed.text('rerate-status'), failedStatus.message);
  assert.equal(failed.has('rerate-progress'), false);
  assert.equal(storage.getItem('jobcron:rerate-handled:archive'), running.run_token);

  const handled = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  handled.inject('rerate-status', failedStatus.message);
  handled.inject('rerate-progress', 'stale progress');
  handled.queueStatus(failedStatus);
  handled.dispatchWindow('pageshow');
  await flush();
  assert.equal(handled.has('rerate-status'), false);
  assert.equal(handled.has('rerate-progress'), false);

  // A restarted process may reuse run_id=1, but its unique run token must not
  // inherit the prior process's handled state.
  const restartedStatus = {
    ...running,
    run_token: 'process-b-run-1',
    state: 'running',
    progress: '공고 1/2 분석 중...'
  };
  const restarted = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  restarted.queueStatus(restartedStatus);
  restarted.dispatchWindow('pageshow');
  await flush();
  assert.equal(restarted.button.disabled, true);
  assert.equal(restarted.text('rerate-progress'), restartedStatus.progress);

  // A terminal snapshot cannot reload or create a notice on a non-owner entry.
  storage.removeItem('jobcron:rerate-handled:archive');
  const doneStatus = {
    ...running,
    state: 'done',
    outcome: 'changed',
    message: '공고 2개를 모두 AI로 분석했어요.'
  };
  const doneNonOwner = makePage({ storage, navigationType: 'back_forward' });
  doneNonOwner.queueStatus(doneStatus);
  doneNonOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(doneNonOwner.location.reloads, 0);
  assert.equal(storage.getItem('jobcron:rerate-notice:archive'), null);
  assert.equal(storage.getItem('jobcron:rerate-handled:archive'), null);

  // The owner reloads once, sees one completion notice, then a manual reload is quiet.
  const doneOwner = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  doneOwner.queueStatus(doneStatus);
  doneOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(doneOwner.location.reloads, 1);
  assert.notEqual(storage.getItem('jobcron:rerate-notice:archive'), null);
  assert.equal(storage.getItem('jobcron:rerate-handled:archive'), running.run_token);

  const reloaded = makePage({ storage, state: initiating.history.state, navigationType: 'reload' });
  assert.equal(reloaded.text('rerate-status'), completedCopy);
  assert.equal(storage.getItem('jobcron:rerate-notice:archive'), null);
  const manualReload = makePage({ storage, state: initiating.history.state, navigationType: 'reload' });
  assert.equal(manualReload.has('rerate-status'), false);

  // Cached and partial terminal snapshots keep their server-specific outcome
  // copy when completion is discovered after navigating away.
  const cachedCopy = '이미 모든 공고가 AI로 평가됐습니다. 추가 토큰은 사용하지 않았어요.';
  const cachedStatus = {
    ...doneStatus,
    run_token: 'process-a-run-cached',
    outcome: 'cached',
    message: cachedCopy
  };
  const cachedOwner = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  cachedOwner.queueStatus(cachedStatus);
  cachedOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(cachedOwner.location.reloads, 1);
  const cachedReload = makePage({ storage, state: initiating.history.state, navigationType: 'reload' });
  assert.equal(cachedReload.text('rerate-status'), cachedCopy);

  const partialCopy = '공고 2/7개를 AI로 분석했어요 — 토큰을 아끼려고 한 번에 일정 개수만 분석해요. 더 보려면 다시 눌러주세요.';
  const partialStatus = {
    ...doneStatus,
    run_token: 'process-a-run-partial',
    outcome: 'partial',
    message: partialCopy
  };
  const partialOwner = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  partialOwner.queueStatus(partialStatus);
  partialOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(partialOwner.location.reloads, 1);
  const partialReload = makePage({ storage, state: initiating.history.state, navigationType: 'reload' });
  assert.equal(partialReload.text('rerate-status'), partialCopy);

  const noProgressCopy = '8개는 AI가 근거를 확인하지 못했어요. 지금 다시 눌러도 같은 결과일 수 있어요.';
  const noProgressStatus = {
    ...doneStatus,
    run_token: 'process-a-run-no-progress',
    outcome: 'no_progress',
    message: noProgressCopy
  };
  const noProgressOwner = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  noProgressOwner.queueStatus(noProgressStatus);
  noProgressOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(noProgressOwner.location.reloads, 1);
  const noProgressReload = makePage({ storage, state: initiating.history.state, navigationType: 'reload' });
  assert.equal(noProgressReload.text('rerate-status'), noProgressCopy);

  const emptyCopy = '지금 화면에 분석할 공고가 없어요.';
  const emptyStatus = {
    ...doneStatus,
    run_token: 'process-a-run-empty',
    outcome: 'empty',
    message: emptyCopy
  };
  const emptyOwner = makePage({ storage, state: initiating.history.state, navigationType: 'back_forward' });
  emptyOwner.queueStatus(emptyStatus);
  emptyOwner.dispatchWindow('pageshow');
  await flush();
  assert.equal(emptyOwner.location.reloads, 1);
  const emptyReload = makePage({ storage, state: initiating.history.state, navigationType: 'reload' });
  assert.equal(emptyReload.text('rerate-status'), emptyCopy);

  // The additive run-token event preserves the visible-page SSE flow.
  const visibleStorage = new Storage();
  const visible = makePage({ storage: visibleStorage });
  visible.click();
  visible.sources[0].emit('run-token', 'process-visible-run-1');
  visible.sources[0].emit('status', activeCopy);
  visible.sources[0].emit('progress', '공고 1/2 분석 중...');
  assert.equal(visible.button.disabled, true);
  assert.equal(visible.text('rerate-progress'), '공고 1/2 분석 중...');
  visible.sources[0].emit('done', '공고 2개를 모두 AI로 분석했어요.');
  assert.equal(visible.location.reloads, 1);
  assert.equal(visibleStorage.getItem('jobcron:rerate-handled:archive'), 'process-visible-run-1');
}

main().catch((error) => {
  console.error(error.stack || error);
  process.exitCode = 1;
});
