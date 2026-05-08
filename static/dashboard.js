(() => {
  const el = {
    from: document.getElementById('fromPicker'),
    to: document.getElementById('toPicker'),
    apply: document.getElementById('applyBtn'),
    presets: document.querySelectorAll('.dashboard-presets button'),
    heatmap: document.getElementById('heatmap'),
    map: document.getElementById('map'),
    mapEmpty: document.getElementById('mapEmpty'),
    summary: document.getElementById('summary'),
    legendMin: document.getElementById('legendMin'),
    legendMax: document.getElementById('legendMax'),
    legendBuckets: [
      document.getElementById('legendBucket0'),
      document.getElementById('legendBucket1'),
      document.getElementById('legendBucket2'),
      document.getElementById('legendBucket3'),
      document.getElementById('legendBucket4'),
    ],
  };

  let map = null;
  let markerLayer = null;

  function pad(n) { return n < 10 ? '0' + n : '' + n; }
  function toISO(d) { return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()); }
  function parseISO(s) {
    const [y, m, d] = s.split('-').map(Number);
    return new Date(y, m - 1, d);
  }
  function addDays(d, n) {
    const r = new Date(d);
    r.setDate(r.getDate() + n);
    return r;
  }
  // Mo=0, Di=1, ..., So=6
  function dowMon(d) { return (d.getDay() + 6) % 7; }

  function defaultRange() {
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const from = addDays(today, -364);
    return { from: toISO(from), to: toISO(today) };
  }

  function readRange() {
    const params = new URLSearchParams(location.search);
    const f = params.get('from');
    const t = params.get('to');
    const ok = (s) => s && /^\d{4}-\d{2}-\d{2}$/.test(s);
    if (ok(f) && ok(t)) return { from: f, to: t };
    return defaultRange();
  }

  function setRange(from, to) {
    const params = new URLSearchParams(location.search);
    params.set('from', from);
    params.set('to', to);
    history.replaceState(null, '', '?' + params.toString());
    el.from.value = from;
    el.to.value = to;
    load();
  }

  async function fetchEntries(from, to) {
    const r = await fetch('/api/entries?from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to));
    if (!r.ok) throw new Error('fetch failed: ' + r.status);
    const data = await r.json();
    return data.entries || [];
  }

  function groupByDay(entries) {
    const m = new Map();
    for (const e of entries) {
      const d = new Date(e.created_at);
      const key = toISO(d);
      m.set(key, (m.get(key) || 0) + 1);
    }
    return m;
  }

  function intensity(count, max) {
    if (count <= 0 || max <= 0) return 0;
    return Math.min(4, Math.ceil((count / max) * 4));
  }

  function bucketStart(k, max) {
    return Math.floor(((k - 1) * max) / 4) + 1;
  }

  function fitHeatmap(weeks) {
    const wrap = el.heatmap.parentElement;
    if (!wrap) return;
    const wrapStyle = getComputedStyle(wrap);
    const padX = parseFloat(wrapStyle.paddingLeft) + parseFloat(wrapStyle.paddingRight);
    const wrapGap = parseFloat(wrapStyle.gap || wrapStyle.columnGap) || 0;
    const labels = wrap.querySelector('.heatmap-weekdays');
    const labelW = labels ? labels.getBoundingClientRect().width : 0;
    const innerW = wrap.clientWidth - padX - wrapGap - labelW;
    if (innerW <= 0 || weeks <= 0) return;
    const cellGap = 3;
    const fit = Math.floor((innerW - (weeks - 1) * cellGap) / weeks);
    const size = Math.max(8, Math.min(20, fit));
    wrap.style.setProperty('--cell-size', size + 'px');
  }

  let resizeObserver = null;
  function ensureResizeObserver() {
    if (resizeObserver) return;
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', () => {
        const w = parseInt(el.heatmap.dataset.weeks || '0', 10);
        if (w > 0) fitHeatmap(w);
      });
      resizeObserver = true;
      return;
    }
    let raf = 0;
    resizeObserver = new ResizeObserver(() => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        const w = parseInt(el.heatmap.dataset.weeks || '0', 10);
        if (w > 0) fitHeatmap(w);
      });
    });
    resizeObserver.observe(el.heatmap.parentElement);
  }

  function updateLegend(max) {
    el.legendMin.textContent = '0';
    el.legendMax.textContent = String(max);
    el.legendBuckets[0].title = '0 Einträge';
    for (let k = 1; k <= 4; k++) {
      const a = bucketStart(k, max);
      const b = k === 4 ? max : bucketStart(k + 1, max) - 1;
      el.legendBuckets[k].title = max <= 0
        ? '–'
        : (a === b ? a + ' Einträge' : a + '–' + b + ' Einträge');
    }
  }

  function renderHeatmap(from, to, byDay) {
    const fromD = parseISO(from);
    const toD = parseISO(to);
    // Snap start to Monday of from-week.
    const start = addDays(fromD, -dowMon(fromD));
    // Snap end to Sunday of to-week.
    const end = addDays(toD, 6 - dowMon(toD));

    el.heatmap.innerHTML = '';
    const totalDays = Math.round((end - start) / 86400000) + 1;
    const weeks = Math.ceil(totalDays / 7);
    el.heatmap.style.gridTemplateColumns = 'repeat(' + weeks + ', var(--cell-size, 12px))';
    el.heatmap.dataset.weeks = String(weeks);
    fitHeatmap(weeks);

    let max = 0;
    for (const v of byDay.values()) if (v > max) max = v;
    updateLegend(max);

    let monthLabel = -1;
    for (let w = 0; w < weeks; w++) {
      for (let r = 0; r < 7; r++) {
        const day = addDays(start, w * 7 + r);
        const iso = toISO(day);
        const inRange = day >= fromD && day <= toD;
        const cell = document.createElement('div');
        cell.className = 'cell';
        if (!inRange) {
          cell.classList.add('outside');
        } else {
          const c = byDay.get(iso) || 0;
          cell.classList.add('intensity-' + intensity(c, max));
          cell.title = iso + ' — ' + c + (c === 1 ? ' Eintrag' : ' Einträge');
          cell.style.cursor = 'pointer';
          cell.addEventListener('click', () => {
            location.href = '/?date=' + iso;
          });
        }
        cell.style.gridColumn = (w + 1);
        cell.style.gridRow = (r + 1);
        el.heatmap.appendChild(cell);

        // Month label on first row of each new month.
        if (r === 0 && day.getMonth() !== monthLabel) {
          monthLabel = day.getMonth();
          // Optional future enhancement; skipped to keep heatmap simple.
        }
      }
    }
  }

  function escapeHTML(s) {
    return s.replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  function renderMap(entries) {
    const geo = entries.filter((e) => typeof e.lat === 'number' && typeof e.lon === 'number');

    if (!map) {
      map = L.map(el.map, { scrollWheelZoom: true });
      L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
        maxZoom: 19,
        attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
      }).addTo(map);
      map.setView([51.1657, 10.4515], 5); // Default: middle of Germany.
    }
    if (markerLayer) {
      markerLayer.remove();
      markerLayer = null;
    }

    if (geo.length === 0) {
      el.mapEmpty.classList.remove('hidden');
      return;
    }
    el.mapEmpty.classList.add('hidden');

    const layer = L.layerGroup();
    const points = [];
    for (const e of geo) {
      const m = L.marker([e.lat, e.lon]);
      const d = new Date(e.created_at);
      const iso = toISO(d);
      const preview = e.text.length > 80 ? e.text.slice(0, 80) + '…' : e.text;
      m.bindPopup(
        '<div class="map-popup"><strong>' + iso + '</strong><br>' +
        escapeHTML(preview) +
        '<br><a href="/?date=' + iso + '">Tag öffnen &rarr;</a></div>'
      );
      layer.addLayer(m);
      points.push([e.lat, e.lon]);
    }
    layer.addTo(map);
    markerLayer = layer;
    if (points.length === 1) {
      map.setView(points[0], 12);
    } else {
      map.fitBounds(points, { padding: [20, 20] });
    }
  }

  function renderSummary(entries, from, to) {
    const geo = entries.filter((e) => typeof e.lat === 'number' && typeof e.lon === 'number').length;
    el.summary.textContent =
      entries.length + ' Einträge im Zeitraum ' + from + ' – ' + to +
      ' · ' + geo + ' mit Geo';
  }

  async function load() {
    const from = el.from.value;
    const to = el.to.value;
    if (!from || !to) return;
    if (to < from) {
      el.summary.textContent = 'Bis-Datum darf nicht vor Von-Datum liegen.';
      return;
    }
    el.summary.textContent = 'Lädt…';
    try {
      const entries = await fetchEntries(from, to);
      const byDay = groupByDay(entries);
      renderHeatmap(from, to, byDay);
      renderMap(entries);
      renderSummary(entries, from, to);
    } catch (err) {
      el.summary.textContent = 'Fehler: ' + err.message;
    }
  }

  function applyPreset(kind) {
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    let from;
    if (kind === '365') from = addDays(today, -364);
    else if (kind === '90') from = addDays(today, -89);
    else if (kind === 'ytd') from = new Date(today.getFullYear(), 0, 1);
    else return;
    setRange(toISO(from), toISO(today));
  }

  el.apply.addEventListener('click', () => setRange(el.from.value, el.to.value));
  el.presets.forEach((b) => b.addEventListener('click', () => applyPreset(b.dataset.range)));
  el.from.addEventListener('keydown', (e) => { if (e.key === 'Enter') setRange(el.from.value, el.to.value); });
  el.to.addEventListener('keydown', (e) => { if (e.key === 'Enter') setRange(el.from.value, el.to.value); });

  // Init
  ensureResizeObserver();
  const r = readRange();
  el.from.value = r.from;
  el.to.value = r.to;
  load();
})();
