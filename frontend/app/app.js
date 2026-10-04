const API_URL = 'http://localhost:5173'; // your Go backend
const PREFIX = 'ICT-ACC-', SUFFIX_LEN = 8, MAX = 99999999;

const $ = s => document.querySelector(s);
const esc = s => String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

function buildCode(raw) {
    if (!/^\d+$/.test(raw)) return null;
    const n = parseInt(raw, 10);
    return n > MAX ? null : PREFIX + String(n).padStart(SUFFIX_LEN, '0');
}

async function api(path, opts) {
    const res = await fetch(`${API_URL}/api/v1${path}`, opts);
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${await res.text()}`);
    return res.json();
}
const getItems  = () => api('/items');
const deleteAll = () => api('/items', { method: 'DELETE' });
const sendCode  = code => api('/scan', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code })
});

const scanLog = {
    get() { try { return JSON.parse(localStorage.getItem('scans') || '[]'); } catch { return []; } },
    set(v) { localStorage.setItem('scans', JSON.stringify(v)); },
    stats() {
        const s = this.get();
        return { total: s.length, ok: s.filter(x => x.status === 'ok').length, err: s.filter(x => x.status === 'err').length };
    }
};

// Scan a raw number, log the result, return the log entry
async function doScan(raw) {
    const code = buildCode(raw.trim());
    if (!code) throw new Error('Enter a whole number between 0 and 99999999.');
    const entry = { code, time: new Date().toLocaleTimeString() };
    try {
        const r = await sendCode(code);
        entry.status = 'ok'; entry.message = r.status ?? 'OK'; entry.isNew = /new/i.test(r.status ?? '');
    } catch (e) {
        entry.status = 'err'; entry.message = e.message;
    }
    const log = scanLog.get(); log.push(entry); scanLog.set(log);
    return entry;
}

function scanRow(s) {
    return `<li><span class="code">${esc(s.code)}</span><span class="status-${s.status}">${esc(s.message)}</span><span class="time">${esc(s.time)}</span></li>`;
}
function itemRow(i) {
    const title = i.name ? `<b>${esc(i.name)}</b> <span class="time">${esc(i.code)}</span>` : `<span class="code">${esc(i.code)}</span>`;
    const sub = i.item ? `<br><span class="time">${esc(i.item)}</span>` : '';
    const cat = i.category ? `<span class="tag">${esc(i.category)}</span> ` : '';
    return `<li title="${esc(i.description || '')}"><span>${title}${sub}</span><span>${cat}<span class="time">scanned ${esc(i.scanCount)}×</span></span><span class="time">${esc(new Date(i.lastSeen).toLocaleString())}</span></li>`;
}

function renderHeader(active) {
    const pages = [['index.html','Overview'],['scan.html','Scan'],['history.html','History'],['records.html','Records'],['categories.html','Categories'],['admin.html','Admin']];
    $('#header').outerHTML = `<header class="site-header"><div class="container site-header__inner">
    <a class="logo" href="index.html">Scan Dashboard</a>
    <nav class="nav">${pages.map(([h,l]) => `<a href="${h}" class="${h===active?'is-current':''}">${l}</a>`).join('')}</nav>
  </div></header>`;
}

// --- categories (predefined list, editable; kept in this browser) ---
const catStore = {
    defaults: ['Laptop', 'Phone', 'Monitor', 'Accessory', 'Other'],
    get() { try { const c = JSON.parse(localStorage.getItem('categories')); if (Array.isArray(c)) return c; } catch {} return this.defaults.slice(); },
    set(v) { localStorage.setItem('categories', JSON.stringify(v)); },
    add(name) {
        name = name.trim(); const c = this.get();
        if (!name || c.some(x => x.toLowerCase() === name.toLowerCase())) return false;
        c.push(name); this.set(c); return true;
    },
    remove(name) { this.set(this.get().filter(x => x !== name)); }
};

// Sends name/item/description/category to the backend
const saveDetails = (code, d) => api(`/items/${encodeURIComponent(code)}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(d)
});

// Modal shown after a new item is registered. Resolves true if saved, false if skipped.
function askDetails(code) {
    return new Promise(resolve => {
        const dlg = document.createElement('dialog');
        dlg.className = 'modal';
        dlg.innerHTML = `<form id="mf">
      <h3>New item</h3><p class="code">${esc(code)}</p>
      <label>Name<input class="input" name="name" required autocomplete="off"></label>
      <label>Item<input class="input" name="item" required autocomplete="off" placeholder="e.g. Dell Latitude 5440"></label>
      <label>Category<span class="row"><select class="input" name="category"></select>
        <button type="button" class="btn" id="newCat">New category</button></span></label>
      <span class="row" id="catRow" hidden><input class="input" id="catInput" placeholder="Category name"><button type="button" class="btn" id="catAdd">Add</button></span>
      <label>Description<textarea class="input" name="description" rows="3"></textarea></label>
      <p class="hint" id="mh"></p>
      <span class="row"><button class="btn btn--primary" id="save">Save</button><button type="button" class="btn" id="skip">Skip for now</button></span>
    </form>`;
        document.body.appendChild(dlg);
        const f = dlg.querySelector('#mf'), sel = f.category;
        let saved = false;
        const fill = pick => { sel.innerHTML = catStore.get().map(c => `<option>${esc(c)}</option>`).join(''); if (pick) sel.value = pick; };
        fill();
        f.querySelector('#newCat').onclick = () => { f.querySelector('#catRow').hidden = false; f.querySelector('#catInput').focus(); };
        const addCat = () => {
            const v = f.querySelector('#catInput').value;
            if (catStore.add(v)) { fill(v.trim()); f.querySelector('#catInput').value = ''; f.querySelector('#catRow').hidden = true; }
            else f.querySelector('#mh').textContent = 'Enter a new category name that is not in the list yet.';
        };
        f.querySelector('#catAdd').onclick = addCat;
        f.querySelector('#catInput').addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); addCat(); } });
        f.querySelector('#skip').onclick = () => dlg.close();
        f.addEventListener('submit', async e => {
            e.preventDefault();
            const btn = f.querySelector('#save'); btn.disabled = true;
            try {
                await saveDetails(code, { name: f.name.value.trim(), item: f.item.value.trim(), description: f.description.value.trim(), category: sel.value });
                saved = true; dlg.close();
            } catch (err) { f.querySelector('#mh').textContent = `Could not save: ${err.message}`; btn.disabled = false; }
        });
        dlg.addEventListener('close', () => { dlg.remove(); resolve(saved); });
        dlg.showModal();
        f.name.focus();
    });
}