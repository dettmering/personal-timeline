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
  };

  let editingId = null;

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
    el.dayLabel.textContent = isToday(state.date) ? `Heute — ${formatDateLong(state.date)}` : formatDateLong(state.date);
    el.datePicker.value = state.date;
    el.datePicker.max = todayISO();

    try {
      const data = await api(`/api/entries?date=${encodeURIComponent(state.date)}`);
      renderEntries(data.entries);
    } catch (err) {
      alert('Fehler beim Laden: ' + err.message);
    }
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
    } catch (err) {
      alert('Fehler: ' + err.message);
    } finally {
      el.postBtn.disabled = false;
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

  el.editForm.addEventListener('submit', (e) => {
    const val = e.submitter && e.submitter.value;
    if (val === 'save') {
      e.preventDefault();
      saveEdit().then(() => el.editDialog.close());
    } else {
      editingId = null;
    }
  });

  // Initial load
  loadDay();
})();
