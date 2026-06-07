(() => {
  const state = {
    view: 'day', // 'day' | 'hashtag' | 'search'
    date: todayISO(),
    hashtag: null,
    query: '',
  };

  {
    const dq = new URLSearchParams(location.search).get('date');
    if (dq && /^\d{4}-\d{2}-\d{2}$/.test(dq)) state.date = dq;
  }

  const el = {
    text: document.getElementById('text'),
    charCount: document.getElementById('charCount'),
    postBtn: document.getElementById('postBtn'),
    timeline: document.getElementById('timeline'),
    emptyState: document.getElementById('emptyState'),
    prevDay: document.getElementById('prevDay'),
    nextDay: document.getElementById('nextDay'),
    todayBtn: document.getElementById('todayBtn'),
    dayLabel: document.getElementById('dayLabel'),
    datePicker: document.getElementById('datePicker'),
    dayNav: document.getElementById('dayNav'),
    composer: document.getElementById('composer'),
    filterBanner: document.getElementById('filterBanner'),
    filterTag: document.getElementById('filterTag'),
    clearFilter: document.getElementById('clearFilter'),
    searchToggle: document.getElementById('searchToggle'),
    searchBar: document.getElementById('searchBar'),
    searchInput: document.getElementById('searchInput'),
    searchCount: document.getElementById('searchCount'),
    searchClose: document.getElementById('searchClose'),
    editDialog: document.getElementById('editDialog'),
    editForm: document.getElementById('editForm'),
    editText: document.getElementById('editText'),
    editCharCount: document.getElementById('editCharCount'),
    sealBadge: document.getElementById('sealBadge'),
    sealDialog: document.getElementById('sealDialog'),
    sealStatus: document.getElementById('sealStatus'),
    sealDate: document.getElementById('sealDate'),
    sealCount: document.getElementById('sealCount'),
    sealedAt: document.getElementById('sealedAt'),
    sealHash: document.getElementById('sealHash'),
    sealOTS: document.getElementById('sealOTS'),
    sealProofLink: document.getElementById('sealProofLink'),
  };

  let editingId = null;
  let knownHashtags = [];
  let currentSeal = null;
  let verifyStatus = null;
  // UI feature flags from /api/config; both buttons are hidden by default.
  const config = { show_permalink: false, show_quote: false };

  async function refreshConfig() {
    try {
      const data = await api('/api/config');
      config.show_permalink = !!data.show_permalink;
      config.show_quote = !!data.show_quote;
    } catch (err) {
      // non-critical: keep defaults (both hidden)
    }
  }
  // refCache: id (string) -> entry-shaped object, or { missing: true }
  const refCache = new Map();

  async function refreshVerifyStatus() {
    try {
      verifyStatus = await api('/api/verify');
    } catch (err) {
      verifyStatus = null;
    }
  }

  // dayIsBroken returns true if the chain is broken at or before `date`.
  // We return true for any date >= first_broken_day because VerifyChain stops
  // at the first mismatch and later days are no longer verifiable as part of a
  // consistent chain.
  function dayIsBroken(date) {
    if (!verifyStatus || verifyStatus.chain_ok) return false;
    if (!verifyStatus.first_broken_day) return false;
    return date >= verifyStatus.first_broken_day;
  }

  async function refreshHashtags() {
    try {
      const data = await api('/api/hashtags');
      knownHashtags = data.hashtags || [];
    } catch (err) {
      // non-critical
    }
  }

  function attachAutocomplete(textarea) {
    const wrap = document.createElement('div');
    wrap.className = 'hashtag-autocomplete-wrap';
    textarea.parentNode.insertBefore(wrap, textarea);
    wrap.appendChild(textarea);

    const box = document.createElement('div');
    box.className = 'hashtag-suggestions hidden';
    wrap.appendChild(box);

    const state = { mode: null, items: [], selected: 0, start: -1 };
    let searchSeq = 0;

    function hashtagToken() {
      const caret = textarea.selectionStart;
      if (caret !== textarea.selectionEnd) return null;
      const before = textarea.value.slice(0, caret);
      const m = before.match(/#([\p{L}\p{N}_]*)$/u);
      if (!m) return null;
      const ch = before.charAt(before.length - m[0].length - 1);
      if (ch && /[\p{L}\p{N}_]/u.test(ch)) return null;
      return { start: caret - m[0].length, query: m[1].toLowerCase() };
    }

    function refToken() {
      const caret = textarea.selectionStart;
      if (caret !== textarea.selectionEnd) return null;
      const before = textarea.value.slice(0, caret);
      const m = before.match(/@([^\s@]*)$/);
      if (!m) return null;
      const ch = before.charAt(before.length - m[0].length - 1);
      if (ch && /[\p{L}\p{N}_]/u.test(ch)) return null;
      return { start: caret - m[0].length, query: m[1] };
    }

    function close() {
      state.mode = null;
      state.items = [];
      state.selected = 0;
      state.start = -1;
      box.classList.add('hidden');
    }

    function render() {
      box.innerHTML = '';
      state.items.forEach((item, i) => {
        const div = document.createElement('div');
        div.className = 'hashtag-suggestion' + (i === state.selected ? ' selected' : '');
        if (item.kind === 'ref') {
          div.classList.add('ref-suggestion');
          const date = document.createElement('span');
          date.className = 'ref-suggestion-date';
          date.textContent = formatRefDate(item.entry.created_at);
          const text = document.createElement('span');
          text.className = 'ref-suggestion-text';
          text.textContent = truncate(item.entry.text, 60);
          div.appendChild(date);
          div.appendChild(text);
        } else {
          div.textContent = '#' + item.tag;
        }
        div.addEventListener('mousedown', (e) => {
          e.preventDefault();
          commit(item);
        });
        box.appendChild(div);
      });
      box.classList.remove('hidden');
    }

    function commit(item) {
      const caret = textarea.selectionStart;
      const before = textarea.value.slice(0, state.start);
      const after = textarea.value.slice(caret);
      const tok = item.kind === 'ref' ? '@' + item.entry.id : '#' + item.tag;
      const needSpaceAfter = !/^\s/.test(after);
      const insert = tok + (needSpaceAfter ? ' ' : '');
      textarea.value = before + insert + after;
      const pos = before.length + insert.length;
      textarea.setSelectionRange(pos, pos);
      if (item.kind === 'ref') {
        refCache.set(String(item.entry.id), item.entry);
      }
      textarea.dispatchEvent(new Event('input'));
      close();
    }

    async function update() {
      const hashTok = hashtagToken();
      if (hashTok) {
        const matches = knownHashtags
          .filter((t) => t.startsWith(hashTok.query) && t !== hashTok.query)
          .slice(0, 8)
          .map((tag) => ({ kind: 'hashtag', tag }));
        if (matches.length === 0) return close();
        state.mode = 'hashtag';
        state.items = matches;
        state.start = hashTok.start;
        if (state.selected >= matches.length) state.selected = 0;
        render();
        return;
      }
      const refTok = refToken();
      if (refTok) {
        const seq = ++searchSeq;
        let entries;
        try {
          const data = await api(
            '/api/entries?q=' + encodeURIComponent(refTok.query) + '&limit=10',
          );
          entries = data.entries || [];
        } catch (err) {
          return close();
        }
        if (seq !== searchSeq) return; // stale response
        if (entries.length === 0) return close();
        state.mode = 'ref';
        state.items = entries.map((e) => ({ kind: 'ref', entry: e }));
        state.start = refTok.start;
        if (state.selected >= state.items.length) state.selected = 0;
        render();
        return;
      }
      close();
    }

    textarea.addEventListener('input', update);
    textarea.addEventListener('click', update);
    textarea.addEventListener('keyup', (e) => {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' ||
          e.key === 'Home' || e.key === 'End') {
        update();
      }
    });
    textarea.addEventListener('blur', () => setTimeout(close, 120));
    textarea.addEventListener('keydown', (e) => {
      if (!state.mode) return;
      if (e.key === 'Tab' ||
          (e.key === 'Enter' && !e.ctrlKey && !e.metaKey && !e.shiftKey && !e.altKey)) {
        e.preventDefault();
        commit(state.items[state.selected]);
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        state.selected = (state.selected + 1) % state.items.length;
        render();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        state.selected = (state.selected - 1 + state.items.length) % state.items.length;
        render();
      } else if (e.key === 'Escape') {
        e.preventDefault();
        close();
      }
    });
  }

  function truncate(s, n) {
    if (s.length <= n) return s;
    return s.slice(0, n - 1).trimEnd() + '…';
  }

  function formatRefDate(iso) {
    const d = new Date(iso);
    const now = new Date();
    const opts = d.getFullYear() === now.getFullYear()
      ? { day: '2-digit', month: '2-digit' }
      : { day: '2-digit', month: '2-digit', year: '2-digit' };
    return d.toLocaleDateString(undefined, opts);
  }

  // insertAtCaret inserts text at the textarea's caret, focuses it, and fires
  // an input event so the autocomplete picks the change up. Pads with single
  // spaces on either side as needed so the inserted token is whitespace-bounded.
  function insertAtCaret(textarea, text) {
    textarea.focus();
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const before = textarea.value.slice(0, start);
    const after = textarea.value.slice(end);
    const needSpaceBefore = before.length > 0 && !/\s$/.test(before);
    const needSpaceAfter = !/^\s/.test(after);
    const insert = (needSpaceBefore ? ' ' : '') + text + (needSpaceAfter ? ' ' : '');
    textarea.value = before + insert + after;
    const pos = before.length + insert.length;
    textarea.setSelectionRange(pos, pos);
    textarea.dispatchEvent(new Event('input'));
  }

  function todayISO() {
    const d = new Date();
    return toISO(d);
  }

  function toISO(d) {
    const year = d.getFullYear();
    const month = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }

  function parseISO(s) {
    const [y, m, d] = s.split('-').map(Number);
    return new Date(y, m - 1, d);
  }

  function shiftDay(dateStr, delta) {
    const d = parseISO(dateStr);
    d.setDate(d.getDate() + delta);
    return toISO(d);
  }

  function formatDateLong(dateStr) {
    const d = parseISO(dateStr);
    return d.toLocaleDateString(undefined, {
      weekday: 'long', year: 'numeric', month: 'long', day: 'numeric'
    });
  }

  function formatTime(iso) {
    const d = new Date(iso);
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  }

  function formatDateTime(iso) {
    const d = new Date(iso);
    return d.toLocaleString(undefined, {
      day: '2-digit', month: '2-digit', year: 'numeric',
      hour: '2-digit', minute: '2-digit',
    });
  }

  function formatCoordsDDM(lat, lon) {
    const part = (val, pos, neg) => {
      const dir = val >= 0 ? pos : neg;
      const abs = Math.abs(val);
      const deg = Math.floor(abs);
      const min = (abs - deg) * 60;
      return `${deg}°${min.toFixed(2)}'${dir}`;
    };
    return `${part(lat, 'N', 'S')} ${part(lon, 'E', 'W')}`;
  }

  function escapeHTML(s) {
    return s.replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  marked.use({
    breaks: true,
    gfm: true,
    extensions: [
      {
        name: 'hashtag',
        level: 'inline',
        start(src) {
          const m = src.match(/(?<![&\p{L}\p{N}_])#[\p{L}\p{N}_]/u);
          return m != null ? m.index : undefined;
        },
        tokenizer(src) {
          const match = src.match(/^#([\p{L}\p{N}_]+)/u);
          if (!match) return;
          return { type: 'hashtag', raw: match[0], tag: match[1] };
        },
        renderer(token) {
          return `<a href="#" class="hashtag" data-tag="${token.tag.toLowerCase()}">#${escapeHTML(token.tag)}</a>`;
        },
      },
      {
        name: 'entryRef',
        level: 'inline',
        start(src) {
          const m = src.match(/(?<![\p{L}\p{N}_])@\d/u);
          return m != null ? m.index : undefined;
        },
        tokenizer(src) {
          const match = src.match(/^@(\d+)/);
          if (!match) return;
          return { type: 'entryRef', raw: match[0], id: match[1] };
        },
        renderer(token) {
          return `<a href="#" class="entry-ref" data-ref-id="${token.id}">↪ #${token.id}</a>`;
        },
      },
    ],
    renderer: {
      link({ href, title, tokens }) {
        const text = this.parser.parseInline(tokens);
        if (!/^https?:\/\//i.test(href)) return text;
        const t = title ? ` title="${escapeHTML(title)}"` : '';
        return `<a href="${escapeHTML(href)}" target="_blank" rel="noopener"${t}>${text}</a>`;
      },
      image({ href, title, text }) {
        if (!/^https?:\/\//i.test(href)) return escapeHTML(text);
        const t = title ? ` title="${escapeHTML(title)}"` : '';
        return `<img src="${escapeHTML(href)}" alt="${escapeHTML(text)}"${t}>`;
      },
    },
  });

  function renderText(text) {
    const safe = text.replace(/</g, '&lt;');
    return marked.parse(safe);
  }

  async function fetchRef(id) {
    const key = String(id);
    if (refCache.has(key)) return refCache.get(key);
    try {
      const data = await api('/api/entries/' + encodeURIComponent(key));
      refCache.set(key, data);
      return data;
    } catch (err) {
      const missing = { missing: true };
      refCache.set(key, missing);
      return missing;
    }
  }

  function renderRefChip(link, data) {
    link.innerHTML = '';
    if (data.missing) {
      link.classList.add('missing');
      link.textContent = '↪ Eintrag gelöscht';
      return;
    }
    const arrow = document.createElement('span');
    arrow.className = 'entry-ref-arrow';
    arrow.textContent = '↪';
    const date = document.createElement('span');
    date.className = 'entry-ref-date';
    date.textContent = formatRefDate(data.created_at);
    const txt = document.createElement('span');
    txt.className = 'entry-ref-text';
    txt.textContent = truncate(data.text, 80);
    link.appendChild(arrow);
    link.appendChild(date);
    link.appendChild(txt);
  }

  async function resolveRefs(container) {
    const links = container.querySelectorAll('a.entry-ref[data-ref-id]');
    const ids = new Set();
    links.forEach((a) => ids.add(a.dataset.refId));
    await Promise.all(
      [...ids].map(async (id) => {
        const data = await fetchRef(id);
        container.querySelectorAll(`a.entry-ref[data-ref-id="${id}"]`)
          .forEach((a) => renderRefChip(a, data));
      }),
    );
  }

  function highlightEntry(id) {
    const node = el.timeline.querySelector(`.entry[data-id="${id}"]`);
    if (!node) return;
    node.scrollIntoView({ behavior: 'smooth', block: 'center' });
    node.classList.remove('highlighted');
    void node.offsetWidth;
    node.classList.add('highlighted');
  }

  async function copyToClipboard(text) {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return;
    }
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
    } finally {
      document.body.removeChild(ta);
    }
  }

  async function copyPermalink(entryId, btn) {
    const url = location.origin + location.pathname + '#/entry/' + entryId;
    const orig = btn.dataset.origLabel || btn.textContent;
    btn.dataset.origLabel = orig;
    try {
      await copyToClipboard(url);
      btn.textContent = 'Kopiert!';
      btn.classList.add('copied');
    } catch (err) {
      btn.textContent = 'Fehler';
    }
    setTimeout(() => {
      btn.textContent = orig;
      btn.classList.remove('copied');
    }, 1500);
  }

  async function quoteEntry(entry) {
    refCache.set(String(entry.id), entry);
    const token = '@' + entry.id;
    if (el.editDialog.open) {
      insertAtCaret(el.editText, token);
      return;
    }
    if (state.view !== 'day' || !isToday(state.date)) {
      state.date = todayISO();
      await loadDay();
    }
    insertAtCaret(el.text, token);
  }

  async function navigateToEntry(refId) {
    const data = await fetchRef(refId);
    if (data.missing) return;
    const targetDate = toISO(new Date(data.created_at));
    if (state.view === 'day' && state.date === targetDate) {
      highlightEntry(refId);
      return;
    }
    state.date = targetDate;
    await loadDay();
    highlightEntry(refId);
  }

  async function api(path, opts = {}) {
    const res = await fetch(path, {
      headers: { 'Content-Type': 'application/json' },
      ...opts,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data.error || `HTTP ${res.status}`);
    }
    return data;
  }

  function isToday(dateStr) {
    return dateStr === todayISO();
  }

  function isSameDay(isoTime, dateStr) {
    const d = new Date(isoTime);
    return toISO(d) === dateStr;
  }

  function canEdit(entry) {
    if (entry.automated) return false;
    if (state.view === 'hashtag') {
      return isSameDay(entry.created_at, todayISO());
    }
    return isToday(state.date);
  }

  function canDelete(entry) {
    if (state.view === 'hashtag') {
      return isSameDay(entry.created_at, todayISO());
    }
    return isToday(state.date);
  }

  function updateCharCount(textEl, countEl) {
    const len = [...textEl.value].length;
    countEl.textContent = `${len} / 10000`;
    countEl.classList.toggle('over', len > 10000);
  }

  function renderEntries(entries) {
    el.timeline.innerHTML = '';
    if (!entries || entries.length === 0) {
      el.emptyState.classList.remove('hidden');
      return;
    }
    el.emptyState.classList.add('hidden');

    for (const entry of entries) {
      const div = document.createElement('article');
      div.className = 'entry' + (entry.automated ? ' automated' : '');
      div.dataset.id = entry.id;

      const meta = document.createElement('div');
      meta.className = 'entry-meta';
      const timeSpan = document.createElement('span');
      timeSpan.className = 'time';
      if (state.view === 'hashtag' || state.view === 'search') {
        timeSpan.textContent = formatDateTime(entry.created_at);
      } else {
        timeSpan.textContent = formatTime(entry.created_at);
      }
      meta.appendChild(timeSpan);

      if (entry.edited_at) {
        const edited = document.createElement('span');
        edited.className = 'edited-mark';
        edited.textContent = `bearbeitet ${formatTime(entry.edited_at)}`;
        meta.appendChild(edited);
      }

      div.appendChild(meta);

      const textDiv = document.createElement('div');
      textDiv.className = 'entry-text';
      textDiv.innerHTML = renderText(entry.text);
      div.appendChild(textDiv);

      const actions = document.createElement('div');
      actions.className = 'entry-actions';
      if (typeof entry.lat === 'number' && typeof entry.lon === 'number') {
        const geo = document.createElement('a');
        geo.className = 'geo-link';
        geo.href = `https://www.openstreetmap.org/?mlat=${entry.lat}&mlon=${entry.lon}#map=16/${entry.lat}/${entry.lon}`;
        geo.target = '_blank';
        geo.rel = 'noopener';
        geo.title = `${entry.lat.toFixed(6)}, ${entry.lon.toFixed(6)}`;
        geo.innerHTML = `<i class="fa-solid fa-location-dot"></i><span>${formatCoordsDDM(entry.lat, entry.lon)}</span>`;
        actions.appendChild(geo);
      }
      if (config.show_permalink) {
        const permaBtn = document.createElement('button');
        permaBtn.type = 'button';
        permaBtn.title = 'Permalink';
        permaBtn.innerHTML = '<i class="fa-solid fa-link"></i>';
        permaBtn.addEventListener('click', () => copyPermalink(entry.id, permaBtn));
        actions.appendChild(permaBtn);
      }
      if (config.show_quote) {
        const quoteBtn = document.createElement('button');
        quoteBtn.type = 'button';
        quoteBtn.title = 'Zitieren';
        quoteBtn.innerHTML = '<i class="fa-solid fa-quote-right"></i>';
        quoteBtn.addEventListener('click', () => quoteEntry(entry));
        actions.appendChild(quoteBtn);
      }
      if (canEdit(entry)) {
        const editBtn = document.createElement('button');
        editBtn.type = 'button';
        editBtn.title = 'Bearbeiten';
        editBtn.innerHTML = '<i class="fa-regular fa-pen-to-square"></i>';
        editBtn.addEventListener('click', () => openEdit(entry));
        actions.appendChild(editBtn);
      }
      if (canDelete(entry)) {
        const delBtn = document.createElement('button');
        delBtn.type = 'button';
        delBtn.title = 'Löschen';
        delBtn.className = 'danger';
        delBtn.innerHTML = '<i class="fa-regular fa-trash-can"></i>';
        delBtn.addEventListener('click', () => deleteEntry(entry.id));
        actions.appendChild(delBtn);
      }
      div.appendChild(actions);

      el.timeline.appendChild(div);
    }
    resolveRefs(el.timeline);
    if (state.view === 'search' && state.query) {
      highlightMatches(el.timeline, state.query);
    }
  }

  // highlightMatches wraps case-insensitive occurrences of query in <mark>.
  // It walks text nodes only (not the rendered HTML string) so markdown tags,
  // links and HTML entities stay intact.
  function highlightMatches(container, query) {
    const needle = query.toLowerCase();
    if (!needle) return;
    for (const textDiv of container.querySelectorAll('.entry-text')) {
      const walker = document.createTreeWalker(textDiv, NodeFilter.SHOW_TEXT);
      const nodes = [];
      while (walker.nextNode()) nodes.push(walker.currentNode);
      for (const node of nodes) {
        const text = node.nodeValue;
        const lower = text.toLowerCase();
        let idx = lower.indexOf(needle);
        if (idx === -1) continue;
        const frag = document.createDocumentFragment();
        let pos = 0;
        while (idx !== -1) {
          if (idx > pos) frag.appendChild(document.createTextNode(text.slice(pos, idx)));
          const mark = document.createElement('mark');
          mark.textContent = text.slice(idx, idx + needle.length);
          frag.appendChild(mark);
          pos = idx + needle.length;
          idx = lower.indexOf(needle, pos);
        }
        if (pos < text.length) frag.appendChild(document.createTextNode(text.slice(pos)));
        node.parentNode.replaceChild(frag, node);
      }
    }
  }

  async function loadDay() {
    state.view = 'day';
    state.hashtag = null;
    el.emptyState.textContent = 'Keine Einträge an diesem Tag.';
    el.searchBar.classList.add('hidden');
    el.filterBanner.classList.add('hidden');
    el.dayNav.classList.remove('hidden');
    el.composer.classList.toggle('hidden', !isToday(state.date));
    el.composer.style.display = isToday(state.date) ? '' : 'none';
    el.dayLabel.textContent = formatDateLong(state.date);
    el.datePicker.value = state.date;
    el.datePicker.max = todayISO();

    try {
      const data = await api(`/api/entries?date=${encodeURIComponent(state.date)}`);
      renderEntries(data.entries);
    } catch (err) {
      alert('Fehler beim Laden: ' + err.message);
    }
    await updateSealBadge();
  }

  const ICON_LOCK =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none"' +
    ' stroke="currentColor" stroke-width="1.25" stroke-linecap="round"' +
    ' stroke-linejoin="round" aria-hidden="true">' +
    '<rect x="5.5" y="10.5" width="13" height="10" rx="2"/>' +
    '<path d="M8.5 10.5V7.25a3.5 3.5 0 0 1 7 0v3.25"/>' +
    '</svg>';

  const ICON_WARN =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none"' +
    ' stroke="currentColor" stroke-width="1.25" stroke-linecap="round"' +
    ' stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M12 3.5L21.5 20.5H2.5L12 3.5z"/>' +
    '<line x1="12" y1="10" x2="12" y2="14.5"/>' +
    '<circle cx="12" cy="17.5" r="0.6" fill="currentColor" stroke="none"/>' +
    '</svg>';

  async function updateSealBadge() {
    if (state.view !== 'day' || isToday(state.date)) {
      el.sealBadge.classList.add('hidden');
      currentSeal = null;
      return;
    }
    try {
      currentSeal = await api(`/api/seals/${encodeURIComponent(state.date)}`);
      el.sealBadge.classList.remove('hidden');
      const broken = dayIsBroken(state.date);
      el.sealBadge.classList.toggle('broken', broken);
      if (broken) {
        el.sealBadge.innerHTML = ICON_WARN;
        el.sealBadge.title = 'Chain gebrochen — Manipulation erkannt';
      } else {
        el.sealBadge.innerHTML = ICON_LOCK;
        el.sealBadge.title = 'Tag ist versiegelt';
      }
    } catch (err) {
      el.sealBadge.classList.add('hidden');
      currentSeal = null;
    }
  }

  function showSealDialog() {
    if (!currentSeal) return;
    const broken = dayIsBroken(currentSeal.date);
    el.sealStatus.classList.toggle('status-broken', broken);
    el.sealStatus.classList.toggle('status-ok', !broken);
    if (!broken) {
      el.sealStatus.textContent = 'Kette intakt';
    } else if (verifyStatus && currentSeal.date === verifyStatus.first_broken_day) {
      el.sealStatus.textContent =
        'Manipulation erkannt: ' + (verifyStatus.break_reason || 'unbekannte Abweichung');
    } else if (verifyStatus) {
      el.sealStatus.textContent =
        'Kette bereits ab ' + verifyStatus.first_broken_day + ' gebrochen — dieser Tag ist nicht mehr verifizierbar';
    } else {
      el.sealStatus.textContent = 'Status unbekannt';
    }
    el.sealDate.textContent = currentSeal.date;
    el.sealCount.textContent = String(currentSeal.entry_count);
    el.sealedAt.textContent = formatDateTime(currentSeal.sealed_at);
    el.sealHash.textContent = currentSeal.seal_hash;
    if (currentSeal.ots_upgraded_at) {
      el.sealOTS.textContent =
        'Bitcoin-bestätigt (' + formatDateTime(currentSeal.ots_upgraded_at) + ')';
    } else if (currentSeal.has_ots_proof) {
      el.sealOTS.textContent =
        'Bei OpenTimestamps eingereicht, wartet auf Bitcoin-Bestätigung';
    } else {
      el.sealOTS.textContent = 'Kein externer Zeitstempel vorhanden';
    }
    if (currentSeal.has_ots_proof) {
      el.sealProofLink.href = `/api/seals/${encodeURIComponent(currentSeal.date)}/proof.ots`;
      el.sealProofLink.style.display = '';
    } else {
      el.sealProofLink.style.display = 'none';
    }
    el.sealDialog.showModal();
  }

  async function loadHashtag(tag) {
    state.view = 'hashtag';
    state.hashtag = tag;
    state.query = '';
    el.emptyState.textContent = 'Keine Einträge mit diesem Hashtag.';
    el.searchBar.classList.add('hidden');
    el.searchInput.value = '';
    el.searchCount.textContent = '';
    el.filterBanner.classList.remove('hidden');
    el.filterTag.textContent = `#${tag}`;
    el.dayNav.classList.add('hidden');
    el.composer.style.display = 'none';

    try {
      const data = await api(`/api/entries?hashtag=${encodeURIComponent(tag)}`);
      renderEntries(data.entries);
    } catch (err) {
      alert('Fehler beim Laden: ' + err.message);
    }
  }

  function openSearch() {
    el.searchBar.classList.remove('hidden');
    el.searchInput.focus();
    loadSearch(el.searchInput.value.trim());
  }

  function closeSearch() {
    el.searchBar.classList.add('hidden');
    el.searchInput.value = '';
    el.searchCount.textContent = '';
    state.query = '';
    state.date = todayISO();
    loadDay();
  }

  async function loadSearch(query) {
    state.view = 'search';
    state.query = query;
    el.filterBanner.classList.add('hidden');
    el.dayNav.classList.add('hidden');
    el.composer.style.display = 'none';

    if (!query) {
      el.timeline.innerHTML = '';
      el.searchCount.textContent = '';
      el.emptyState.textContent = 'Suchbegriff eingeben…';
      el.emptyState.classList.remove('hidden');
      return;
    }

    try {
      const data = await api(`/api/entries?q=${encodeURIComponent(query)}&limit=50`);
      const n = data.entries.length;
      el.searchCount.textContent = n >= 50 ? '50+ Treffer' : `${n} Treffer`;
      el.emptyState.textContent = 'Keine Treffer.';
      renderEntries(data.entries);
    } catch (err) {
      alert('Fehler beim Laden: ' + err.message);
    }
  }

  async function postEntry() {
    const text = el.text.value.trim();
    if (!text) return;
    if ([...text].length > 10000) {
      alert('Text zu lang (max. 10000 Zeichen)');
      return;
    }
    el.postBtn.disabled = true;
    try {
      await api('/api/entries', {
        method: 'POST',
        body: JSON.stringify({ text }),
      });
      el.text.value = '';
      updateCharCount(el.text, el.charCount);
      if (!isToday(state.date)) {
        state.date = todayISO();
      }
      await loadDay();
      refreshHashtags();
    } catch (err) {
      alert('Fehler: ' + err.message);
    } finally {
      el.postBtn.disabled = false;
    }
  }

  async function deleteEntry(id) {
    if (!confirm('Eintrag wirklich löschen?')) return;
    try {
      await api(`/api/entries/${id}`, { method: 'DELETE' });
      if (state.view === 'search') {
        await loadSearch(state.query);
      } else if (state.view === 'hashtag') {
        await loadHashtag(state.hashtag);
      } else {
        await loadDay();
      }
    } catch (err) {
      alert('Fehler: ' + err.message);
    }
  }

  function openEdit(entry) {
    editingId = entry.id;
    el.editText.value = entry.text;
    updateCharCount(el.editText, el.editCharCount);
    el.editDialog.showModal();
    el.editText.focus();
  }

  async function saveEdit() {
    const text = el.editText.value.trim();
    if (!text) return;
    if ([...text].length > 10000) {
      alert('Text zu lang (max. 10000 Zeichen)');
      return;
    }
    try {
      await api(`/api/entries/${editingId}`, {
        method: 'PUT',
        body: JSON.stringify({ text }),
      });
      editingId = null;
      if (state.view === 'search') {
        await loadSearch(state.query);
      } else if (state.view === 'hashtag') {
        await loadHashtag(state.hashtag);
      } else {
        await loadDay();
      }
      refreshHashtags();
    } catch (err) {
      alert('Fehler: ' + err.message);
    }
  }

  // Events
  el.text.addEventListener('input', () => updateCharCount(el.text, el.charCount));
  el.editText.addEventListener('input', () => updateCharCount(el.editText, el.editCharCount));

  el.postBtn.addEventListener('click', postEntry);
  el.text.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      postEntry();
    }
  });

  el.prevDay.addEventListener('click', () => {
    state.date = shiftDay(state.date, -1);
    loadDay();
  });
  el.nextDay.addEventListener('click', () => {
    if (isToday(state.date)) return;
    state.date = shiftDay(state.date, 1);
    loadDay();
  });
  el.todayBtn.addEventListener('click', () => {
    state.date = todayISO();
    loadDay();
  });
  el.datePicker.addEventListener('change', () => {
    if (el.datePicker.value) {
      state.date = el.datePicker.value;
      loadDay();
    }
  });

  el.clearFilter.addEventListener('click', () => {
    state.date = todayISO();
    loadDay();
  });

  el.searchToggle.addEventListener('click', () => {
    if (el.searchBar.classList.contains('hidden')) {
      openSearch();
    } else {
      closeSearch();
    }
  });
  el.searchClose.addEventListener('click', closeSearch);
  let searchDebounce = null;
  el.searchInput.addEventListener('input', () => {
    clearTimeout(searchDebounce);
    const q = el.searchInput.value.trim();
    searchDebounce = setTimeout(() => loadSearch(q), 200);
  });
  el.searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeSearch();
    }
  });

  el.timeline.addEventListener('click', (e) => {
    const ref = e.target.closest('a.entry-ref');
    if (ref) {
      e.preventDefault();
      if (ref.classList.contains('missing')) return;
      navigateToEntry(ref.dataset.refId);
      return;
    }
    const a = e.target.closest('a.hashtag');
    if (!a) return;
    e.preventDefault();
    const tag = a.dataset.tag;
    loadHashtag(tag);
  });


  el.sealBadge.addEventListener('click', showSealDialog);

  el.editForm.addEventListener('submit', (e) => {
    const val = e.submitter && e.submitter.value;
    if (val === 'save') {
      e.preventDefault();
      saveEdit().then(() => el.editDialog.close());
    } else {
      editingId = null;
    }
  });

  attachAutocomplete(el.text);
  attachAutocomplete(el.editText);

  function parseEntryHash() {
    const m = location.hash.match(/^#\/entry\/(\d+)$/);
    return m ? m[1] : null;
  }

  // Initial load: if the URL points at a permalink (#/entry/<id>), resolve it
  // first so we don't briefly flash today's view before jumping to the target.
  async function init() {
    refreshHashtags();
    await refreshConfig();
    await refreshVerifyStatus();
    const refId = parseEntryHash();
    if (refId) {
      const data = await fetchRef(refId);
      if (!data.missing) {
        state.date = toISO(new Date(data.created_at));
        await loadDay();
        highlightEntry(refId);
        return;
      }
    }
    await loadDay();
  }

  window.addEventListener('hashchange', () => {
    const refId = parseEntryHash();
    if (refId) navigateToEntry(refId);
  });

  init();
})();
