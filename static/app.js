(() => {
  const state = {
    view: 'day', // 'day' | 'hashtag'
    date: todayISO(),
    hashtag: null,
  };

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
    editDialog: document.getElementById('editDialog'),
    editForm: document.getElementById('editForm'),
    editText: document.getElementById('editText'),
    editCharCount: document.getElementById('editCharCount'),
    sealBadge: document.getElementById('sealBadge'),
    sealDialog: document.getElementById('sealDialog'),
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

  async function refreshHashtags() {
    try {
      const data = await api('/api/hashtags');
      knownHashtags = data.hashtags || [];
    } catch (err) {
      // non-critical
    }
  }

  function attachHashtagAutocomplete(textarea) {
    const wrap = document.createElement('div');
    wrap.className = 'hashtag-autocomplete-wrap';
    textarea.parentNode.insertBefore(wrap, textarea);
    wrap.appendChild(textarea);

    const box = document.createElement('div');
    box.className = 'hashtag-suggestions hidden';
    wrap.appendChild(box);

    const state = { active: false, matches: [], selected: 0, start: -1 };

    function tokenAtCaret() {
      const caret = textarea.selectionStart;
      if (caret !== textarea.selectionEnd) return null;
      const before = textarea.value.slice(0, caret);
      const m = before.match(/#([\p{L}\p{N}_]*)$/u);
      if (!m) return null;
      const ch = before.charAt(before.length - m[0].length - 1);
      if (ch && /[\p{L}\p{N}_]/u.test(ch)) return null;
      return { start: caret - m[0].length, prefix: m[1].toLowerCase() };
    }

    function render() {
      box.innerHTML = '';
      state.matches.forEach((tag, i) => {
        const item = document.createElement('div');
        item.className = 'hashtag-suggestion' + (i === state.selected ? ' selected' : '');
        item.textContent = '#' + tag;
        item.addEventListener('mousedown', (e) => {
          e.preventDefault();
          commit(tag);
        });
        box.appendChild(item);
      });
      box.classList.remove('hidden');
    }

    function close() {
      state.active = false;
      state.matches = [];
      state.selected = 0;
      state.start = -1;
      box.classList.add('hidden');
    }

    function update() {
      const tok = tokenAtCaret();
      if (!tok) return close();
      const matches = knownHashtags
        .filter((t) => t.startsWith(tok.prefix) && t !== tok.prefix)
        .slice(0, 8);
      if (matches.length === 0) return close();
      state.active = true;
      state.matches = matches;
      state.start = tok.start;
      if (state.selected >= matches.length) state.selected = 0;
      render();
    }

    function commit(tag) {
      const caret = textarea.selectionStart;
      const before = textarea.value.slice(0, state.start);
      const after = textarea.value.slice(caret);
      const insert = '#' + tag + ' ';
      textarea.value = before + insert + after;
      const pos = before.length + insert.length;
      textarea.setSelectionRange(pos, pos);
      textarea.dispatchEvent(new Event('input'));
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
      if (!state.active) return;
      if (e.key === 'Tab' ||
          (e.key === 'Enter' && !e.ctrlKey && !e.metaKey && !e.shiftKey && !e.altKey)) {
        e.preventDefault();
        commit(state.matches[state.selected]);
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        state.selected = (state.selected + 1) % state.matches.length;
        render();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        state.selected = (state.selected - 1 + state.matches.length) % state.matches.length;
        render();
      } else if (e.key === 'Escape') {
        e.preventDefault();
        close();
      }
    });
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

  function escapeHTML(s) {
    return s.replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  function renderText(text) {
    const esc = escapeHTML(text);
    return esc.replace(/#([\p{L}\p{N}_]+)/gu, (_, tag) => {
      return `<a href="#" class="hashtag" data-tag="${tag.toLowerCase()}">#${tag}</a>`;
    });
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

  function updateCharCount(textEl, countEl) {
    const len = [...textEl.value].length;
    countEl.textContent = `${len} / 1000`;
    countEl.classList.toggle('over', len > 1000);
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
      if (state.view === 'hashtag') {
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

      if (canEdit(entry)) {
        const actions = document.createElement('div');
        actions.className = 'entry-actions';
        const editBtn = document.createElement('button');
        editBtn.type = 'button';
        editBtn.textContent = 'Bearbeiten';
        editBtn.addEventListener('click', () => openEdit(entry));
        actions.appendChild(editBtn);
        const delBtn = document.createElement('button');
        delBtn.type = 'button';
        delBtn.textContent = 'Löschen';
        delBtn.className = 'danger';
        delBtn.addEventListener('click', () => deleteEntry(entry.id));
        actions.appendChild(delBtn);
        div.appendChild(actions);
      }

      el.timeline.appendChild(div);
    }
  }

  async function loadDay() {
    state.view = 'day';
    state.hashtag = null;
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

  async function updateSealBadge() {
    if (state.view !== 'day' || isToday(state.date)) {
      el.sealBadge.classList.add('hidden');
      currentSeal = null;
      return;
    }
    try {
      currentSeal = await api(`/api/seals/${encodeURIComponent(state.date)}`);
      el.sealBadge.classList.remove('hidden');
    } catch (err) {
      el.sealBadge.classList.add('hidden');
      currentSeal = null;
    }
  }

  function showSealDialog() {
    if (!currentSeal) return;
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

  async function postEntry() {
    const text = el.text.value.trim();
    if (!text) return;
    if ([...text].length > 1000) {
      alert('Text zu lang (max. 1000 Zeichen)');
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
      if (state.view === 'hashtag') {
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
    if ([...text].length > 1000) {
      alert('Text zu lang (max. 1000 Zeichen)');
      return;
    }
    try {
      await api(`/api/entries/${editingId}`, {
        method: 'PUT',
        body: JSON.stringify({ text }),
      });
      editingId = null;
      if (state.view === 'hashtag') {
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

  el.timeline.addEventListener('click', (e) => {
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

  attachHashtagAutocomplete(el.text);
  attachHashtagAutocomplete(el.editText);

  // Initial load
  refreshHashtags();
  loadDay();
})();
