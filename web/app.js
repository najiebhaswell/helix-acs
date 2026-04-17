'use strict';

// ─────────────────────────────────────────────────────────────
//  State
// ─────────────────────────────────────────────────────────────
const S = {
  token: localStorage.getItem('helixToken') || '',
  currentSerial: null,   // device serial being viewed
  pollTimer: null,       // task-list polling timer
  taskModal: null,       // bootstrap modal instance
  confirmModal: null,
  tagsModal: null,
  pendingConfirm: null,  // function to call on confirm
  deviceFilter: { page: 1, limit: 20, manufacturer: '', model: '', online: '', tag: '', wan_ip: '' },
  taskPage: 1,
};

// ─────────────────────────────────────────────────────────────
//  API client
// ─────────────────────────────────────────────────────────────
const API = {
  async req(method, path, body) {
    const h = { 'Content-Type': 'application/json' };
    if (S.token) h['Authorization'] = 'Bearer ' + S.token;
    const res = await fetch(path, { method, headers: h, body: body != null ? JSON.stringify(body) : undefined });
    if (res.status === 401) { doLogout(); throw new Error('Unauthorized'); }
    if (res.status === 204) return null;
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    return data;
  },
  get:    (p)    => API.req('GET',    '/api/v1' + p),
  post:   (p, b) => API.req('POST',   '/api/v1' + p, b),
  put:    (p, b) => API.req('PUT',    '/api/v1' + p, b),
  del:    (p)    => API.req('DELETE', '/api/v1' + p),

  login: (u, p) => fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: u, password: p }),
  }).then(r => r.json()),

  health: () => fetch('/health').then(r => r.json()),
};

// ─────────────────────────────────────────────────────────────
//  Auth
// ─────────────────────────────────────────────────────────────
function doLogout() {
  S.token = '';
  localStorage.removeItem('helixToken');
  stopPoll();
  document.getElementById('app-shell').style.display = 'none';
  document.getElementById('login-screen').style.display = '';
}

// ─────────────────────────────────────────────────────────────
//  Sidebar (mobile)
// ─────────────────────────────────────────────────────────────
function toggleSidebar() {
  const sidebar  = document.getElementById('sidebar');
  const overlay  = document.getElementById('sidebar-overlay');
  const isOpen   = sidebar.classList.contains('open');
  sidebar.classList.toggle('open', !isOpen);
  overlay.classList.toggle('active', !isOpen);
}

function closeSidebar() {
  document.getElementById('sidebar').classList.remove('open');
  document.getElementById('sidebar-overlay').classList.remove('active');
}

// ─────────────────────────────────────────────────────────────
//  Theme (light / dark)
// ─────────────────────────────────────────────────────────────
function applyTheme(theme) {
  document.documentElement.setAttribute('data-theme', theme);
  localStorage.setItem('helixTheme', theme);

  const icon = document.getElementById('theme-toggle-icon');
  if (icon) {
    icon.className = theme === 'dark' ? 'bi bi-sun' : 'bi bi-moon';
  }

  const btn = document.getElementById('theme-toggle-btn');
  if (btn) btn.title = theme === 'dark' ? 'Light theme' : 'Dark theme';
}

function toggleTheme() {
  const current = document.documentElement.getAttribute('data-theme') || 'light';
  applyTheme(current === 'dark' ? 'light' : 'dark');
}

function initTheme() {
  const saved = localStorage.getItem('helixTheme') || 'light';
  applyTheme(saved);
}

// ─────────────────────────────────────────────────────────────
//  Toast notifications
// ─────────────────────────────────────────────────────────────
function toast(msg, type = 'success') {
  const c = document.getElementById('toast-container');
  const id = 'toast-' + Date.now();
  const icons = { success: 'check-circle-fill', danger: 'x-circle-fill', warning: 'exclamation-triangle-fill', info: 'info-circle-fill' };
  c.insertAdjacentHTML('beforeend', `
    <div id="${id}" class="toast align-items-center text-bg-${type} border-0 show mb-2" role="alert">
      <div class="d-flex">
        <div class="toast-body"><i class="bi bi-${icons[type]||'info-circle'} me-2"></i>${msg}</div>
        <button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast"></button>
      </div>
    </div>`);
  setTimeout(() => { const el = document.getElementById(id); if (el) el.remove(); }, 4000);
}

// ─────────────────────────────────────────────────────────────
//  Confirm modal
// ─────────────────────────────────────────────────────────────
function confirm(msg, cb) {
  document.getElementById('confirm-message').textContent = msg;
  S.pendingConfirm = cb;
  S.confirmModal.show();
}

// ─────────────────────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────────────────────
function fmtDate(d) {
  if (!d) return '';
  const dt = new Date(d);
  if (isNaN(dt)) return '';
  return dt.toLocaleString('en-US');
}

function fmtBytes(bytes) {
  if (!bytes) return '';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  if (bytes < 1024 * 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB';
  return (bytes / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}

function fmtUptime(seconds) {
  if (!seconds) return '';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function fmtRam(totalKB, freeKB) {
  if (!totalKB) return '';
  const usedKB = totalKB - freeKB;
  const pct = Math.round((usedKB / totalKB) * 100);
  return `${Math.round(usedKB / 1024)} MB / ${Math.round(totalKB / 1024)} MB (${pct}%)`;
}

function statusBadge(online) {
  return online
    ? `<span class="badge badge-online"><span class="status-dot dot-online"></span>Online</span>`
    : `<span class="badge badge-offline"><span class="status-dot dot-offline"></span>Offline</span>`;
}

function taskStatusBadge(status) {
  const labels = { pending: 'Pending', executing: 'Executing', done: 'Completed', failed: 'Failed', cancelled: 'Cancelled' };
  return `<span class="badge badge-${status}">${labels[status] || status}</span>`;
}

function tagBadges(tags) {
  if (!tags || tags.length === 0) return '<span class="text-muted small"></span>';
  return tags.map(t => `<span class="tag-badge">${escHtml(t)}</span>`).join('');
}

function extractOpticalInfo(params) {
  if (!params) return null;
  
  const optical = {};
  
  // TP-Link XPON parameter mappings from Device.Optical.Interface.1.X_TP_GPON_Config
  const mappings = {
    'PonType': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.PonType',
    ],
    'XPONStatus': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.XponStatus',
    ],
    'Status': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.Status',
    ],
    'Temperature': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.TransceiverTemperature',
    ],
    'SupplyVoltage': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.SupplyVottage',
      'Device.Optical.Interface.1.X_TP_GPON_Config.SupplyVoltage',
    ],
    'BiasCurrent': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.BiasCurrent',
    ],
    'TXPower': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.TXPower',
    ],
    'RXPower': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.RXPower',
    ],
    'FecDownstreamEnabled': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.FecDsEn',
    ],
    'FecUpstreamEnabled': [
      'Device.Optical.Interface.1.X_TP_GPON_Config.FecUsEn',
    ],
    'OMCIPacketsReceived': [
      'Device.Optical.Interface.1.X_TP_OMCIStats.PacketsReceived',
    ],
    'OMCIPacketsSent': [
      'Device.Optical.Interface.1.X_TP_OMCIStats.PacketsSent',
    ],
    'OpticalSignalLevel': [
      'Device.Optical.Interface.1.OpticalSignalLevel',
    ],
    'TransmitOpticalLevel': [
      'Device.Optical.Interface.1.TransmitOpticalLevel',
    ],
    'BytesReceived': [
      'Device.Optical.Interface.1.Stats.BytesReceived',
    ],
    'BytesSent': [
      'Device.Optical.Interface.1.Stats.BytesSent',
    ],
    'PacketsReceived': [
      'Device.Optical.Interface.1.Stats.PacketsReceived',
    ],
    'PacketsSent': [
      'Device.Optical.Interface.1.Stats.PacketsSent',
    ],
  };
  
  for (const [key, paths] of Object.entries(mappings)) {
    for (const path of paths) {
      if (params[path]) {
        optical[key] = params[path];
        break;
      }
    }
  }
  
  return Object.keys(optical).length > 0 ? optical : null;
}

function escHtml(s) {
  if (!s) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function loading(container = 'view-content') {
  document.getElementById(container).innerHTML =
    `<div class="text-center py-5"><div class="spinner-border text-primary"></div></div>`;
}

function stopPoll() {
  if (S.pollTimer) { clearInterval(S.pollTimer); S.pollTimer = null; }
}

function setTopbarUser(username) {
  const el = document.getElementById('topbar-username');
  if (el) el.textContent = username || 'Admin';
  const sidebar = document.getElementById('sidebar-username');
  if (sidebar) sidebar.textContent = username || 'Admin';
}

function setActive(route) {
  document.querySelectorAll('.sidebar-nav .nav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.route === route);
  });
  // Mirror the active nav item label in the topbar
  const active = document.querySelector('.sidebar-nav .nav-item[data-route="' + route + '"]');
  if (active) {
    const icon = active.querySelector('i');
    const label = active.querySelector('span') || active;
    const iconClass = icon ? icon.className : '';
    const text = label.textContent.trim();
    const el = document.getElementById('topbar-title');
    if (el) el.innerHTML = iconClass
      ? `<i class="${iconClass} me-2 text-primary" style="font-size:.9rem"></i>${escHtml(text)}`
      : escHtml(text);
  }
}

// ─────────────────────────────────────────────────────────────
//  Router
// ─────────────────────────────────────────────────────────────
function navigate(hash) {
  closeSidebar();
  window.location.hash = hash;
}

function routeTo(hash) {
  stopPoll();
  const path = hash.replace(/^#/, '') || '/';
  const parts = path.split('/').filter(Boolean);
  const base  = '/' + (parts[0] || '');

  if (base === '/devices' && parts[1]) {
    setActive('/devices');
    viewDeviceDetail(decodeURIComponent(parts[1]));
  } else if (base === '/devices') {
    setActive('/devices');
    viewDevices();
  } else if (base === '/health') {
    setActive('/health');
    viewHealth();
  } else {
    setActive('/');
    viewDashboard();
  }
}

window.addEventListener('hashchange', () => routeTo(window.location.hash));

// ─────────────────────────────────────────────────────────────
//  View: Dashboard
// ─────────────────────────────────────────────────────────────
async function viewDashboard() {
  const el = document.getElementById('view-content');
  el.innerHTML = `
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h5 class="fw-bold mb-0"><i class="bi bi-speedometer2 me-2 text-primary"></i>Dashboard</h5>
      <button class="btn btn-sm btn-outline-secondary" onclick="viewDashboard()">
        <i class="bi bi-arrow-clockwise"></i>
      </button>
    </div>
    <div class="row g-3 mb-4" id="dash-stats">
      <div class="col-12 text-center py-4"><div class="spinner-border text-primary"></div></div>
    </div>
    <div class="row g-3">
      <div class="col-md-6">
        <div class="card border-0 shadow-sm h-100">
          <div class="card-header bg-white fw-semibold">
            <i class="bi bi-hdd-network me-2 text-primary"></i>Recent Devices
          </div>
          <div id="dash-recent" class="card-body p-0">
            <div class="text-center py-3"><div class="spinner-border spinner-border-sm"></div></div>
          </div>
        </div>
      </div>
      <div class="col-md-6">
        <div class="card border-0 shadow-sm h-100">
          <div class="card-header bg-white fw-semibold">
            <i class="bi bi-heart-pulse me-2 text-danger"></i>System Health
          </div>
          <div id="dash-health" class="card-body">
            <div class="text-center py-3"><div class="spinner-border spinner-border-sm"></div></div>
          </div>
        </div>
      </div>
    </div>`;

  try {
    const [all, online, offline, healthData] = await Promise.all([
      API.get('/devices?limit=1'),
      API.get('/devices?online=true&limit=1'),
      API.get('/devices?online=false&limit=1'),
      API.health(),
    ]);

    const sysOk = healthData.status === 'OK';
    document.getElementById('dash-stats').innerHTML = `
      <div class="col-6 col-md-3">
        <div class="card stat-card h-100">
          <div class="card-body">
            <div class="stat-icon color-primary"><i class="bi bi-hdd-network"></i></div>
            <div class="stat-body">
              <div class="stat-value">${all.total ?? 0}</div>
              <div class="stat-label">Total</div>
            </div>
          </div>
        </div>
      </div>
      <div class="col-6 col-md-3">
        <div class="card stat-card h-100">
          <div class="card-body">
            <div class="stat-icon color-success"><i class="bi bi-check-circle"></i></div>
            <div class="stat-body">
              <div class="stat-value">${online.total ?? 0}</div>
              <div class="stat-label">Online</div>
            </div>
          </div>
        </div>
      </div>
      <div class="col-6 col-md-3">
        <div class="card stat-card h-100">
          <div class="card-body">
            <div class="stat-icon color-danger"><i class="bi bi-x-circle"></i></div>
            <div class="stat-body">
              <div class="stat-value">${offline.total ?? 0}</div>
              <div class="stat-label">Offline</div>
            </div>
          </div>
        </div>
      </div>
      <div class="col-6 col-md-3">
        <div class="card stat-card h-100">
          <div class="card-body">
            <div class="stat-icon ${sysOk ? 'color-success' : 'color-danger'}">
              <i class="bi bi-${sysOk ? 'heart-pulse' : 'exclamation-triangle'}"></i>
            </div>
            <div class="stat-body">
              <div class="stat-value" style="font-size:1.25rem;line-height:1.5">SYSTEM</div>
              <div class="stat-label">${sysOk ? 'HEALTHY' : 'DEGRADED'}</div>
            </div>
          </div>
        </div>
      </div>`;

    // Recent devices
    const recent = await API.get('/devices?limit=5&page=1');
    const rows = (recent.data || []).map(d => `
      <tr class="clickable" onclick="navigate('/devices/${encodeURIComponent(d.serial)}')">
        <td>${escHtml(d.serial)}</td>
        <td>${escHtml(d.manufacturer || '')}</td>
        <td>${statusBadge(d.online)}</td>
        <td class="text-muted small">${fmtDate(d.last_inform)}</td>
      </tr>`).join('');
    document.getElementById('dash-recent').innerHTML = rows.length
      ? `<table class="table table-sm table-hover mb-0"><thead><tr>
           <th>Serial</th><th>Manufacturer</th><th>Status</th><th>Last Inform</th>
         </tr></thead><tbody>${rows}</tbody></table>`
      : `<div class="empty-state"><i class="bi bi-inbox"></i>No devices</div>`;

    // Health
    const mkDot = ok => ok === 'OK'
      ? `<i class="bi bi-check-circle-fill text-success me-2"></i>`
      : `<i class="bi bi-x-circle-fill text-danger me-2"></i>`;
    document.getElementById('dash-health').innerHTML = `
      <ul class="list-group list-group-flush">
        <li class="list-group-item d-flex align-items-center">
          ${mkDot(healthData.mongodb)}<span>MongoDB</span>
          <span class="ms-auto badge ${healthData.mongodb==='OK'?'bg-success':'bg-danger'}">${healthData.mongodb === 'OK' ? 'HEALTHY' : 'DEGRADED'}</span>
        </li>
        <li class="list-group-item d-flex align-items-center">
          ${mkDot(healthData.redis)}<span>Redis</span>
          <span class="ms-auto badge ${healthData.redis==='OK'?'bg-success':'bg-danger'}">${healthData.redis === 'OK' ? 'HEALTHY' : 'DEGRADED'}</span>
        </li>
      </ul>`;
  } catch (e) {
    toast('Error loading dashboard: ' + e.message, 'danger');
  }
}

// ─────────────────────────────────────────────────────────────
//  View: Devices list
// ─────────────────────────────────────────────────────────────
async function viewDevices() {
  const el = document.getElementById('view-content');
  const f = S.deviceFilter;

  el.innerHTML = `
    <div class="d-flex justify-content-between align-items-center mb-3">
      <h5 class="fw-bold mb-0"><i class="bi bi-hdd-network me-2 text-primary"></i>Devices</h5>
    </div>

    <!-- Filter bar -->
    <div class="card border-0 shadow-sm mb-3">
      <div class="card-body py-2">
        <div class="row g-2 align-items-end">
          <div class="col-sm-2">
            <select class="form-select form-select-sm" id="f-online">
              <option value="">All</option>
              <option value="true" ${f.online==='true'?'selected':''}>Online</option>
              <option value="false" ${f.online==='false'?'selected':''}>Offline</option>
            </select>
          </div>
          <div class="col-sm-2">
            <input class="form-control form-control-sm" id="f-manufacturer" placeholder="Manufacturer" value="${escHtml(f.manufacturer)}">
          </div>
          <div class="col-sm-2">
            <input class="form-control form-control-sm" id="f-model" placeholder="Model" value="${escHtml(f.model)}">
          </div>
          <div class="col-sm-2">
            <input class="form-control form-control-sm" id="f-tag" placeholder="Tag" value="${escHtml(f.tag)}">
          </div>
          <div class="col-sm-2">
            <input class="form-control form-control-sm" id="f-wan" placeholder="WAN IP" value="${escHtml(f.wan_ip)}">
          </div>
          <div class="col-sm-2 d-flex gap-1">
            <button class="btn btn-primary btn-sm flex-fill" onclick="applyDeviceFilter()">
              <i class="bi bi-search"></i> Filter
            </button>
            <button class="btn btn-outline-secondary btn-sm" onclick="clearDeviceFilter()">
              <i class="bi bi-x"></i>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Table -->
    <div class="card border-0 shadow-sm">
      <div class="card-body p-0">
        <div id="devices-table">
          <div class="text-center py-5"><div class="spinner-border text-primary"></div></div>
        </div>
      </div>
    </div>`;

  await loadDevicesTable();
}

function applyDeviceFilter() {
  S.deviceFilter.online       = document.getElementById('f-online').value;
  S.deviceFilter.manufacturer = document.getElementById('f-manufacturer').value.trim();
  S.deviceFilter.model        = document.getElementById('f-model').value.trim();
  S.deviceFilter.tag          = document.getElementById('f-tag').value.trim();
  S.deviceFilter.wan_ip       = document.getElementById('f-wan').value.trim();
  S.deviceFilter.page         = 1;
  loadDevicesTable();
}

function clearDeviceFilter() {
  S.deviceFilter = { page: 1, limit: 20, manufacturer: '', model: '', online: '', tag: '', wan_ip: '' };
  viewDevices();
}

async function loadDevicesTable() {
  const f = S.deviceFilter;
  const params = new URLSearchParams();
  params.set('page', f.page);
  params.set('limit', f.limit);
  if (f.online)       params.set('online', f.online);
  if (f.manufacturer) params.set('manufacturer', f.manufacturer);
  if (f.model)        params.set('model', f.model);
  if (f.tag)          params.set('tag', f.tag);
  if (f.wan_ip)       params.set('wan_ip', f.wan_ip);

  try {
    const res = await API.get('/devices?' + params);
    const devices = res.data || [];
    const total = res.total || 0;
    const totalPages = Math.ceil(total / f.limit) || 1;

    const rows = devices.map(d => `
      <tr class="clickable" onclick="navigate('/devices/${encodeURIComponent(d.serial)}')">
        <td class="fw-semibold">${escHtml(d.serial)}</td>
        <td>${escHtml(d.manufacturer || '')}</td>
        <td>${escHtml(d.model_name || '')}</td>
        <td>${statusBadge(d.online)}</td>
        <td class="text-muted small">${escHtml(d.wan_ip || '')}</td>
        <td class="text-muted small">${fmtDate(d.last_inform)}</td>
        <td>${tagBadges(d.tags)}</td>
        <td onclick="event.stopPropagation()">
          <button class="btn btn-sm btn-outline-danger py-0" title="Delete"
                  onclick="deleteDevice('${escHtml(d.serial)}')">
            <i class="bi bi-trash"></i>
          </button>
        </td>
      </tr>`).join('');

    const table = rows.length
      ? `<table class="table table-hover align-middle mb-0">
           <thead><tr>
             <th>Serial</th><th>Manufacturer</th><th>Model</th><th>Status</th>
             <th>WAN IP</th><th>Last Inform</th><th>Tags</th><th></th>
           </tr></thead>
           <tbody>${rows}</tbody>
         </table>`
      : `<div class="empty-state"><i class="bi bi-inbox"></i>No devices found</div>`;

    // Pagination
    const pages = Array.from({ length: totalPages }, (_, i) => i + 1)
      .filter(p => p === 1 || p === totalPages || Math.abs(p - f.page) <= 2)
      .reduce((acc, p, i, arr) => {
        if (i > 0 && arr[i-1] < p - 1) acc.push('…');
        acc.push(p);
        return acc;
      }, []);

    const pagi = totalPages > 1 ? `
      <div class="d-flex justify-content-between align-items-center px-3 py-2 border-top">
        <small class="text-muted">Total: ${total}</small>
        <nav><ul class="pagination pagination-sm mb-0">
          <li class="page-item ${f.page===1?'disabled':''}">
            <a class="page-link" href="#" onclick="changePage(${f.page-1}); return false">‹</a>
          </li>
          ${pages.map(p => p === '…'
            ? `<li class="page-item disabled"><span class="page-link">…</span></li>`
            : `<li class="page-item ${p===f.page?'active':''}">
                 <a class="page-link" href="#" onclick="changePage(${p}); return false">${p}</a>
               </li>`).join('')}
          <li class="page-item ${f.page===totalPages?'disabled':''}">
            <a class="page-link" href="#" onclick="changePage(${f.page+1}); return false">›</a>
          </li>
        </ul></nav>
      </div>` : '';

    document.getElementById('devices-table').innerHTML = table + pagi;
  } catch (e) {
    document.getElementById('devices-table').innerHTML =
      `<div class="empty-state text-danger"><i class="bi bi-exclamation-triangle"></i>${e.message}</div>`;
  }
}

function changePage(p) {
  S.deviceFilter.page = p;
  loadDevicesTable();
}

async function deleteDevice(serial) {
  confirm(`Delete device "${serial}"? This action cannot be undone.`, async () => {
    try {
      await API.del(`/devices/${encodeURIComponent(serial)}`);
      toast(`Device ${serial} deleted.`);
      loadDevicesTable();
    } catch (e) { toast(e.message, 'danger'); }
  });
}

// ─────────────────────────────────────────────────────────────
//  View: Device detail
// ─────────────────────────────────────────────────────────────
async function viewDeviceDetail(serial) {
  S.currentSerial = serial;
  const el = document.getElementById('view-content');

  el.innerHTML = `
    <div class="d-flex align-items-center mb-3 gap-2">
      <button class="btn btn-sm btn-outline-secondary" onclick="navigate('/devices')">
        <i class="bi bi-arrow-left"></i>
      </button>
      <h5 class="fw-bold mb-0"><i class="bi bi-hdd me-2 text-primary"></i><span id="dev-title">Loading…</span></h5>
    </div>

    <!-- Device info header card -->
    <div id="dev-header-card" class="card border-0 shadow-sm mb-3">
      <div class="card-body py-2">
        <div class="text-center py-3"><div class="spinner-border spinner-border-sm"></div></div>
      </div>
    </div>

    <!-- Tabs -->
    <ul class="nav nav-tabs mb-3" id="dev-tabs">
      <li class="nav-item">
        <a class="nav-link active" data-bs-toggle="tab" href="#tab-info">
          <i class="bi bi-info-circle me-1"></i>Information
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-bs-toggle="tab" href="#tab-network">
          <i class="bi bi-diagram-3 me-1"></i>Network
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-bs-toggle="tab" href="#tab-hosts" onclick="loadHosts()">
          <i class="bi bi-people me-1"></i>Hosts
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-bs-toggle="tab" href="#tab-params" onclick="loadParams()">
          <i class="bi bi-table me-1"></i>Parameters
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-bs-toggle="tab" href="#tab-tasks" onclick="loadTasks()">
          <i class="bi bi-list-check me-1"></i>Tasks
        </a>
      </li>
    </ul>

    <div class="tab-content">
      <div class="tab-pane fade show active" id="tab-info">
        <div class="text-center py-5"><div class="spinner-border text-primary"></div></div>
      </div>
      <div class="tab-pane fade" id="tab-network">
        <div class="text-center py-5"><div class="spinner-border text-primary"></div></div>
      </div>
      <div class="tab-pane fade" id="tab-hosts">
        <div class="text-center py-5"><div class="spinner-border text-primary"></div></div>
      </div>
      <div class="tab-pane fade" id="tab-params">
        <div class="text-center py-5"><div class="spinner-border text-primary"></div></div>
      </div>
      <div class="tab-pane fade" id="tab-tasks">
        <div class="text-center py-5"><div class="spinner-border text-primary"></div></div>
      </div>
    </div>`;

  try {
    const dev = await API.get(`/devices/${encodeURIComponent(serial)}`);
    document.getElementById('dev-title').textContent = dev.serial;
    // Update topbar breadcrumb with device serial
    const topbarTitle = document.getElementById('topbar-title');
    if (topbarTitle) {
      topbarTitle.innerHTML =
        `<i class="bi bi-hdd-network me-2 text-primary" style="font-size:.9rem"></i>` +
        `<span class="text-muted fw-normal me-1" style="font-size:.8rem">Devices /</span>` +
        `<span>${escHtml(dev.serial)}</span>`;
    }
    renderDeviceHeader(dev);
    renderInfoTab(dev);
    renderNetworkTab(dev);
  } catch (e) {
    toast('Error while loading device: ' + e.message, 'danger');
  }
}

function renderDeviceHeader(dev) {
  const uptimeStr = dev.uptime_seconds ? fmtUptime(dev.uptime_seconds) : null;
  const ramStr    = dev.ram_total ? fmtRam(dev.ram_total, dev.ram_free) : null;
  const cpuStr    = dev.cpu_usage != null ? dev.cpu_usage + '%' : null;

  const field = (label, val) => val
    ? `<div class="dev-header-field">
         <span class="field-label">${label}</span>
         <span class="field-value">${escHtml(String(val))}</span>
       </div>` : '';

  document.getElementById('dev-header-card').innerHTML = `
    <div class="card-body">
      <div class="d-flex align-items-start gap-3 flex-wrap">
        <div class="dev-header-device-icon">
          <i class="bi bi-router"></i>
        </div>
        <div style="flex:1;min-width:0">
          <div class="d-flex align-items-center gap-2 mb-2 flex-wrap">
            <span class="fw-bold" style="font-size:1rem">${escHtml(dev.manufacturer||'')} ${escHtml(dev.model_name||'')}</span>
            ${statusBadge(dev.online)}
            ${dev.data_model ? `<span class="badge bg-secondary bg-opacity-10 text-secondary border" style="font-size:.65rem">${escHtml(dev.data_model)}</span>` : ''}
          </div>
          <div class="dev-header-meta">
            ${field('Serial', dev.serial)}
            ${field('Firmware', dev.sw_version)}
            ${field('WAN IP', dev.wan_ip)}
            ${field('WAN TR069', dev.ip_address)}
            ${field('Uptime', uptimeStr)}
            ${field('CPU', cpuStr)}
            ${field('RAM', ramStr)}
          </div>
        </div>
      </div>
    </div>`;
}

function renderInfoTab(dev) {
  const row = (label, val) =>
    `<tr><td class="text-muted small fw-semibold" style="width:180px">${label}</td><td>${val||''}</td></tr>`;

  // Build base HTML with reorganized layout: Identification + Versions + Tags + System, then Optical section below
  let baseHtml = `
    <div class="row g-3">
      <div class="col-md-6">
        <div class="card border-0 shadow-sm">
          <div class="card-header bg-white fw-semibold small">Identification</div>
          <div class="card-body p-0">
            <table class="table table-sm mb-0">
              ${row('Serial', escHtml(dev.serial))}
              ${row('OUI', escHtml(dev.oui))}
              ${row('Manufacturer', escHtml(dev.manufacturer))}
              ${row('Model', escHtml(dev.model_name || dev.product_class))}
              ${row('Product Class', escHtml(dev.product_class))}
              ${row('Data Model', escHtml(dev.data_model))}
            </table>
          </div>
        </div>
      </div>
      <div class="col-md-6">
        <div class="card border-0 shadow-sm">
          <div class="card-header bg-white fw-semibold small">Versions</div>
          <div class="card-body p-0">
            <table class="table table-sm mb-0">
              ${row('Firmware', escHtml(dev.sw_version))}
              ${row('Hardware', escHtml(dev.hw_version))}
              ${row('Bootloader', escHtml(dev.bl_version))}
            </table>
          </div>
        </div>
      </div>
      <div class="col-md-6">
        <div class="card border-0 shadow-sm">
          <div class="card-header bg-white fw-semibold small d-flex justify-content-between align-items-center">
            Tags
            <button class="btn btn-sm btn-outline-primary py-0 px-2" onclick="openTagsModal('${escHtml(dev.serial)}', ${JSON.stringify(dev.tags||[])})">
              <i class="bi bi-pencil"></i>
            </button>
          </div>
          <div class="card-body" id="dev-tags-area">
            ${tagBadges(dev.tags)}
          </div>
        </div>
      </div>
      <div class="col-md-6">
        <div class="card border-0 shadow-sm">
          <div class="card-header bg-white fw-semibold small">System</div>
          <div class="card-body p-0">
            <table class="table table-sm mb-0">
              ${row('Uptime', dev.uptime_seconds ? fmtUptime(dev.uptime_seconds) : null)}
              ${row('CPU Usage', dev.cpu_usage != null ? dev.cpu_usage + '%' : null)}
              ${row('RAM (Used/Total)', dev.ram_total ? fmtRam(dev.ram_total, dev.ram_free) : null)}
              ${row('URL ACS', escHtml(dev.acs_url))}
              ${row('WAN TR069 IP', escHtml(dev.ip_address))}
              <tr>
                <td class="text-muted small fw-semibold" style="width:180px">WAN IP</td>
                <td>${dev.wans && dev.wans.length > 0 
                      ? dev.wans.map(w => w.ip_address ? `${escHtml(w.ip_address)} <span class="text-muted">(${escHtml(w.connection_type || 'Unknown')})</span>` : '').filter(Boolean).join('<br>')
                      : escHtml(dev.wan_ip || '')}
                </td>
              </tr>
              ${row('Last Inform', fmtDate(dev.last_inform))}
              ${row('Created at', fmtDate(dev.created_at))}
            </table>
          </div>
        </div>
      </div>
    </div>
    
    <!-- Optical Information Section (loaded separately) -->
    <div id="optical-section" class="row g-3 mt-2"></div>`;

  document.getElementById('tab-info').innerHTML = baseHtml;

  // Fetch parameters and extract optical info if available
  API.get(`/devices/${encodeURIComponent(dev.serial)}/parameters`)
    .then(params => {
      const opticalInfo = extractOpticalInfo(params);
      if (opticalInfo) {
        const opticalRow = (label, val) => 
          `<tr><td class="text-muted small fw-semibold" style="width:180px">${label}</td><td>${val ? escHtml(String(val)) : '-'}</td></tr>`;
        
        const formatTemp = (val) => val ? Math.round(val / 100) + ' °C' : '-';
        const formatVoltage = (val) => val ? (val / 1000).toFixed(3) + ' mV' : '-';
        const formatCurrent = (val) => val ? (val / 100).toFixed(2) + ' mA' : '-';
        const formatTXPower = (val) => val ? (parseInt(val) / 10000).toFixed(2) + ' dBm' : '-';
        
        const opticalHtml = `
          <div class="col-md-6">
            <div class="card border-0 shadow-sm">
              <div class="card-header bg-white fw-semibold small"><i class="bi bi-lightbulb me-1 text-warning"></i>XPON Status</div>
              <div class="card-body p-0">
                <table class="table table-sm mb-0">
                  ${opticalInfo.PonType ? opticalRow('PON Type', opticalInfo.PonType) : ''}
                  ${opticalInfo.XPONStatus ? opticalRow('XPON Status', opticalInfo.XPONStatus) : ''}
                  ${opticalInfo.Status ? opticalRow('Status Code', opticalInfo.Status) : ''}
                  ${opticalInfo.OpticalSignalLevel ? opticalRow('Signal Level', opticalInfo.OpticalSignalLevel) : ''}
                </table>
              </div>
            </div>
          </div>`;
        
        // Optical metrics card
        const opticalMetricsHtml = `
          <div class="col-md-6">
            <div class="card border-0 shadow-sm">
              <div class="card-header bg-white fw-semibold small">Optical Metrics</div>
              <div class="card-body p-0">
                <table class="table table-sm mb-0">
                  ${opticalInfo.Temperature ? opticalRow('Temperature', formatTemp(opticalInfo.Temperature)) : ''}
                  ${opticalInfo.SupplyVoltage ? opticalRow('Supply Voltage', formatVoltage(opticalInfo.SupplyVoltage)) : ''}
                  ${opticalInfo.BiasCurrent ? opticalRow('Bias Current', formatCurrent(opticalInfo.BiasCurrent)) : ''}
                  ${opticalInfo.TXPower ? opticalRow('TX Power', formatTXPower(opticalInfo.TXPower)) : ''}
                  ${opticalInfo.RXPower ? opticalRow('RX Power', opticalInfo.RXPower + ' dBm') : ''}
                  ${opticalInfo.TransmitOpticalLevel ? opticalRow('TX Optical Level', opticalInfo.TransmitOpticalLevel) : ''}
                  ${opticalInfo.FecDownstreamEnabled ? opticalRow('FEC Downstream', opticalInfo.FecDownstreamEnabled === '1' || opticalInfo.FecDownstreamEnabled === 1 ? 'Enabled' : 'Disabled') : ''}
                  ${opticalInfo.FecUpstreamEnabled ? opticalRow('FEC Upstream', opticalInfo.FecUpstreamEnabled === '1' || opticalInfo.FecUpstreamEnabled === 1 ? 'Enabled' : 'Disabled') : ''}
                </table>
              </div>
            </div>
          </div>`;
        
        // Traffic stats card
        const trafficHtml = `
          <div class="col-md-6">
            <div class="card border-0 shadow-sm">
              <div class="card-header bg-white fw-semibold small">Optical Traffic</div>
              <div class="card-body p-0">
                <table class="table table-sm mb-0">
                  ${opticalInfo.BytesReceived ? opticalRow('Bytes RX', fmtBytes(opticalInfo.BytesReceived)) : ''}
                  ${opticalInfo.BytesSent ? opticalRow('Bytes TX', fmtBytes(opticalInfo.BytesSent)) : ''}
                  ${opticalInfo.PacketsReceived ? opticalRow('Packets RX', opticalInfo.PacketsReceived) : ''}
                  ${opticalInfo.PacketsSent ? opticalRow('Packets TX', opticalInfo.PacketsSent) : ''}
                </table>
              </div>
            </div>
          </div>`;
        
        // OMCI stats card
        const omciHtml = opticalInfo.OMCIPacketsReceived || opticalInfo.OMCIPacketsSent ? `
          <div class="col-md-6">
            <div class="card border-0 shadow-sm">
              <div class="card-header bg-white fw-semibold small">OMCI Stats</div>
              <div class="card-body p-0">
                <table class="table table-sm mb-0">
                  ${opticalInfo.OMCIPacketsReceived ? opticalRow('OMCI RX', opticalInfo.OMCIPacketsReceived) : ''}
                  ${opticalInfo.OMCIPacketsSent ? opticalRow('OMCI TX', opticalInfo.OMCIPacketsSent) : ''}
                </table>
              </div>
            </div>
          </div>` : '';
        
        document.getElementById('optical-section').innerHTML = opticalHtml + opticalMetricsHtml + trafficHtml + omciHtml;
      }
    })
    .catch(e => {
      // Silently fail if parameters endpoint not available
    });
}


function renderNetworkTab(dev) {
  const row = (label, val) =>
    `<tr><td class="text-muted small fw-semibold" style="width:180px">${label}</td><td>${escHtml(String(val||''))}</td></tr>`;

  let wanCards = '';
  if (dev.wans && dev.wans.length > 0) {
    wanCards = dev.wans.map(w => `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small"><i class="bi bi-globe me-1 text-primary"></i>WAN (${escHtml(w.connection_type || 'Unknown')})</div>
        <div class="card-body p-0">
          <table class="table table-sm mb-0">
            ${row('Status', w.link_status)}
            ${row('Service', w.service_type)}
            ${row('Type', w.connection_type)}
            ${row('IP', w.ip_address)}
            ${row('Subnet', w.subnet_mask)}
            ${row('Gateway', w.gateway)}
            ${row('DNS 1', w.dns1)}
            ${row('DNS 2', w.dns2)}
            ${row('MAC', w.mac_address)}
            ${row('MTU', w.mtu || null)}
            ${row('Uptime WAN', w.uptime_seconds ? fmtUptime(w.uptime_seconds) : null)}
            ${w.pppoe_username ? row('PPPoE Username', w.pppoe_username) : ''}
          </table>
        </div>
        ${w.bytes_sent || w.bytes_received ? `
        <div class="card-footer bg-white border-top">
          <small class="text-muted">Traffic  Sent: ${fmtBytes(w.bytes_sent)} · Received: ${fmtBytes(w.bytes_received)}</small>
        </div>` : ''}
      </div>
    </div>`).join('');
  } else if (dev.wan) {
    // fallback for older db data
    wanCards = `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small"><i class="bi bi-globe me-1 text-primary"></i>WAN</div>
        <div class="card-body p-0">
          <table class="table table-sm mb-0">
            ${row('Status', dev.wan.link_status)}
            ${row('Type', dev.wan.connection_type)}
            ${row('IP', dev.wan.ip_address)}
            ${row('Subnet', dev.wan.subnet_mask)}
            ${row('Gateway', dev.wan.gateway)}
            ${row('DNS 1', dev.wan.dns1)}
            ${row('DNS 2', dev.wan.dns2)}
            ${row('MAC', dev.wan.mac_address)}
            ${row('MTU', dev.wan.mtu || null)}
            ${row('Uptime WAN', dev.wan.uptime_seconds ? fmtUptime(dev.wan.uptime_seconds) : null)}
            ${dev.wan.pppoe_username ? row('PPPoE Username', dev.wan.pppoe_username) : ''}
          </table>
        </div>
      </div>
    </div>`;
  } else {
    wanCards = `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small"><i class="bi bi-globe me-1 text-primary"></i>WAN</div>
        <div class="empty-state"><i class="bi bi-info-circle"></i>Run the task <strong>CPE Statistics</strong> to populate WAN data.</div>
      </div>
    </div>`;
  }

  const lanCard = dev.lan ? `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small"><i class="bi bi-house me-1 text-success"></i>LAN</div>
        <div class="card-body p-0">
          <table class="table table-sm mb-0">
            ${row('Router IP', dev.lan.ip_address)}
            ${row('Subnet', dev.lan.subnet_mask)}
            ${row('DHCP', dev.lan.dhcp_enabled ? 'Enabled' : 'Disabled')}
            ${dev.lan.dhcp_enabled ? row('DHCP Range', `${dev.lan.dhcp_start||''} – ${dev.lan.dhcp_end||''}`) : ''}
            ${row('DNS', dev.lan.dns_servers)}
            ${row('Active Leases', dev.lan.active_leases != null ? dev.lan.active_leases : null)}
          </table>
        </div>
      </div>
    </div>` : `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small"><i class="bi bi-house me-1 text-success"></i>LAN</div>
        <div class="empty-state"><i class="bi bi-info-circle"></i>No LAN data collected.</div>
      </div>
    </div>`;

  const wifiCard = (wifi, label, icon) => wifi ? `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small d-flex justify-content-between">
          <span><i class="bi bi-wifi me-1 ${icon}"></i>${label}</span>
          <span class="badge ${wifi.enabled ? 'bg-success' : 'bg-secondary'}">${wifi.enabled ? 'Active' : 'Inactive'}</span>
        </div>
        <div class="card-body p-0">
          <table class="table table-sm mb-0">
            ${row('SSID', wifi.ssid)}
            ${row('BSSID', wifi.bssid)}
            ${row('Channel', wifi.channel || null)}
            ${row('Bandwidth', wifi.channel_width)}
            ${row('Standard', wifi.standard)}
            ${row('Security', wifi.security_mode)}
            ${row('TX Power', wifi.tx_power ? wifi.tx_power + '%' : null)}
            ${row('Clients', wifi.connected_clients != null ? wifi.connected_clients : null)}
          </table>
        </div>
        ${wifi.bytes_sent || wifi.bytes_received ? `
        <div class="card-footer bg-white border-top">
          <small class="text-muted">Traffic  Sent: ${fmtBytes(wifi.bytes_sent)} · Received: ${fmtBytes(wifi.bytes_received)}</small>
        </div>` : ''}
      </div>
    </div>` : '';

  const wifi24 = wifiCard(dev.wifi_24, 'Wi-Fi 2.4 GHz', 'text-warning');
  const wifi5  = wifiCard(dev.wifi_5,  'Wi-Fi 5 GHz',   'text-primary');

  const noWifi = (!dev.wifi_24 && !dev.wifi_5) ? `
    <div>
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white fw-semibold small"><i class="bi bi-wifi me-1 text-warning"></i>Wi-Fi</div>
        <div class="empty-state"><i class="bi bi-info-circle"></i>No WiFi data collected. Run the task <strong>CPE Statistics</strong>.</div>
      </div>
    </div>` : '';

  document.getElementById('tab-network').innerHTML = `
    <div class="row g-3">
      <div class="col-md-6 d-flex flex-column gap-3">
        ${wanCards}
        ${lanCard}
      </div>
      <div class="col-md-6 d-flex flex-column gap-3">
        ${wifi24}
        ${wifi5}
        ${noWifi}
      </div>
    </div>`;
}

// ─────────────────────────────────────────────────────────────
//  Tab: Connected Hosts
// ─────────────────────────────────────────────────────────────
async function loadHosts() {
  const el = document.getElementById('tab-hosts');
  if (el.dataset.loaded) return;
  try {
    const dev = await API.get(`/devices/${encodeURIComponent(S.currentSerial)}`);
    const hosts = dev.connected_hosts || [];

    if (!hosts.length) {
      el.innerHTML = `
        <div class="empty-state">
          <i class="bi bi-people"></i>
          No connected hosts recorded.
          <div class="mt-2 text-muted small">Run the task <strong>Connected Devices</strong> to update.</div>
        </div>`;
      return;
    }

    const rows = hosts.map(h => `
      <tr>
        <td class="fw-semibold small font-monospace">${escHtml(h.mac||'')}</td>
        <td>${escHtml(h.ip||'')}</td>
        <td>${escHtml(h.hostname||'')}</td>
        <td>
          <span class="badge ${h.interface && h.interface.includes('Wi-Fi') ? 'bg-primary' : 'bg-success'}">${escHtml(h.interface || 'Unknown')}</span>
          ${h.rssi != null ? `<small class="text-muted ms-1" title="Signal Strength">${h.rssi} dBm</small>` : ''}
        </td>
        <td>${h.active
          ? '<span class="badge badge-online"><span class="status-dot dot-online"></span>Active</span>'
          : '<span class="badge badge-offline">Inactive</span>'}</td>
        <td class="text-muted small">${h.lease_time > 0 ? fmtUptime(h.lease_time) : ''}</td>
      </tr>`).join('');

    el.innerHTML = `
      <div class="d-flex justify-content-between align-items-center mb-2">
        <span class="fw-semibold small">Connected Hosts (${hosts.length})</span>
        <button class="btn btn-sm btn-outline-secondary" onclick="delete document.getElementById('tab-hosts').dataset.loaded; loadHosts()">
          <i class="bi bi-arrow-clockwise"></i>
        </button>
      </div>
      <div class="card border-0 shadow-sm">
        <div class="card-body p-0">
          <table class="table table-sm table-hover align-middle mb-0">
            <thead><tr>
              <th>MAC</th><th>IP</th><th>Hostname</th><th>Interface</th><th>Status</th><th>Lease</th>
            </tr></thead>
            <tbody>${rows}</tbody>
          </table>
        </div>
      </div>`;
    el.dataset.loaded = '1';
  } catch (e) {
    el.innerHTML = `<div class="alert alert-danger">${e.message}</div>`;
  }
}

// ─────────────────────────────────────────────────────────────
//  Tab: Parameters
// ─────────────────────────────────────────────────────────────
async function loadParams() {
  const el = document.getElementById('tab-params');
  if (el.dataset.loaded) return;
  try {
    const params = await API.get(`/devices/${encodeURIComponent(S.currentSerial)}/parameters`);
    const entries = Object.entries(params || {});

    el.innerHTML = `
      <div class="card border-0 shadow-sm">
        <div class="card-header bg-white d-flex justify-content-between align-items-center">
          <span class="fw-semibold small">Parameters TR-069 (${entries.length})</span>
          <input class="form-control form-control-sm w-auto" id="param-search"
                 placeholder="Filter…" oninput="filterParams()" style="max-width:240px">
        </div>
        <div class="card-body p-0">
          <div style="max-height:520px;overflow-y:auto">
            <table class="table table-sm table-hover mb-0" id="params-table">
              <thead class="sticky-top bg-white">
                <tr><th>Parameter</th><th>Value</th></tr>
              </thead>
              <tbody id="params-tbody">
                ${entries.map(([k,v]) =>
                  `<tr data-param="${escHtml(k.toLowerCase())}">
                     <td class="param-name">${escHtml(k)}</td>
                     <td class="param-value">${escHtml(v)}</td>
                   </tr>`).join('')}
              </tbody>
            </table>
            ${entries.length === 0 ? '<div class="empty-state"><i class="bi bi-inbox"></i>No parameters</div>' : ''}
          </div>
        </div>
      </div>`;
    el.dataset.loaded = '1';
  } catch (e) {
    el.innerHTML = `<div class="alert alert-danger">${e.message}</div>`;
  }
}

function filterParams() {
  const q = document.getElementById('param-search').value.toLowerCase();
  document.querySelectorAll('#params-tbody tr').forEach(tr => {
    tr.style.display = tr.dataset.param.includes(q) ? '' : 'none';
  });
}

// ─────────────────────────────────────────────────────────────
//  Tab: Tasks
// ─────────────────────────────────────────────────────────────
async function loadTasks(page = 1) {
  S.taskPage = page;
  const el = document.getElementById('tab-tasks');
  try {
    const res = await API.get(`/devices/${encodeURIComponent(S.currentSerial)}/tasks?page=${page}&limit=15`);
    const tasks = res.data || [];
    const total = res.total || 0;
    const totalPages = Math.ceil(total / 15) || 1;

    const rows = tasks.map(t => `
      <tr>
        <td class="small text-muted">${escHtml(t.id.substring(0,8))}…</td>
        <td><span class="badge bg-secondary">${escHtml(taskTypeLabel(t.type))}</span></td>
        <td>${taskStatusBadge(t.status)}</td>
        <td class="small text-muted">${fmtDate(t.created_at)}</td>
        <td class="small text-muted">${fmtDate(t.completed_at)}</td>
        <td class="small text-danger">${escHtml(t.error||'')}</td>
        <td>
          ${t.status === 'pending' || t.status === 'executing'
            ? `<button class="btn btn-sm btn-outline-danger py-0" onclick="cancelTask('${escHtml(t.id)}')">
                 <i class="bi bi-x"></i>
               </button>`
            : ''}
        </td>
      </tr>
      ${t.result ? `<tr class="task-result-row"><td colspan="7" class="p-0 ps-3 pb-2">${renderTaskResult(t)}</td></tr>` : ''}`).join('');

    const pagi = totalPages > 1 ? `
      <div class="d-flex justify-content-between px-3 py-2 border-top">
        <small class="text-muted">Total: ${total}</small>
        <nav><ul class="pagination pagination-sm mb-0">
          ${Array.from({length:totalPages},(_,i)=>i+1).map(p =>
            `<li class="page-item ${p===page?'active':''}">
               <a class="page-link" href="#" onclick="loadTasks(${p});return false">${p}</a>
             </li>`).join('')}
        </ul></nav>
      </div>` : '';

    el.innerHTML = `
      <div class="d-flex justify-content-between align-items-center mb-2">
        <span class="fw-semibold small">Task History (${total})</span>
        <button class="btn btn-primary btn-sm" onclick="openTaskModal('${escHtml(S.currentSerial)}')">
          <i class="bi bi-plus-circle me-1"></i>New Task
        </button>
      </div>
      <div class="card border-0 shadow-sm">
        <div class="card-body p-0">
          ${rows.length
            ? `<table class="table table-sm table-hover align-middle mb-0">
                 <thead><tr>
                   <th>ID</th><th>Type</th><th>Status</th>
                   <th>Created</th><th>Completed</th><th>Error</th><th></th>
                 </tr></thead>
                 <tbody>${rows}</tbody>
               </table>${pagi}`
            : `<div class="empty-state"><i class="bi bi-list-check"></i>No task created
                 <div class="mt-2">
                   <button class="btn btn-primary btn-sm" onclick="openTaskModal('${escHtml(S.currentSerial)}')">
                     <i class="bi bi-plus-circle me-1"></i>Create first task
                   </button>
                 </div>
               </div>`}
        </div>
      </div>`;

    // Auto-refresh if any pending/executing tasks
    const hasPending = tasks.some(t => t.status === 'pending' || t.status === 'executing');
    if (hasPending && !S.pollTimer) {
      S.pollTimer = setInterval(() => {
        if (document.querySelector('#tab-tasks.active') !== null ||
            document.querySelector('#tab-tasks.show') !== null) {
          loadTasks(S.taskPage);
        }
      }, 5000);
    }
  } catch (e) {
    el.innerHTML = `<div class="alert alert-danger">${e.message}</div>`;
  }
}

function taskTypeLabel(type) {
  const labels = {
    wifi: 'Wi-Fi', wan: 'WAN', lan: 'LAN', reboot: 'Reboot',
    factory_reset: 'Factory Reset', set_params: 'Set Params', firmware: 'Firmware',
    web_admin: 'Web Password',
    diagnostic: 'Diagnostic', ping_test: 'Ping', traceroute: 'Traceroute',
    speed_test: 'Speed Test', connected_devices: 'Hosts', cpe_stats: 'CPE Stats',
    port_forwarding: 'Port Fwd',
  };
  return labels[type] || type;
}

function renderTaskResult(t) {
  if (!t.result) return '';
  const r = t.result;

  switch (t.type) {
    case 'ping_test': {
      const loss = r.packet_loss_pct != null ? r.packet_loss_pct.toFixed(1) + '%' : '';
      const lossClass = (r.packet_loss_pct || 0) > 0 ? 'text-danger' : 'text-success';
      return `<div class="task-result">
        <i class="bi bi-reception-4 me-1 text-primary"></i>
        <strong>${escHtml(r.host||'')}</strong> 
        Enviados: ${r.packets_sent||0} · Recebidos: ${r.packets_received||0} ·
        Perda: <span class="${lossClass}">${loss}</span> ·
        RTT: min ${r.min_rtt_ms||0}ms / avg ${r.avg_rtt_ms||0}ms / max ${r.max_rtt_ms||0}ms
      </div>`;
    }

    case 'traceroute': {
      const hops = (r.hops || []).map(h =>
        `<span class="badge bg-light text-dark border me-1">${h.hop_number}. ${escHtml(h.host||'*')} (${h.rtt_ms||0}ms)</span>`
      ).join('');
      return `<div class="task-result">
        <i class="bi bi-map me-1 text-info"></i>
        <strong>${escHtml(r.host||'')}</strong>  ${r.hop_count||0} hops
        <div class="mt-1">${hops || '<span class="text-muted small">no hops</span>'}</div>
      </div>`;
    }

    case 'speed_test': {
      const speed = r.download_speed_mbps != null ? r.download_speed_mbps.toFixed(2) : '';
      return `<div class="task-result">
        <i class="bi bi-speedometer me-1 text-warning"></i>
        Download: <strong>${speed} Mbps</strong> ·
        Duration: ${r.download_duration_ms||0}ms ·
        Bytes: ${fmtBytes(r.download_bytes_total)}
      </div>`;
    }

    case 'cpe_stats': {
      return `<div class="task-result">
        <i class="bi bi-bar-chart me-1 text-success"></i>
        Uptime: <strong>${fmtUptime(r.uptime_seconds)}</strong> ·
        RAM: ${fmtRam(r.ram_total_kb, r.ram_total_kb - r.ram_free_kb > 0 ? r.ram_total_kb - r.ram_free_kb : 0)} ·
        WAN ↑${fmtBytes(r.wan_bytes_sent)} ↓${fmtBytes(r.wan_bytes_recv)}
      </div>`;
    }

    case 'connected_devices': {
      const hosts = Array.isArray(r) ? r : [];
      if (!hosts.length) return `<div class="task-result text-muted"><i class="bi bi-people me-1"></i>No connected host</div>`;
      const items = hosts.map(h =>
        `<span class="badge bg-light text-dark border me-1">${escHtml(h.hostname||h.ip||h.mac||'?')} (${escHtml(h.interface||'')})</span>`
      ).join('');
      return `<div class="task-result">
        <i class="bi bi-people me-1 text-primary"></i>
        <strong>${hosts.length}</strong> hosts 
        <div class="mt-1">${items}</div>
      </div>`;
    }

    case 'port_forwarding': {
      const rules = Array.isArray(r) ? r : [];
      if (!rules.length) return `<div class="task-result text-muted"><i class="bi bi-arrows-angle-expand me-1"></i>No forwarding rules</div>`;
      const items = rules.map(rule =>
        `<div class="small">${rule.enabled ? '✓' : '✗'} ${escHtml(rule.protocol||'TCP')} :${rule.external_port} → ${escHtml(rule.internal_ip||'')}:${rule.internal_port} ${rule.description ? `<span class="text-muted">(${escHtml(rule.description)})</span>` : ''}</div>`
      ).join('');
      return `<div class="task-result">
        <i class="bi bi-arrow-left-right me-1 text-info"></i>
        <strong>${rules.length}</strong> rule(s):<div class="mt-1">${items}</div>
      </div>`;
    }

    default:
      return '';
  }
}

async function cancelTask(taskId) {
  confirm('Cancel this task?', async () => {
    try {
      await API.del(`/tasks/${taskId}`);
      toast('Task cancelled.', 'warning');
      loadTasks(S.taskPage);
    } catch (e) { toast(e.message, 'danger'); }
  });
}

// ─────────────────────────────────────────────────────────────
//  Tags modal
// ─────────────────────────────────────────────────────────────
function openTagsModal(serial, tags) {
  document.getElementById('tags-input').value = (tags || []).join(', ');
  const btn = document.getElementById('tags-save-btn');
  btn.onclick = async () => {
    const newTags = document.getElementById('tags-input').value
      .split(',').map(t => t.trim()).filter(Boolean);
    try {
      const dev = await API.put(`/devices/${encodeURIComponent(serial)}`, { tags: newTags });
      S.tagsModal.hide();
      document.getElementById('dev-tags-area').innerHTML = tagBadges(dev.tags);
      toast('Tags atualizadas.');
    } catch (e) { toast(e.message, 'danger'); }
  };
  S.tagsModal.show();
}

// ─────────────────────────────────────────────────────────────
//  View: Health
// ─────────────────────────────────────────────────────────────
async function viewHealth() {
  const el = document.getElementById('view-content');
  el.innerHTML = `
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h5 class="fw-bold mb-0"><i class="bi bi-heart-pulse me-2 text-danger"></i>System Health</h5>
      <button class="btn btn-sm btn-outline-secondary" onclick="viewHealth()">
        <i class="bi bi-arrow-clockwise"></i> Refresh
      </button>
    </div>
    <div id="health-content" class="text-center py-5">
      <div class="spinner-border text-primary"></div>
    </div>`;

  try {
    const h = await API.health();
    const item = (name, status, icon) => `
      <div class="col-md-4">
        <div class="card border-0 shadow-sm text-center">
          <div class="card-body py-4">
            <i class="bi bi-${icon} fs-1 ${status==='OK'?'text-success':'text-danger'}"></i>
            <h5 class="mt-2">${name}</h5>
            <span class="badge ${status==='OK'?'bg-success':'bg-danger'} fs-6">${status === 'OK' ? 'HEALTHY' : 'DEGRADED'}</span>
          </div>
        </div>
      </div>`;

    document.getElementById('health-content').innerHTML = `
      <div class="row g-3 justify-content-center">
        ${item('MongoDB', h.mongodb, 'database')}
        ${item('Redis', h.redis, 'lightning-charge')}
        ${item('API', h.status, 'server')}
      </div>
      <p class="text-center text-muted small mt-3">
        Verificado em: ${new Date().toLocaleString('en-US')}
      </p>`;
  } catch (e) {
    document.getElementById('health-content').innerHTML =
      `<div class="alert alert-danger">${e.message}</div>`;
  }
}

// ─────────────────────────────────────────────────────────────
//  Task modal & forms
// ─────────────────────────────────────────────────────────────
let _taskSerial = '';

function openTaskModal(serial) {
  _taskSerial = serial;
  document.getElementById('task-type-select').value = 'wifi';
  renderTaskForm();
  S.taskModal.show();
}

function renderTaskForm() {
  const type = document.getElementById('task-type-select').value;
  const el = document.getElementById('task-form-body');
  el.innerHTML = TASK_FORMS[type] || '';
  // Init kv-container for parameters form
  if (type === 'parameters') addKvRow();
}

const TASK_FORMS = {
  wifi: `
    <div class="row g-3">
      <div class="col-sm-4">
        <label class="form-label">Banda</label>
        <select class="form-select" id="tf-band">
          <option value="2.4">2.4 GHz</option>
          <option value="5">5 GHz</option>
        </select>
      </div>
      <div class="col-sm-8">
        <label class="form-label">SSID</label>
        <input class="form-control" id="tf-ssid" placeholder="Network name">
      </div>
      <div class="col-sm-4">
        <label class="form-label">Security</label>
        <select class="form-select" id="tf-security" onchange="document.getElementById('tf-pass').disabled = (this.value === 'None')">
          <option value="WPA2-PSK">WPA2-PSK</option>
          <option value="WPA-WPA2-PSK">WPA/WPA2-PSK</option>
          <option value="None">Open (No Password)</option>
        </select>
      </div>
      <div class="col-sm-8">
        <label class="form-label">Password</label>
        <input class="form-control" id="tf-pass" type="password" placeholder="Minimum 8 characters">
      </div>
      <div class="col-sm-3">
        <label class="form-label">Channel</label>
        <input class="form-control" id="tf-channel" type="number" placeholder="0=auto" min="0" max="165">
      </div>
      <div class="col-sm-3 d-flex align-items-end gap-3 flex-wrap">
        <div class="form-check">
          <input class="form-check-input" type="checkbox" id="tf-enabled" checked>
          <label class="form-check-label">Enabled</label>
        </div>
        <div class="form-check">
          <input class="form-check-input" type="checkbox" id="tf-band-steering">
          <label class="form-check-label" title="Enable Smart Connect (Band Steering)">Smart Connect</label>
        </div>
      </div>
    </div>`,

  wan: `
    <div class="row g-3">
      <div class="col-sm-6">
        <label class="form-label">Connection Type</label>
        <select class="form-select" id="tf-wan-type" onchange="toggleWanFields()">
          <option value="pppoe">PPPoE</option>
          <option value="dhcp">DHCP</option>
          <option value="static">IP Fixo</option>
        </select>
      </div>
      <div class="col-sm-3">
        <label class="form-label">VLAN ID</label>
        <input class="form-control" id="tf-vlan" type="number" placeholder="0=no VLAN" min="0" max="4094">
      </div>
      <div class="col-sm-3">
        <label class="form-label">MTU</label>
        <input class="form-control" id="tf-mtu" type="number" placeholder="1500" value="1500" min="576" max="9000">
      </div>
      <div id="tf-pppoe-fields">
        <div class="row g-3">
          <div class="col-sm-6">
            <label class="form-label">PPPoE Username</label>
            <input class="form-control" id="tf-wan-user" placeholder="usuario@isp.com.br">
          </div>
          <div class="col-sm-6">
            <label class="form-label">PPPoE Password</label>
            <input class="form-control" id="tf-wan-pass" type="password">
          </div>
        </div>
      </div>
      <div id="tf-static-fields" style="display:none">
        <div class="row g-3">
          <div class="col-sm-6"><label class="form-label">IP</label>
            <input class="form-control" id="tf-wan-ip" placeholder="200.100.50.1"></div>
          <div class="col-sm-6"><label class="form-label">Subnet</label>
            <input class="form-control" id="tf-wan-mask" placeholder="255.255.255.0"></div>
          <div class="col-sm-6"><label class="form-label">Gateway</label>
            <input class="form-control" id="tf-wan-gw" placeholder="200.100.50.254"></div>
          <div class="col-sm-3"><label class="form-label">DNS 1</label>
            <input class="form-control" id="tf-dns1" placeholder="8.8.8.8"></div>
          <div class="col-sm-3"><label class="form-label">DNS 2</label>
            <input class="form-control" id="tf-dns2" placeholder="8.8.4.4"></div>
        </div>
      </div>
    </div>`,

  lan: `
    <div class="row g-3">
      <div class="col-sm-6">
        <label class="form-label">Router IP (LAN)</label>
        <input class="form-control" id="tf-lan-ip" placeholder="192.168.1.1">
      </div>
      <div class="col-sm-6">
        <label class="form-label">Subnet</label>
        <input class="form-control" id="tf-lan-mask" placeholder="255.255.255.0">
      </div>
      <div class="col-12">
        <div class="form-check">
          <input class="form-check-input" type="checkbox" id="tf-dhcp-en" checked onchange="toggleDhcpFields()">
          <label class="form-check-label fw-semibold">DHCP Server Enabled</label>
        </div>
      </div>
      <div id="tf-dhcp-fields">
        <div class="row g-3">
          <div class="col-sm-4"><label class="form-label">IP inicial</label>
            <input class="form-control" id="tf-dhcp-start" placeholder="192.168.1.100"></div>
          <div class="col-sm-4"><label class="form-label">IP final</label>
            <input class="form-control" id="tf-dhcp-end" placeholder="192.168.1.200"></div>
          <div class="col-sm-4"><label class="form-label">DNS</label>
            <input class="form-control" id="tf-dhcp-dns" placeholder="8.8.8.8"></div>
          <div class="col-sm-4"><label class="form-label">Lease Time (s)</label>
            <input class="form-control" id="tf-lease" type="number" placeholder="86400" value="86400"></div>
        </div>
      </div>
    </div>`,

  'web-admin': `
    <div class="row g-3">
      <div class="col-12">
        <div class="alert alert-warning py-2 small">
          <i class="bi bi-info-circle me-1"></i>
          Supported only on <strong>TR-181</strong> devices. For TR-098, use
          <em>Set Parameters</em> with the path specific to the manufacturer.
        </div>
      </div>
      <div class="col-sm-6">
        <label class="form-label">New Password</label>
        <div class="input-group">
          <span class="input-group-text"><i class="bi bi-key"></i></span>
          <input class="form-control" id="tf-webadmin-pass" type="password"
                 placeholder="New web interface password" autocomplete="new-password">
        </div>
      </div>
      <div class="col-sm-6">
        <label class="form-label">Confirm Password</label>
        <div class="input-group">
          <span class="input-group-text"><i class="bi bi-key-fill"></i></span>
          <input class="form-control" id="tf-webadmin-pass2" type="password"
                 placeholder="Repeat password" autocomplete="new-password">
        </div>
      </div>
    </div>`,

  reboot: `
    <div class="alert alert-warning">
      <i class="bi bi-exclamation-triangle me-2"></i>
      The device will reboot on next Inform. Active connections will be interrupted.
    </div>`,

  'factory-reset': `
    <div class="alert alert-danger">
      <i class="bi bi-exclamation-octagon me-2"></i>
      <strong>Warning!</strong> All device settings will be erased and it
      will return to factory settings.
    </div>`,

  parameters: `
    <div>
      <label class="form-label fw-semibold">Parameters TR-069</label>
      <div id="kv-container"></div>
      <button type="button" class="btn btn-sm btn-outline-secondary mt-2" onclick="addKvRow()">
        <i class="bi bi-plus"></i> Add parameter
      </button>
    </div>`,

  firmware: `
    <div class="row g-3">
      <div class="col-12">
        <label class="form-label">URL do Firmware <span class="text-danger">*</span></label>
        <input class="form-control" id="tf-fw-url" placeholder="http://files.isp.com.br/firmware-v2.bin">
      </div>
      <div class="col-sm-6">
        <label class="form-label">Version (informational)</label>
        <input class="form-control" id="tf-fw-version" placeholder="2.0.1">
      </div>
      <div class="col-sm-6">
        <label class="form-label">File Type</label>
        <select class="form-select" id="tf-fw-type">
          <option value="1 Firmware Upgrade Image">Firmware</option>
          <option value="3 Vendor Configuration File">Configuration</option>
        </select>
      </div>
      <div class="col-sm-6">
        <label class="form-label">User (HTTP server)</label>
        <input class="form-control" id="tf-fw-user" placeholder="Opcional">
      </div>
      <div class="col-sm-6">
        <label class="form-label">Password</label>
        <input class="form-control" id="tf-fw-pass" type="password">
      </div>
    </div>`,

  ping: `
    <div class="row g-3">
      <div class="col-sm-6">
        <label class="form-label">Host / IP <span class="text-danger">*</span></label>
        <input class="form-control" id="tf-ping-host" placeholder="8.8.8.8">
      </div>
      <div class="col-sm-3">
        <label class="form-label">Count</label>
        <input class="form-control" id="tf-ping-count" type="number" value="4" min="1" max="100">
      </div>
      <div class="col-sm-3">
        <label class="form-label">Timeout (s)</label>
        <input class="form-control" id="tf-ping-timeout" type="number" value="5" min="1" max="60">
      </div>
      <div class="col-sm-3">
        <label class="form-label">Packet size (bytes)</label>
        <input class="form-control" id="tf-ping-size" type="number" value="64" min="1" max="65535">
      </div>
      <div class="col-sm-3">
        <label class="form-label">DSCP</label>
        <input class="form-control" id="tf-ping-dscp" type="number" value="0" min="0" max="63">
      </div>
    </div>`,

  traceroute: `
    <div class="row g-3">
      <div class="col-sm-6">
        <label class="form-label">Host / IP <span class="text-danger">*</span></label>
        <input class="form-control" id="tf-tr-host" placeholder="8.8.8.8">
      </div>
      <div class="col-sm-3">
        <label class="form-label">Max Hops</label>
        <input class="form-control" id="tf-tr-maxhops" type="number" value="30" min="1" max="64">
      </div>
      <div class="col-sm-3">
        <label class="form-label">Timeout (s)</label>
        <input class="form-control" id="tf-tr-timeout" type="number" value="5" min="1" max="60">
      </div>
    </div>`,

  'speed-test': `
    <div class="row g-3">
      <div class="col-12">
        <label class="form-label">Download URL <span class="text-danger">*</span></label>
        <input class="form-control" id="tf-st-url" placeholder="http://speedtest.tele2.net/10MB.zip">
        <div class="form-text">Use a file of at least 5–10 MB for accurate results.</div>
      </div>
    </div>`,

  'connected-devices': `
    <div class="alert alert-info">
      <i class="bi bi-people me-2"></i>
      Collects the list of connected hosts (LAN + Wi-Fi) from the device.
      The result will be available in the <strong>Hosts</strong> tab.
    </div>`,

  'cpe-stats': `
    <div class="alert alert-info">
      <i class="bi bi-bar-chart me-2"></i>
      Collects CPE statistics: uptime, RAM usage, WAN counters and network
      information. The data will be updated in the <strong>Information</strong> and <strong>Network</strong> tabs.
    </div>`,

  'port-forwarding': `
    <div class="row g-3">
      <div class="col-sm-4">
        <label class="form-label">Action</label>
        <select class="form-select" id="tf-pf-action" onchange="togglePortForwardingFields()">
          <option value="list">List rules</option>
          <option value="add">Add rule</option>
          <option value="remove">Remove rule</option>
        </select>
      </div>

      <div id="tf-pf-add-fields" style="display:none" class="col-12">
        <div class="row g-3">
          <div class="col-sm-3">
            <label class="form-label">Protocol</label>
            <select class="form-select" id="tf-pf-proto">
              <option value="TCP">TCP</option>
              <option value="UDP">UDP</option>
              <option value="TCP_UDP">TCP+UDP</option>
            </select>
          </div>
          <div class="col-sm-3">
            <label class="form-label">External Port <span class="text-danger">*</span></label>
            <input class="form-control" id="tf-pf-ext-port" type="number" placeholder="8080" min="1" max="65535">
          </div>
          <div class="col-sm-4">
            <label class="form-label">Internal IP <span class="text-danger">*</span></label>
            <input class="form-control" id="tf-pf-int-ip" placeholder="192.168.1.100">
          </div>
          <div class="col-sm-2">
            <label class="form-label">Internal Port <span class="text-danger">*</span></label>
            <input class="form-control" id="tf-pf-int-port" type="number" placeholder="80" min="1" max="65535">
          </div>
          <div class="col-sm-6">
            <label class="form-label">Description</label>
            <input class="form-control" id="tf-pf-desc" placeholder="Servidor Web">
          </div>
          <div class="col-sm-3 d-flex align-items-end">
            <div class="form-check">
              <input class="form-check-input" type="checkbox" id="tf-pf-enabled" checked>
              <label class="form-check-label">Enabled</label>
            </div>
          </div>
        </div>
      </div>

      <div id="tf-pf-remove-fields" style="display:none">
        <div class="col-sm-4">
          <label class="form-label">Instance number <span class="text-danger">*</span></label>
          <input class="form-control" id="tf-pf-instance" type="number" placeholder="1" min="1">
          <div class="form-text">Use the "List rules" action first to get the number.</div>
        </div>
      </div>
    </div>`,
};

function toggleWanFields() {
  const t = document.getElementById('tf-wan-type').value;
  document.getElementById('tf-pppoe-fields').style.display = t === 'pppoe' ? '' : 'none';
  document.getElementById('tf-static-fields').style.display = t === 'static' ? '' : 'none';
}

function toggleDhcpFields() {
  document.getElementById('tf-dhcp-fields').style.display =
    document.getElementById('tf-dhcp-en').checked ? '' : 'none';
}

function togglePortForwardingFields() {
  const action = document.getElementById('tf-pf-action').value;
  document.getElementById('tf-pf-add-fields').style.display    = action === 'add'    ? '' : 'none';
  document.getElementById('tf-pf-remove-fields').style.display = action === 'remove' ? '' : 'none';
}

function addKvRow(k = '', v = '') {
  const c = document.getElementById('kv-container');
  const row = document.createElement('div');
  row.className = 'kv-row';
  row.innerHTML = `
    <input class="form-control form-control-sm kv-key" placeholder="Device.X.Y" value="${escHtml(k)}">
    <input class="form-control form-control-sm kv-val" placeholder="value" value="${escHtml(v)}">
    <button type="button" class="btn btn-sm btn-outline-danger" onclick="this.parentElement.remove()">
      <i class="bi bi-x"></i>
    </button>`;
  c.appendChild(row);
}

async function submitTask() {
  const type = document.getElementById('task-type-select').value;
  let payload = {};

  try {
    switch (type) {
      case 'wifi': {
        const enabled = document.getElementById('tf-enabled').checked;
        const steering = document.getElementById('tf-band-steering').checked;
        const security = document.getElementById('tf-security').value;
        const password = document.getElementById('tf-pass').value;
        
        payload = {
          band:     document.getElementById('tf-band').value,
          ssid:     document.getElementById('tf-ssid').value,
          password: password,
          security: security,
          channel:  parseInt(document.getElementById('tf-channel').value) || 0,
          enabled,
          band_steering_enabled: steering,
        };
        if (!payload.ssid) throw new Error('SSID is required');
        if (security !== 'None' && password.length < 8) throw new Error('Password must be at least 8 characters');
        break;
      }
      case 'wan': {
        const wanType = document.getElementById('tf-wan-type').value;
        payload = {
          connection_type: wanType,
          vlan: parseInt(document.getElementById('tf-vlan').value) || 0,
          mtu:  parseInt(document.getElementById('tf-mtu').value) || 0,
        };
        if (wanType === 'pppoe') {
          payload.username = document.getElementById('tf-wan-user').value;
          payload.password = document.getElementById('tf-wan-pass').value;
        } else if (wanType === 'static') {
          payload.ip_address  = document.getElementById('tf-wan-ip').value;
          payload.subnet_mask = document.getElementById('tf-wan-mask').value;
          payload.gateway     = document.getElementById('tf-wan-gw').value;
          payload.dns1        = document.getElementById('tf-dns1').value;
          payload.dns2        = document.getElementById('tf-dns2').value;
        }
        break;
      }
      case 'lan': {
        payload = {
          dhcp_enabled: document.getElementById('tf-dhcp-en').checked,
          ip_address:   document.getElementById('tf-lan-ip').value,
          subnet_mask:  document.getElementById('tf-lan-mask').value,
        };
        if (payload.dhcp_enabled) {
          payload.dhcp_start = document.getElementById('tf-dhcp-start').value;
          payload.dhcp_end   = document.getElementById('tf-dhcp-end').value;
          payload.dns_server = document.getElementById('tf-dhcp-dns').value;
          payload.lease_time = parseInt(document.getElementById('tf-lease').value) || 86400;
        }
        break;
      }
      case 'web-admin': {
        const pass  = document.getElementById('tf-webadmin-pass').value;
        const pass2 = document.getElementById('tf-webadmin-pass2').value;
        if (!pass) throw new Error('Password cannot be empty');
        if (pass !== pass2) throw new Error('Passwords do not match');
        payload = { password: pass };
        break;
      }
      case 'reboot':
      case 'factory-reset':
      case 'connected-devices':
      case 'cpe-stats':
        payload = {};
        break;
      case 'parameters': {
        const params = {};
        document.querySelectorAll('.kv-row').forEach(row => {
          const k = row.querySelector('.kv-key').value.trim();
          const v = row.querySelector('.kv-val').value.trim();
          if (k) params[k] = v;
        });
        if (Object.keys(params).length === 0) throw new Error('Add at least one parameter');
        payload = { parameters: params };
        break;
      }
      case 'firmware': {
        const url = document.getElementById('tf-fw-url').value.trim();
        if (!url) throw new Error('URL is required');
        payload = {
          url,
          version:   document.getElementById('tf-fw-version').value,
          file_type: document.getElementById('tf-fw-type').value,
          username:  document.getElementById('tf-fw-user').value,
          password:  document.getElementById('tf-fw-pass').value,
        };
        break;
      }
      case 'ping': {
        const host = document.getElementById('tf-ping-host').value.trim();
        if (!host) throw new Error('Host is required');
        payload = {
          host,
          count:       parseInt(document.getElementById('tf-ping-count').value) || 4,
          timeout:     parseInt(document.getElementById('tf-ping-timeout').value) || 5,
          packet_size: parseInt(document.getElementById('tf-ping-size').value) || 64,
          dscp:        parseInt(document.getElementById('tf-ping-dscp').value) || 0,
        };
        break;
      }
      case 'traceroute': {
        const host = document.getElementById('tf-tr-host').value.trim();
        if (!host) throw new Error('Host is required');
        payload = {
          host,
          max_hops: parseInt(document.getElementById('tf-tr-maxhops').value) || 30,
          timeout:  parseInt(document.getElementById('tf-tr-timeout').value) || 5,
        };
        break;
      }
      case 'speed-test': {
        const url = document.getElementById('tf-st-url').value.trim();
        if (!url) throw new Error('Download URL is required');
        payload = { download_url: url };
        break;
      }
      case 'port-forwarding': {
        const action = document.getElementById('tf-pf-action').value;
        payload = { action };
        if (action === 'add') {
          const extPort = parseInt(document.getElementById('tf-pf-ext-port').value);
          const intPort = parseInt(document.getElementById('tf-pf-int-port').value);
          const intIP   = document.getElementById('tf-pf-int-ip').value.trim();
          if (!extPort || !intIP || !intPort) throw new Error('External port, internal IP and internal port are required');
          const enabled = document.getElementById('tf-pf-enabled').checked;
          payload = {
            action,
            protocol:      document.getElementById('tf-pf-proto').value,
            external_port: extPort,
            internal_ip:   intIP,
            internal_port: intPort,
            description:   document.getElementById('tf-pf-desc').value,
            enabled,
          };
        } else if (action === 'remove') {
          const instance = parseInt(document.getElementById('tf-pf-instance').value);
          if (!instance) throw new Error('Instance number is required');
          payload = { action, instance_number: instance };
        }
        break;
      }
    }

    const btn = document.getElementById('task-submit-btn');
    btn.disabled = true;
    try {
      let t = await API.post(`/devices/${encodeURIComponent(_taskSerial)}/tasks/${type}`, payload);
      
      // If the API wrapped the task in a response object (like CreateWifi does)
      if (t && t.task && t.task.id) {
        t = t.task;
      }
      
      S.taskModal.hide();
      toast(`Task ${taskTypeLabel(t.type)} created (${t.id.substring(0,8)}…)`);
      // Switch to tasks tab
      const tab = document.querySelector('[href="#tab-tasks"]');
      if (tab) { bootstrap.Tab.getOrCreateInstance(tab).show(); }
      loadTasks(1);
    } finally {
      btn.disabled = false;
    }
  } catch (e) {
    toast(e.message, 'danger');
  }
}

// ─────────────────────────────────────────────────────────────
//  Login form
// ─────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
  // Apply saved theme (sync icon with saved preference)
  initTheme();

  // Init Bootstrap modals
  S.taskModal    = new bootstrap.Modal(document.getElementById('taskModal'));
  S.confirmModal = new bootstrap.Modal(document.getElementById('confirmModal'));
  S.tagsModal    = new bootstrap.Modal(document.getElementById('tagsModal'));

  // Confirm button
  document.getElementById('confirm-ok-btn').onclick = () => {
    S.confirmModal.hide();
    if (S.pendingConfirm) { S.pendingConfirm(); S.pendingConfirm = null; }
  };

  // Login form
  document.getElementById('login-form').addEventListener('submit', async e => {
    e.preventDefault();
    const u = document.getElementById('login-user').value;
    const p = document.getElementById('login-pass').value;
    const errEl = document.getElementById('login-error');
    const spinner = document.getElementById('login-spinner');
    const btn = document.getElementById('login-btn');

    errEl.classList.add('d-none');
    spinner.classList.remove('d-none');
    btn.disabled = true;

    try {
      const res = await API.login(u, p);
      if (!res.token) throw new Error(res.error || 'Invalid credentials');
      S.token = res.token;
      localStorage.setItem('helixToken', res.token);
      setTopbarUser(u);
      document.getElementById('login-screen').style.display = 'none';
      document.getElementById('app-shell').style.display = 'flex';
      routeTo(window.location.hash || '/');
    } catch (err) {
      errEl.textContent = err.message;
      errEl.classList.remove('d-none');
    } finally {
      spinner.classList.add('d-none');
      btn.disabled = false;
    }
  });

  // Initial routing
  if (S.token) {
    document.getElementById('login-screen').style.display = 'none';
    document.getElementById('app-shell').style.display = 'flex';
    routeTo(window.location.hash || '/');
  }
});
