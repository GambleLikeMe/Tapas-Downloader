const $ = (selector) => document.querySelector(selector);
const escapeHtml = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
}[char]));
function savedExpandedSeries() {
  try {
    const paths = JSON.parse(localStorage.getItem('expandedSeries') || '[]');
    return new Set(Array.isArray(paths) ? paths.filter((path) => typeof path === 'string') : []);
  } catch (_) {
    return new Set();
  }
}
const state = {
  accounts: [],
  saved: [],
  recentSearches: [],
  recentSeries: [],
  results: [],
  series: null,
  seriesAccount: '',
  selected: new Set(),
  tasks: [],
  library: [],
  settings: null,
  sortDesc: true,
  view: 'search',
  lastClicked: null,
  taskSignature: '',
  expandedSeries: savedExpandedSeries(),
};
let toastTimer;
let filenamePreviewVersion = 0;
const defaultFilenameTemplate = 'Episode {chapter_number}[{chapter_id}]';

function toast(message, error = false) {
  const node = $('#toast');
  node.textContent = message;
  node.classList.toggle('error', error);
  node.classList.add('show');

  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => node.classList.remove('show'), 4200);
}
async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
  });
  if (response.status === 401) throw new Error('Open the current link printed by the app.');
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`);
  return data;
}
const post = (path, data) => api(path, { method: 'POST', body: JSON.stringify(data) });
function show(view) {
  state.view = view;
  document.querySelectorAll('.view').forEach((node) => node.classList.toggle('active', node.id === view));
  document.querySelectorAll('.nav-button').forEach((node) => node.classList.toggle('active', node.dataset.view === view));
  if (view === 'history') {
    loadLibrary();
    renderHistory();
  }
  if (view === 'downloads') {
    renderDownloads();
    renderDownloadFolder();
  }
  if (view === 'settings') {
    loadCacheInfo();
    loadDebug().catch((error) => {
      $('#debug-output').textContent = error.message;
    });
  }
}
function imageMarkup(url, className = 'cover') {
  try {
    const parsed = new URL(url);
    if (parsed.protocol === 'https:') {
      return `<img class="${className}" src="${escapeHtml(parsed.href)}" alt="" loading="lazy">`;
    }
  } catch (_) {
    // The API sometimes omits a cover.
  }

  return `<div class="${className}" aria-hidden="true"></div>`;
}

function creatorNames(creators) {
  return (creators || [])
    .map((creator) => creator.display_name)
    .filter(Boolean)
    .join(', ');
}
function renderAccounts() {
  const select = $('#account-select');
  const preferred = select.value || localStorage.getItem('activeAccount') || '';
  select.innerHTML = state.accounts.length ? state.accounts.map((account) => `<option value="${escapeHtml(account.id)}">${escapeHtml(account.email)}</option>`).join('') : '<option value="">No account connected</option>';
  if (state.accounts.some((account) => account.id === preferred)) select.value = preferred;
  $('#account-list').innerHTML = state.accounts.length ? state.accounts.map((account) => `<div class="account-row"><strong>${escapeHtml(account.email)}</strong><button class="button link danger account-delete" type="button" data-id="${escapeHtml(account.id)}">Remove</button></div>`).join('') : '<p class="field-note">No account connected yet.</p>';
}
function renderSaved() {
  $('#saved-list').innerHTML = state.saved.length ? state.saved.map((query) => `<span class="saved-chip"><button class="saved-open" type="button" data-query="${escapeHtml(query)}">${escapeHtml(query)}</button><button class="remove saved-delete" type="button" data-query="${escapeHtml(query)}" aria-label="Remove saved search">×</button></span>`).join('') : '<span class="field-note">Saved searches appear here.</span>';
}
function renderRecentActivity() {
  $('#recent-searches').innerHTML = state.recentSearches.length ? state.recentSearches.map((query) => `<span class="saved-chip"><button class="recent-search-open" type="button" data-query="${escapeHtml(query)}">${escapeHtml(query)}</button><button class="remove recent-search-remove" type="button" data-query="${escapeHtml(query)}" aria-label="Remove recent search">×</button></span>`).join('') : '<span class="field-note">Searches you run will appear here.</span>';
  $('#recent-series-list').innerHTML = state.recentSeries.map((series) => `<button class="recent-series-row" type="button" data-id="${series.id}"><span>${escapeHtml(series.title)}</span><small>View chapters →</small></button>`).join('');
  $('#recent-series-section').classList.toggle('hidden', !state.recentSeries.length || state.results.length > 0);
  $('#recent-search-count').textContent = `${state.recentSearches.length} recent searches`;
  $('#recent-opened-count').textContent = `${state.recentSeries.length} series`;
}
async function loadState() { const data = await api('/api/state');
state.accounts = data.accounts;
state.saved = data.saved;
state.recentSearches = data.recentSearches || [];
state.recentSeries = data.recentSeries || [];
renderAccounts();
renderSaved();
renderRecentActivity(); }
async function loadCacheInfo() {
  try { const data = await api('/api/cache');
const size = data.bytes < 1024 ? `${data.bytes} B` : `${(data.bytes / 1024).toFixed(1)} KB`;
$('#cache-summary').textContent = `${data.entries} series · ${size} · refreshes after ${data.ttlMinutes} min`; }
  catch (error) { $('#cache-summary').textContent = error.message; }
}
function renderDebug(data) {
  $('#debug-enabled').checked = !!data.enabled;
  $('#debug-output').textContent = data.enabled
    ? (data.entries || []).map((entry) => `${new Date(entry.at).toLocaleString()}  ${entry.message}`).join('\n') || 'No debug events yet.'
    : 'Debug logs are off.';
}
async function loadDebug() {
  renderDebug(await api('/api/debug'));
}
function renderDownloadFolder() {
  $('#download-folder').textContent = state.settings?.downloadDir || 'No folder selected';
}
async function browseDownloadFolder() {
  const choice = await post('/api/folder/pick', {});
  if (choice.canceled) return;
  const current = state.settings || await api('/api/settings');
  state.settings = await post('/api/settings', { downloadDir: choice.path, defaultFormat: current.defaultFormat, filenameTemplate: current.filenameTemplate });
  $('#settings-form').elements.downloadDir.value = choice.path;
  $('#download-config').elements.directory.value = choice.path;
  renderDownloadFolder();
  toast('Download folder saved.');
}
async function loadSettings() {
  state.settings = await api('/api/settings');
  const form = $('#settings-form');
form.elements.downloadDir.value = state.settings.downloadDir;
form.elements.defaultFormat.value = state.settings.defaultFormat;
form.elements.filenameTemplate.value = state.settings.filenameTemplate || defaultFilenameTemplate;
  updateFilenamePreview();
renderDownloadFolder();
}
async function updateFilenamePreview() {
  const template = $('#filename-template').value;
  const version = ++filenamePreviewVersion;
  const save = $('#settings-form button[type="submit"]');
  save.disabled = true;
  if (!template.trim()) { $('#filename-preview').textContent = '—';
$('#filename-error').textContent = 'Enter a filename template.';
return; }
  try {
    const result = await api(`/api/filename-preview?template=${encodeURIComponent(template)}`);
    if (version !== filenamePreviewVersion) return;
    $('#filename-preview').textContent = result.preview;
    $('#filename-error').textContent = '';
    save.disabled = false;
  } catch (error) {
    if (version !== filenamePreviewVersion) return;
    $('#filename-preview').textContent = '—';
    $('#filename-error').textContent = error.message;
  }
}
function loadingResults() { $('#search-results').innerHTML = '<div class="loading-row"><div class="skeleton cover"></div><div><div class="skeleton line"></div><div class="skeleton short"></div></div></div>'.repeat(4); }
async function search() {
  const query = $('#query').value.trim();
const account = $('#account-select').value;
  if (!account) { show('settings');
toast('Connect a Tapas account first.', true);
return; }
  if (!query) return;
  state.series = null;
$('#search-home').classList.remove('hidden');
$('#series-panel').classList.add('hidden');
show('search');
  const button = $('#search-form button[type="submit"]');
button.disabled = true;
loadingResults();
  $('#recent-series-section').classList.add('hidden');
  try { state.results = await api(`/api/search?q=${encodeURIComponent(query)}&account=${encodeURIComponent(account)}`);
renderResults();
await loadState(); }
  catch (error) { $('#search-results').innerHTML = `<div class="task-empty">${escapeHtml(error.message)}</div>`;
toast(error.message, true); }
  finally { button.disabled = false; }
}
function renderResults() {
  const results = state.results;
  if (!results.length) { $('#search-results').innerHTML = '<div class="empty-state"><span class="empty-icon">⌕</span><strong>No comics found</strong><span>Try a different title or paste a series URL.</span></div>';
renderRecentActivity();
return; }
  $('#recent-series-section').classList.add('hidden');
  $('#search-results').innerHTML = `<div class="result-meta">${results.length} ${results.length === 1 ? 'RESULT' : 'RESULTS'}</div><div class="result-list">${results.map((item) => `<div class="result-row" data-id="${item.id}">${imageMarkup(item.thumb_url)}<div class="result-copy"><button class="result-title" type="button">${escapeHtml(item.title)}</button><small>${escapeHtml(creatorNames(item.creators))}</small><p>${escapeHtml(item.description)}</p></div><button class="button subtle result-open" type="button">View chapters →</button></div>`).join('')}</div>`;
}
async function openSeries(id, refresh = false) {
  const account = $('#account-select').value;
  const refreshButton = $('#refresh-chapters');
  const previousSelection = refresh ? new Set(state.selected) : null;
  if (refresh) { refreshButton.disabled = true; refreshButton.textContent = 'Refreshing…'; }
  else {
    $('#search-home').classList.add('hidden');
$('#series-panel').classList.remove('hidden');
    $('#series-summary').innerHTML = '<div class="loading-row"><div class="skeleton cover"></div><div><div class="skeleton line"></div><div class="skeleton short"></div></div></div>';
    $('#chapter-list').innerHTML = '';
state.selected.clear();
updateSelection();
  }
  try {
    const series = await api(`/api/series?id=${encodeURIComponent(id)}&account=${encodeURIComponent(account)}${refresh ? '&refresh=1' : ''}`);
    state.series = series;
state.seriesAccount = account;
state.lastClicked = null;
    if (refresh) state.selected = new Set([...previousSelection].filter((selectedId) => series.episodes.some((episode) => episode.id === selectedId && episode.access !== 'locked')));
    $('#series-context').textContent = `${series.episodes.length} CHAPTERS`;
    $('#series-summary').innerHTML = `<div class="series-summary">${imageMarkup(series.thumbUrl)}<div><h1>${escapeHtml(series.title)}</h1><div class="creator">${escapeHtml(creatorNames(series.creators))}</div><p class="description">${escapeHtml(series.description)}</p></div><span class="chapter-total">${series.episodes.length} chapters</span></div>`;
    renderChapters();
    if (!refresh) await loadState();
    else toast('Chapters refreshed.');
  } catch (error) { if (!refresh) { $('#search-home').classList.remove('hidden');
$('#series-panel').classList.add('hidden'); } toast(error.message, true); }
  finally { if (refresh) { refreshButton.disabled = false; refreshButton.textContent = 'Refresh chapters'; } }
}
function taskForChapter(id) {
  return [...state.tasks].reverse().find((task) => state.series && task.seriesId === state.series.id && task.episodeId === id && !['failed', 'canceled'].includes(task.state));
}
function visibleChapters() {
  if (!state.series) return [];
  const query = $('#chapter-query').value.trim().toLowerCase();
const owned = $('#owned-only').checked;
const filter = $('#chapter-filter').value;
  const rows = state.series.episodes.filter((episode) => {
    if (episode.scene <= 0) return false;
    if (query && !`${episode.scene} ${episode.title}`.toLowerCase().includes(query)) return false;
    if (owned && episode.access === 'locked') return false;
    const downloaded = episode.downloaded || state.tasks.some((task) => task.seriesId === state.series.id && task.episodeId === episode.id && task.state === 'completed');
    if (filter === 'available' && episode.access === 'locked') return false;
    if (filter === 'locked' && episode.access !== 'locked') return false;
    if (filter === 'downloaded' && !downloaded) return false;
    return true;
  });
  rows.sort((a, b) => state.sortDesc ? b.scene - a.scene : a.scene - b.scene);
  return rows;
}
function chapterStatus(episode) {
  const task = taskForChapter(episode.id);
  if (task && ['queued', 'preparing', 'downloading', 'converting', 'canceling'].includes(task.state)) return { label: task.state === 'queued' ? 'Queued' : task.state === 'converting' ? 'Converting' : 'Downloading', className: 'queue' };
  if (episode.downloaded || task?.state === 'completed') return { label: 'Downloaded', className: 'downloaded' };
  if (episode.access === 'unlocked') return { label: 'Unlocked', className: '' };
  if (episode.access === 'free') return { label: 'Free', className: '' };
  return { label: 'Locked', className: 'locked' };
}
function renderChapters() {
  const visible = visibleChapters();
  $('#visible-count').textContent = `${visible.length} ${visible.length === 1 ? 'chapter' : 'chapters'} shown`;
  $('#chapter-list').innerHTML = visible.length ? visible.map((episode) => { const status = chapterStatus(episode);
return `<label class="chapter-row ${state.selected.has(episode.id) ? 'selected' : ''}"><input type="checkbox" data-id="${episode.id}" ${state.selected.has(episode.id) ? 'checked' : ''} ${episode.access === 'locked' ? 'disabled' : ''} aria-label="Select chapter ${episode.scene}: ${escapeHtml(episode.title)}"><span class="chapter-number">${episode.scene}</span><span class="chapter-title">${escapeHtml(episode.title || `Chapter ${episode.scene}`)}</span><span class="chapter-tags"><span class="state-label ${status.className}">${status.label}</span></span></label>`; }).join('') : '<div class="task-empty">No chapters match these filters.</div>';
  updateSelection();
}
function updateSelection() {
  const count = state.selected.size;
  $('#selection-count').textContent = `${count} ${count === 1 ? 'chapter' : 'chapters'} selected`;
  $('#configure-download').textContent = count ? `Download ${count} ${count === 1 ? 'chapter' : 'chapters'}` : 'Download selected';
  $('#configure-download').disabled = count === 0;
}
function selectVisible() { visibleChapters().filter((episode) => episode.access !== 'locked').forEach((episode) => state.selected.add(episode.id));
renderChapters(); }
function openDialog() {
  if (!state.selected.size || !state.series) return;
  const selected = state.series.episodes.filter((episode) => state.selected.has(episode.id));
  $('#dialog-title').textContent = `Download ${selected.length} ${selected.length === 1 ? 'chapter' : 'chapters'}`;
  $('#dialog-summary').textContent = state.series.title;
  const form = $('#download-config');
  form.elements.directory.value = state.settings?.downloadDir || '';
  form.elements.format.value = state.settings?.defaultFormat || 'pdf';
  $('#download-dialog').classList.remove('hidden');
  form.elements.directory.focus();
}
function closeDialog() { $('#download-dialog').classList.add('hidden'); }
async function startDownload(event) {
  event.preventDefault();
  const selected = state.series.episodes.filter((episode) => state.selected.has(episode.id)).sort((a, b) => a.scene - b.scene);
  const form = event.currentTarget;
  if (selected.some((episode) => episode.access === 'locked')) { toast('Locked chapters are unavailable for download.', true);
return; }
  const button = $('#start-download');
button.disabled = true;
button.textContent = 'Adding to queue…';
  try {
    const result = await post('/api/tasks', { account: state.seriesAccount, seriesId: state.series.id, episodeIds: selected.map((episode) => episode.id), format: form.elements.format.value, directory: form.elements.directory.value });
    closeDialog();
state.selected.clear();
renderChapters();
toast(`${result.queued} ${result.queued === 1 ? 'chapter' : 'chapters'} added to the queue.`);
    await loadTasks();
show('downloads');
  } catch (error) { toast(error.message, true); }
  finally { button.disabled = false;
button.textContent = 'Start download'; }
}
function taskActions(task) {
  if (['queued', 'preparing', 'downloading', 'converting', 'canceling'].includes(task.state)) return `<button class="button link danger task-action" data-id="${task.id}" data-action="cancel" ${task.state === 'canceling' ? 'disabled' : ''}>Cancel</button>`;
  if (task.state === 'failed' || task.state === 'canceled') return `<button class="button subtle task-action" data-id="${task.id}" data-action="retry">Retry</button><button class="button link task-action" data-id="${task.id}" data-action="remove">Dismiss</button>`;
  if (task.state === 'completed') return `${task.format !== 'raw' ? `<button class="button subtle task-action" data-id="${task.id}" data-action="open-file">Open file</button><a class="button link" href="/api/tasks/file?id=${encodeURIComponent(task.id)}">Save copy</a>` : ''}<button class="button subtle task-action" data-id="${task.id}" data-action="open">Open folder</button><button class="button link task-action" data-id="${task.id}" data-action="remove">Dismiss</button>`;
  return '';
}
function taskMarkup(task) {
  const progress = task.imagesTotal ? Math.round(task.imagesDone / task.imagesTotal * 100) : 0;
  const busy = ['downloading', 'preparing', 'converting', 'canceling'].includes(task.state);
  const details = task.state === 'failed' ? task.message : task.state === 'downloading' && task.imagesTotal ? `${task.imagesDone} / ${task.imagesTotal} images saved` : task.message;
  return `<div class="task-row"><div><div class="task-row-title">${escapeHtml(task.seriesTitle)} <span>·</span> ${escapeHtml(task.episodeTitle || `Chapter ${task.scene}`)}</div><div class="task-subline ${task.state === 'failed' ? 'error' : ''}"><span class="task-state ${task.state}">${escapeHtml(task.state.toUpperCase())}</span> · ${escapeHtml(details)} · ${task.format === 'raw' ? 'IMAGES' : task.format.toUpperCase()}</div>${busy && task.imagesTotal ? `<div class="task-progress" role="progressbar" aria-valuenow="${task.imagesDone}" aria-valuemin="0" aria-valuemax="${task.imagesTotal}"><div style="width:${progress}%"></div></div>` : ''}</div><div class="task-actions">${taskActions(task)}</div></div>`;
}
function renderDownloads() {
  const active = state.tasks.filter((task) => !['completed', 'failed', 'canceled'].includes(task.state));
  const failed = state.tasks.filter((task) => task.state === 'failed');
  const recent = state.tasks.filter((task) => task.state === 'completed').slice(-5).reverse();
  $('#active-count').textContent = String(active.length);
  $('#queue-badge').textContent = String(active.length);
$('#queue-badge').classList.toggle('hidden', active.length === 0);
  $('#active-tasks').innerHTML = active.length || failed.length ? [...active, ...failed].map(taskMarkup).join('') : '<div class="task-empty">No downloads in progress. Select chapters from Search to start.</div>';
  $('#recent-tasks').innerHTML = recent.length ? recent.map(taskMarkup).join('') : '<div class="task-empty">Completed chapters will appear here.</div>';
}
async function loadTasks() {
  const tasks = await api('/api/tasks');
  const signature = JSON.stringify(tasks.map((task) => [task.id, task.state, task.imagesDone, task.imagesTotal, task.message, task.result]));
  const chapterStateBefore = JSON.stringify(state.tasks.map((task) => [task.id, task.state]));
  const chapterStateAfter = JSON.stringify(tasks.map((task) => [task.id, task.state]));
  state.tasks = tasks;
  if (signature !== state.taskSignature) {
    state.taskSignature = signature;
renderDownloads();
    if (state.view === 'history') renderHistory();
    if (chapterStateBefore !== chapterStateAfter && state.series && !$('#series-panel').classList.contains('hidden')) renderChapters();
  }
}
async function taskAction(button) {
  const action = button.dataset.action;
const id = button.dataset.id;
  button.disabled = true;
  try {
    await post(`/api/tasks/${action}`, { id });
    if (action === 'open') toast('Opened download folder.');
    if (action === 'open-file') toast('Opened downloaded file.');
    await loadTasks();
  } catch (error) { toast(error.message, true);
button.disabled = false; }
}
async function loadLibrary() { try { state.library = await api('/api/library');
renderHistory(); } catch (error) { toast(error.message, true); } }
function diskActions(item) {
  const path = escapeHtml(item.path);
  return `<div class="history-actions"><button class="button subtle disk-export" data-path="${path}" data-format="pdf">${item.pdf ? 'Make another PDF' : 'Make PDF'}</button><button class="button subtle disk-export" data-path="${path}" data-format="epub">${item.epub ? 'Make another EPUB' : 'Make EPUB'}</button>${item.pdf ? `<a class="button link" href="/api/file?path=${encodeURIComponent(`${item.path}/${item.name}.pdf`)}">Save PDF</a>` : ''}${item.epub ? `<a class="button link" href="/api/file?path=${encodeURIComponent(`${item.path}/${item.name}.epub`)}">Save EPUB</a>` : ''}</div>`;
}
function renderHistory() {
  const completed = state.tasks.filter((task) => task.state === 'completed').reverse();
  $('#history-tasks').innerHTML = completed.length ? completed.map(taskMarkup).join('') : '<div class="task-empty">No completed queue items yet.</div>';
  $('#disk-history').innerHTML = state.library.length ? state.library.map((series) => {
    const expanded = state.expandedSeries.has(series.path);
    const count = series.episodes.length + series.files.length;
    const episodes = series.episodes.map((episode) => `<div class="history-episode"><span class="title">${escapeHtml(episode.name)} <small>· ${episode.images} images</small></span>${diskActions(episode)}</div>`).join('');
    const files = series.files.map((file) => `<div class="history-episode"><span class="title">${escapeHtml(file.name)} <small>· ${escapeHtml(file.format.toUpperCase())}</small></span><a class="button link" href="/api/file?path=${encodeURIComponent(file.path)}">Save ${escapeHtml(file.format.toUpperCase())}</a></div>`).join('');
    return `<div class="history-series"><div class="history-series-header"><button class="history-toggle" type="button" data-path="${escapeHtml(series.path)}" aria-expanded="${expanded}"><span class="history-chevron" aria-hidden="true">${expanded ? '▾' : '▸'}</span><strong>${escapeHtml(series.title)}</strong><small>${count} downloads</small></button>${series.episodes.length ? diskActions(series) : ''}</div><div class="history-series-body ${expanded ? '' : 'hidden'}">${episodes}${files}</div></div>`;
  }).join('') : '<div class="task-empty">No downloaded files found in the original downloads folder.</div>';
}
async function exportDisk(button) {
  button.disabled = true;
const label = button.textContent;
button.textContent = 'Converting…';
  try { const result = await post('/api/export', { path: button.dataset.path, format: button.dataset.format });
await loadLibrary();
toast(`${button.dataset.format.toUpperCase()} is ready.`);
window.location.href = `/api/file?path=${encodeURIComponent(result.path)}`; }
  catch (error) { toast(error.message, true);
button.disabled = false;
button.textContent = label; }
}

for (const button of document.querySelectorAll('[data-view]')) button.addEventListener('click', () => show(button.dataset.view));
$('#account-select').addEventListener('change', () => { localStorage.setItem('activeAccount', $('#account-select').value);
if (state.series) openSeries(state.series.id); });
$('#search-form').addEventListener('submit', (event) => { event.preventDefault();
search(); });
$('#search-results').addEventListener('click', (event) => { const row = event.target.closest('.result-row');
if (!row) return;
const control = event.target.closest('button, a, input, select, textarea');
if (control && !control.matches('.result-open, .result-title')) return;
openSeries(Number(row.dataset.id)); });
$('#back-to-results').addEventListener('click', () => { $('#series-panel').classList.add('hidden');
$('#search-home').classList.remove('hidden'); });
$('#refresh-chapters').addEventListener('click', () => { if (state.series) openSeries(state.series.id, true); });
$('#recent-series-list').addEventListener('click', (event) => { const button = event.target.closest('[data-id]');
if (button) openSeries(Number(button.dataset.id)); });
$('#recent-searches').addEventListener('click', async (event) => { const open = event.target.closest('.recent-search-open');
if (open) { $('#query').value = open.dataset.query;
search();
return; } const remove = event.target.closest('.recent-search-remove');
if (remove) { try { await post('/api/history/searches/remove', { query: remove.dataset.query });
await loadState(); } catch (error) { toast(error.message, true); } } });
for (const button of document.querySelectorAll('.clear-activity')) button.addEventListener('click', async () => { try { await post(`/api/history/${button.dataset.kind}/clear`, {});
await loadState();
toast('History cleared.'); } catch (error) { toast(error.message, true); } });
$('#save-query').addEventListener('click', async () => { try { await post('/api/saved', { query: $('#query').value });
await loadState();
toast('Search saved.'); } catch (error) { toast(error.message, true); } });
$('#saved-list').addEventListener('click', async (event) => {
  const open = event.target.closest('.saved-open');
if (open) { $('#query').value = open.dataset.query;
search();
return; }
  const remove = event.target.closest('.saved-delete');
if (remove) { try { await post('/api/saved/delete', { query: remove.dataset.query });
await loadState(); } catch (error) { toast(error.message, true); } }
});
$('#chapter-query').addEventListener('input', renderChapters);
$('#owned-only').addEventListener('change', () => { if ($('#owned-only').checked && $('#chapter-filter').value === 'locked') $('#chapter-filter').value = 'all';
renderChapters(); });
$('#chapter-filter').addEventListener('change', () => { if ($('#chapter-filter').value === 'locked') $('#owned-only').checked = false;
renderChapters(); });
$('#sort-chapters').addEventListener('click', () => { state.sortDesc = !state.sortDesc;
$('#sort-chapters').textContent = state.sortDesc ? 'Newest first ↓' : 'Oldest first ↑';
renderChapters(); });
$('#select-visible').addEventListener('click', selectVisible);
$('#clear-selection').addEventListener('click', () => { state.selected.clear();
renderChapters(); });
$('#chapter-list').addEventListener('click', (event) => {
  const input = event.target.closest('input[type="checkbox"]');
if (!input) return;
  const id = Number(input.dataset.id);
const checked = input.checked;
const visible = visibleChapters();
  if (event.shiftKey && state.lastClicked !== null) {
    const start = visible.findIndex((episode) => episode.id === state.lastClicked);
    const end = visible.findIndex((episode) => episode.id === id);
    if (start >= 0 && end >= 0) for (let i = Math.min(start, end); i <= Math.max(start, end); i++) { if (checked && visible[i].access !== 'locked') state.selected.add(visible[i].id); else if (!checked) state.selected.delete(visible[i].id); }
  } else { if (checked) state.selected.add(id); else state.selected.delete(id); }
  state.lastClicked = id;
renderChapters();
});
$('#configure-download').addEventListener('click', openDialog);
$('#close-dialog').addEventListener('click', closeDialog);
$('#cancel-dialog').addEventListener('click', closeDialog);
$('#download-dialog').addEventListener('click', (event) => { if (event.target.id === 'download-dialog') closeDialog(); });
$('#download-config').addEventListener('submit', startDownload);
for (const selector of ['#active-tasks', '#recent-tasks', '#history-tasks']) $(selector).addEventListener('click', (event) => { const button = event.target.closest('.task-action');
if (button) taskAction(button); });
$('#disk-history').addEventListener('click', (event) => {
  const action = event.target.closest('.disk-export');
  if (action) { exportDisk(action);
return; }
  const header = event.target.closest('.history-series-header');
  if (!header || (event.target.closest('button, a') && !event.target.closest('.history-toggle'))) return;
  const toggle = header.querySelector('.history-toggle');
  const path = toggle.dataset.path;
  const expanded = !state.expandedSeries.has(path);
  if (expanded) state.expandedSeries.add(path); else state.expandedSeries.delete(path);
  try { localStorage.setItem('expandedSeries', JSON.stringify([...state.expandedSeries])); } catch (_) { /* The current view still works without storage. */ }
  toggle.setAttribute('aria-expanded', String(expanded));
  toggle.querySelector('.history-chevron').textContent = expanded ? '▾' : '▸';
  header.nextElementSibling.classList.toggle('hidden', !expanded);
});
$('#refresh-history').addEventListener('click', loadLibrary);
$('#clear-cache').addEventListener('click', async () => { try { await post('/api/cache/clear', {});
await loadCacheInfo();
toast('Cache cleared.'); } catch (error) { toast(error.message, true); } });
$('#filename-template').addEventListener('input', updateFilenamePreview);
$('#reset-filename').addEventListener('click', () => { const input = $('#filename-template'); input.value = defaultFilenameTemplate; input.dispatchEvent(new Event('input')); input.focus(); });
$('.template-variables').addEventListener('click', (event) => { const button = event.target.closest('[data-variable]');
if (!button) return;
const input = $('#filename-template');
const cursor = input.selectionStart ?? input.value.length; input.setRangeText(`{${button.dataset.variable}}`, cursor, input.selectionEnd ?? cursor, 'end'); input.dispatchEvent(new Event('input')); input.focus(); });
for (const id of ['#settings-folder-browse', '#download-folder-browse', '#dialog-folder-browse']) {
  $(id).addEventListener('click', async (event) => {
    const button = event.currentTarget;
button.disabled = true;
    try { await browseDownloadFolder(); }
    catch (error) { toast(error.message, true); }
    finally { button.disabled = false; }
  });
}
$('#settings-form').addEventListener('submit', async (event) => {
  event.preventDefault();
const form = event.currentTarget;
const button = form.querySelector('button');
button.disabled = true;
  try { state.settings = await post('/api/settings', { downloadDir: form.elements.downloadDir.value, defaultFormat: form.elements.defaultFormat.value, filenameTemplate: form.elements.filenameTemplate.value });
renderDownloadFolder();
toast('Settings saved.'); }
  catch (error) { toast(error.message, true); } finally { button.disabled = !!$('#filename-error').textContent; }
});
$('#debug-enabled').addEventListener('change', async (event) => {
  const checkbox = event.currentTarget; checkbox.disabled = true;
  try { renderDebug(await post('/api/debug', { enabled: checkbox.checked })); }
  catch (error) { checkbox.checked = !checkbox.checked;
toast(error.message, true); }
  finally { checkbox.disabled = false; }
});
$('#debug-clear').addEventListener('click', async () => {
  try { renderDebug(await post('/api/debug/clear', {})); }
  catch (error) { toast(error.message, true); }
});
$('#account-form').addEventListener('submit', async (event) => {
  event.preventDefault();
const form = event.currentTarget;
const button = form.querySelector('button');
button.disabled = true;
$('#account-error').textContent = '';
  try { const account = await post('/api/accounts', { email: form.elements.email.value, password: form.elements.password.value });
form.reset();
await loadState();
$('#account-select').value = account.id; localStorage.setItem('activeAccount', account.id);
show('search');
toast('Account connected.'); }
  catch (error) { $('#account-error').textContent = error.message;
toast(error.message, true); } finally { button.disabled = false; }
});
$('#account-list').addEventListener('click', async (event) => {
  const button = event.target.closest('.account-delete');
if (!button || !confirm('Remove this local account?')) return;
  try { await post('/api/accounts/delete', { id: button.dataset.id });
await loadState();
toast('Account removed.'); }
  catch (error) { toast(error.message, true); }
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && !$('#download-dialog').classList.contains('hidden')) { closeDialog();
return; }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') { event.preventDefault();
show('search');
$('#series-panel').classList.add('hidden');
$('#search-home').classList.remove('hidden');
$('#query').focus(); }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'a' && document.activeElement === $('#chapter-list')) { event.preventDefault();
selectVisible(); }
});
async function connectSession() {
  const token = new URLSearchParams(location.hash.slice(1)).get('token');
  if (!token) return;
  const response = await fetch('/api/session', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token }) });
  if (!response.ok) throw new Error('This app link has expired. Open the current link printed by the server.');
  history.replaceState(null, '', location.pathname + location.search);
}
connectSession().then(() => Promise.all([loadState(), loadSettings(), loadTasks()])).then(() => { if (!state.accounts.length) show('settings'); }).catch((error) => toast(error.message, true));
setInterval(() => loadTasks().catch((error) => toast(error.message, true)), 1500);
setInterval(() => { if (state.view === 'settings' && $('#debug-enabled').checked) loadDebug().catch((error) => { $('#debug-output').textContent = error.message; }); }, 3000);
