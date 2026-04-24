---
name: acs-web-ui
description: Designs and generates HTML/JavaScript web UI for ACS (Auto Configuration Server) dashboards and management consoles. Use when building device management pages, parameter viewers/editors, session monitors, firmware upgrade UIs, or any ACS front-end interface. Outputs clean, standalone HTML+JS without heavy frameworks.
---

# ACS Web UI Skill

This skill guides the design and generation of web UI for an ACS management console that visualizes and controls TR-069 managed devices (TR-098 and TR-181 data models).

---

## Design Principles

- **Standalone first**: Default to a single `.html` file with embedded `<style>` and `<script>`. No build tools required.
- **Progressive enhancement**: Core info visible without JS; JS adds interactivity.
- **ACS-appropriate UX**: Operators are technical. Prioritize density and clarity over decoration.
- **Data model aware**: UI must clearly label whether a device uses TR-098 or TR-181.

---

## Color & Visual Language

```css
:root {
  --bg:        #0f1117;
  --surface:   #1a1d27;
  --border:    #2a2d3a;
  --accent:    #4f8ef7;
  --success:   #22c55e;
  --warning:   #f59e0b;
  --danger:    #ef4444;
  --text:      #e2e8f0;
  --muted:     #64748b;
  --mono:      'JetBrains Mono', 'Fira Code', monospace;
}
```

Use dark theme by default — ACS consoles are NOC/operator tools, often used in low-light environments.

Status color convention:
- **Green** (`--success`): Online, connected, success
- **Yellow** (`--warning`): Offline <24h, pending task, warning
- **Red** (`--danger`): Error, disconnected, fault
- **Blue** (`--accent`): Selected, active, interactive element

---

## Page Templates

### 1. Device List Page

Shows all managed CPEs with key status at a glance.

```html
<!-- Key columns for device table -->
<table class="device-table">
  <thead>
    <tr>
      <th>Serial Number</th>
      <th>OUI</th>
      <th>Product Class</th>
      <th>Data Model</th>   <!-- TR-098 / TR-181 badge -->
      <th>Last Inform</th>
      <th>IP Address</th>
      <th>Firmware</th>
      <th>Status</th>
      <th>Actions</th>
    </tr>
  </thead>
</table>
```

Status badge pattern:
```html
<span class="badge badge--online">Online</span>
<span class="badge badge--offline">Offline</span>
<span class="badge badge--tr181">TR-181</span>
<span class="badge badge--tr098">TR-098</span>
```

```css
.badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.badge--online  { background: #14532d; color: var(--success); }
.badge--offline { background: #450a0a; color: var(--danger); }
.badge--tr181   { background: #1e3a5f; color: var(--accent); }
.badge--tr098   { background: #3b2a14; color: var(--warning); }
```

### 2. Device Detail Page

Tabbed layout with these tabs:
- **Overview** — key stats (IP, firmware, uptime, connection status)
- **Parameters** — searchable tree/table of all TR-098/TR-181 parameters
- **Tasks** — queued/completed ACS RPCs (GetParameterValues, SetParameterValues, Download, Reboot)
- **Sessions** — Inform history log
- **Firmware** — upgrade trigger UI

### 3. Parameter Viewer/Editor

```html
<div class="param-viewer">
  <div class="param-toolbar">
    <input type="text" id="paramSearch" placeholder="Search parameters..." />
    <button onclick="refreshParams()">↻ Refresh</button>
    <button onclick="applyChanges()">✓ Apply Changes</button>
  </div>
  <table class="param-table">
    <thead>
      <tr>
        <th>Parameter Name</th>
        <th>Value</th>
        <th>Type</th>
        <th>Writable</th>
      </tr>
    </thead>
    <tbody id="paramTableBody">
      <!-- Populated by JS -->
    </tbody>
  </table>
</div>
```

JS pattern for rendering parameters:
```javascript
function renderParams(params) {
  const tbody = document.getElementById('paramTableBody');
  tbody.innerHTML = '';
  params.forEach(p => {
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td class="param-name mono">${escapeHtml(p.name)}</td>
      <td class="param-value">
        ${p.writable
          ? `<input class="param-input" data-name="${escapeHtml(p.name)}"
               value="${escapeHtml(p.value)}" />`
          : `<span class="mono">${escapeHtml(p.value)}</span>`}
      </td>
      <td class="param-type muted">${p.type}</td>
      <td>${p.writable ? '<span class="badge badge--online">✓</span>' : '—'}</td>
    `;
    tbody.appendChild(tr);
  });
}

// Filter on keyup
document.getElementById('paramSearch').addEventListener('input', function() {
  const q = this.value.toLowerCase();
  document.querySelectorAll('.param-table tbody tr').forEach(row => {
    row.style.display = row.querySelector('.param-name').textContent.toLowerCase().includes(q)
      ? '' : 'none';
  });
});
```

### 4. Task Queue Panel

```html
<div class="task-list">
  <!-- Each task card -->
  <div class="task-card task-card--pending">
    <div class="task-header">
      <span class="task-rpc">GetParameterValues</span>
      <span class="task-time muted">2 min ago</span>
      <span class="badge badge--pending">Pending</span>
    </div>
    <div class="task-detail mono">Device.DeviceInfo.SoftwareVersion</div>
  </div>
</div>
```

Task states: `pending` (yellow), `running` (blue pulse), `success` (green), `failed` (red).

### 5. Firmware Upgrade UI

```html
<div class="firmware-panel">
  <div class="current-firmware">
    <label>Current Version</label>
    <span class="mono" id="currentFw">v2.1.3</span>
  </div>
  <div class="upgrade-form">
    <input type="url" id="fwUrl" placeholder="Firmware URL (http/ftp)" />
    <input type="text" id="fwUsername" placeholder="Username (optional)" />
    <input type="password" id="fwPassword" placeholder="Password (optional)" />
    <div class="btn-group">
      <button class="btn btn--primary" onclick="scheduleFirmwareUpgrade()">
        Schedule Download RPC
      </button>
      <button class="btn btn--ghost" onclick="scheduleReboot()">
        Reboot Only
      </button>
    </div>
  </div>
</div>
```

---

## JavaScript Patterns

### API Communication

Always communicate to the ACS backend via REST. Wrap all fetch calls:

```javascript
async function acsAPI(method, path, body) {
  const opts = {
    method,
    headers: { 'Content-Type': 'application/json' },
  };
  if (body) opts.body = JSON.stringify(body);
  const res = await fetch(`/api/v1${path}`, opts);
  if (!res.ok) {
    const err = await res.json().catch(() => ({ message: res.statusText }));
    throw new Error(err.message || 'API error');
  }
  return res.json();
}

// Examples
const devices = await acsAPI('GET', '/devices');
const params  = await acsAPI('GET', `/devices/${serial}/parameters`);
await acsAPI('POST', `/devices/${serial}/tasks`, { rpc: 'Reboot' });
```

### Polling for Task Completion

CWMP RPCs are async. Poll for task completion:

```javascript
async function pollTask(serial, taskId, intervalMs = 2000, maxAttempts = 30) {
  for (let i = 0; i < maxAttempts; i++) {
    const task = await acsAPI('GET', `/devices/${serial}/tasks/${taskId}`);
    if (task.status === 'success') return task;
    if (task.status === 'failed') throw new Error(task.error);
    await new Promise(r => setTimeout(r, intervalMs));
  }
  throw new Error('Task timed out');
}
```

### Live Session Feed (SSE)

```javascript
function connectSessionFeed(serial) {
  const evtSource = new EventSource(`/api/v1/devices/${serial}/events`);
  evtSource.onmessage = (e) => {
    const event = JSON.parse(e.data);
    appendSessionLog(event);
  };
  evtSource.onerror = () => {
    showToast('Session feed disconnected', 'warning');
  };
}
```

---

## Utility Helpers

```javascript
function escapeHtml(str) {
  return String(str)
    .replace(/&/g,'&amp;')
    .replace(/</g,'&lt;')
    .replace(/>/g,'&gt;')
    .replace(/"/g,'&quot;');
}

function formatUptime(seconds) {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${d}d ${h}h ${m}m`;
}

function timeAgo(isoString) {
  const diff = (Date.now() - new Date(isoString)) / 1000;
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff/60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff/3600)}h ago`;
  return `${Math.floor(diff/86400)}d ago`;
}

function showToast(message, type = 'info') {
  const toast = document.createElement('div');
  toast.className = `toast toast--${type}`;
  toast.textContent = message;
  document.getElementById('toastContainer').appendChild(toast);
  setTimeout(() => toast.remove(), 3500);
}
```

---

## Accessibility & UX Rules

- All interactive elements must be keyboard accessible.
- Loading states: show skeleton rows or spinner, never blank screen.
- Empty states: show helpful message ("No devices found. Wait for a CPE to send an Inform.").
- Confirmation dialogs before destructive actions (Reboot, Factory Reset).
- Parameter edits must be staged (show diff) before sending SetParameterValues.

---

## Decision Tree

```
New UI component needed?
│
├─ Showing a list of devices? → Use device-table template
├─ Showing device parameters? → Use param-viewer with search
├─ Triggering an ACS RPC?
│   ├─ Fire-and-forget (Reboot) → button + confirm dialog + toast
│   └─ Needs result (GetParameterValues) → button + task card + pollTask()
├─ Showing session history? → Session log with SSE feed
└─ Firmware upgrade? → firmware-panel template
```
