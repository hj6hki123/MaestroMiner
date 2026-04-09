import './style.css'
// ══ i18n engine ════════════════════════════════════════════
var I18n = (function () {
  var langs = {};
  var langMeta = [
    { id: 'en', shortName: 'EN', nativeName: 'English' },
    { id: 'zh-TW', shortName: '繁中', nativeName: '繁體中文' },
    { id: 'zh-CN', shortName: '简中', nativeName: '简体中文' },
    { id: 'ja', shortName: 'JP', nativeName: '日本語' },
  ];
  var current = 'en';

  function loadLang(id, cb) {
    if (langs[id]) { cb(); return; }
    fetch('/locales/' + id + '.json')
      .then(function (r) { return r.json(); })
      .then(function (d) { langs[id] = d; cb(); })
      .catch(function () { langs[id] = {}; cb(); });
  }

  function detectLang() {
    var nav = (navigator.language || 'en').toLowerCase();
    if (nav.startsWith('zh-tw') || nav.startsWith('zh-hant')) return 'zh-TW';
    if (nav.startsWith('zh')) return 'zh-CN';
    if (nav.startsWith('ja')) return 'ja';
    return 'en';
  }

  function t(key) {
    var d = langs[current] || {};
    return d[key] !== undefined ? d[key] : ((langs['en'] || {})[key] || key);
  }

  function apply(id, save) {
    current = id;
    if (save) { try { localStorage.setItem('ssm-lang', id); } catch (e) { } }
    document.querySelectorAll('[data-i18n]').forEach(function (el) {
      el.innerHTML = t(el.getAttribute('data-i18n'));
    });
    document.querySelectorAll('[data-i18n-placeholder]').forEach(function (el) {
      el.placeholder = t(el.getAttribute('data-i18n-placeholder'));
    });
    var d = langs[id] || {};
    var meta = langMeta.filter(function (m) { return m.id === id; })[0] || langMeta[0];
    document.getElementById('lb-name').textContent = meta.shortName;
    document.querySelectorAll('.lang-opt').forEach(function (opt) {
      opt.classList.toggle('active', opt.getAttribute('data-lang') === id);
    });
    document.documentElement.lang = id;
  }

  function buildMenu() {
    var menu = document.getElementById('lang-menu');
    if (!menu) return;
    menu.innerHTML = langMeta.map(function (m) {
      return '<div class="lang-opt" data-lang="' + m.id + '" onclick="I18n.select(\'' + m.id + '\')">'
        + '<div class="lo-info"><span class="lo-name">' + m.shortName + '</span>'
        + '<span class="lo-native">' + m.nativeName + '</span></div></div>';
    }).join('');
  }

  function init() {
    buildMenu();
    var saved; try { saved = localStorage.getItem('ssm-lang'); } catch (e) { }
    var target = saved || detectLang();
    loadLang(target, function () {
      apply(target, false);
    });
  }

  return {
    t: t, apply: apply,
    select: function (id) {
      loadLang(id, function () {
        apply(id, true); closeLangMenu();
        updateDynamicTexts(); renderAllJitters();
      });
    },
    init: init,
    loadLang: loadLang,
  };
})();

function t(key) { return I18n.t(key); }

function toggleLangMenu() {
  var menu = document.getElementById('lang-menu');
  var btn = document.getElementById('lang-btn');
  var open = menu.classList.toggle('open');
  btn.classList.toggle('open', open);
}
function closeLangMenu() {
  document.getElementById('lang-menu').classList.remove('open');
  document.getElementById('lang-btn').classList.remove('open');
}
document.addEventListener('click', function (e) { if (!e.target.closest('#lang-picker')) closeLangMenu(); });

var THEME_KEY = 'ssm-theme';

function applyTheme(theme, save) {
  var t = theme === 'light' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', t);
  document.documentElement.classList.toggle('dark', t === 'dark');
  var ico = document.getElementById('theme-ico');
  if (ico) ico.textContent = t === 'dark' ? '☾' : '☀';
  if (save) {
    try { localStorage.setItem(THEME_KEY, t); } catch (e) { }
  }
}

function initTheme() {
  var saved;
  try { saved = localStorage.getItem(THEME_KEY); } catch (e) { }
  if (saved === 'light' || saved === 'dark') {
    applyTheme(saved, false);
    return;
  }
  applyTheme('dark', false);
}

function toggleTheme() {
  var cur = document.documentElement.getAttribute('data-theme') || 'dark';
  applyTheme(cur === 'dark' ? 'light' : 'dark', true);
}

function toggleDevDrop(e) {
  e.stopPropagation();
  var drop = document.getElementById('dev-drop');
  if (drop.classList.contains('open')) {
    drop.classList.remove('open');
  } else {
    loadDevOptions();
  }
}


function loadDevOptions() {
  fetch('/api/device')
    .then(function (r) { return r.json(); })
    .then(function (d) {
      var drop = document.getElementById('dev-drop');
      drop.classList.add('dev-drop-style');
      var keys = Object.keys(d);

      if (!keys.length) {
        drop.innerHTML = '<div class="drop-hint">' + t('device.none') + '</div>';
      } else {
        drop.innerHTML = keys.map(function (s) {
          return '<div class="di" onclick="selectDevSerial(\'' + s + '\')">'
            + '<span class="di-id">' + s + '</span>'
            + '<div class="di-info"><div class="di-title">' + d[s].width + ' × ' + d[s].height + '</div></div>'
            + '</div>';
        }).join('');
      }
      drop.classList.add('open');
    });
}


function selectDevSerial(s) {
  document.getElementById('dev-serial').value = s;
  document.getElementById('dev-drop').classList.remove('open');
}

document.addEventListener('click', function (e) {
  if (!e.target.closest('#dev-drop') && e.target.id !== 'btn-dev-drop') {
    document.getElementById('dev-drop').classList.remove('open');
  }
});
// ══ jitter ═════════════════════════════════════════════════
var JITTER_POS_MAP = [0, 0.02, 0.04, 0.06, 0.08, 0.10, 0.12, 0.15, 0.18, 0.22, 0.25];

function jitterRealValue(key, raw) {
  raw = parseInt(raw);
  return key === 'position' ? (JITTER_POS_MAP[raw] || 0) : raw;
}

function getGreatCountRaw() {
  var inp = document.getElementById('inp-grCount');
  var raw = parseInt(inp ? inp.value : 0);
  if (!isFinite(raw) || raw < 0) raw = 0;
  return raw;
}

function renderJitter(key) {
  var raw = key === 'grCount' ? getGreatCountRaw() : parseInt(document.getElementById('sld-' + key).value);
  var el = document.getElementById('val-' + key);

  if (key !== 'grCount') {
    var sld = document.getElementById('sld-' + key);
    var pct = ((raw - (sld.min || 0)) / ((sld.max || 100) - (sld.min || 0))) * 100;
    sld.style.setProperty('--val', pct + '%');
  }

  if (key !== 'grOffset' && raw === 0) { el.textContent = 'OFF'; el.style.color = 'var(--hint)'; return; }
  el.style.color = 'var(--blue)';
  if (key === 'position') {
    el.textContent = '±' + Math.round((JITTER_POS_MAP[raw] || 0) * 100) + '%';
  } else if (key === 'grOffset') {
    el.textContent = raw + ' ms';
  } else if (key === 'grCount') {
    el.textContent = raw + ' notes';
  } else {
    el.textContent = '±' + raw + ' ms';
  }
}

function renderAllJitters() { ['timing', 'position', 'tapDur', 'grOffset', 'grCount'].forEach(renderJitter); }
function onJitter(key) { renderJitter(key); }
function onGreatCountInput() { renderJitter('grCount'); }

// ══ state ══════════════════════════════════════════════════
var S = { backend: 'adb', diff: 3, orient: 'left', mode: 'bang', state: 0, offset: 0, songId: 0, songData: null, db: null, dropIdx: -1, _lastLogState: -1, _lastGreatSig: '' };
var DN_BANG = ['easy', 'normal', 'hard', 'expert', 'special'];
var DN_PJSK = ['easy', 'normal', 'hard', 'expert', 'master', 'append'];
var DL_BANG = ['EASY', 'NORMAL', 'HARD', 'EXPERT', 'SPECIAL'];
var DL_PJSK = ['EASY', 'NORMAL', 'HARD', 'EXPERT', 'MASTER', 'APPEND'];
var DOT_CLS = { 1: 'ready', 2: 'playing', 3: 'done', 4: 'error' };

function diffName(i) {
  var dn = S.mode === 'pjsk' ? DN_PJSK : DN_BANG;
  return dn[i] || dn[3];
}

function diffLabel(i) {
  var dl = S.mode === 'pjsk' ? DL_PJSK : DL_BANG;
  return dl[i] || dl[3];
}

function updateDiffLabels() {
  var btns = document.querySelectorAll('.db');
  if (!btns || !btns.length) return;
  if (btns[4]) btns[4].textContent = diffLabel(4);
  if (btns[5]) {
    btns[5].textContent = diffLabel(5);
    btns[5].style.display = S.mode === 'pjsk' ? '' : 'none';
  }
}

function updateDynamicTexts() {
  var stateMap = { 0: 'state.idle', 1: 'state.ready.full', 2: 'state.playing.full', 3: 'state.done.full', 4: 'state.error.full' };
  var txt = t(stateMap[S.state] || 'state.idle');
  var e1 = document.getElementById('np-state-txt'), e2 = document.getElementById('pn-state-label');
  if (e1) e1.textContent = txt; if (e2) e2.textContent = txt;
  var btn = document.getElementById('btn-start');
  if (btn) btn.innerHTML = t('play.start.btn');
  if (document.getElementById('pane-settings').classList.contains('active')) loadDevices();
}

function nav(id) {
  document.querySelectorAll('.nav-btn').forEach(function (e) { e.classList.remove('active'); });
  document.querySelectorAll('.pane').forEach(function (e) { e.classList.remove('active'); });
  document.getElementById('nav-' + id).classList.add('active');
  document.getElementById('pane-' + id).classList.add('active');
  if (id === 'settings') loadDevices();
}
function navToSearch() {
  nav('song');

  setTimeout(function () {
    var searchInput = document.getElementById('q');
    if (searchInput) {
      searchInput.focus({ preventScroll: true });

      var searchCard = searchInput.closest('.card');
      if (searchCard) {
        searchCard.scrollIntoView({ behavior: 'smooth', block: 'center' });
      }
    }
  }, 50);
}
function setMode(m) {
  S.mode = m; S.db = null; if (S.songId) clearSong();

  // Update active state on the mode buttons.
  ['bang', 'pjsk'].forEach(function (x) {
    document.getElementById('mode-' + x).classList.toggle('active', x === m);
  });

  if (m === 'pjsk') {
    ADV_DEFAULTS.flickDuration = 20; ADV_DEFAULTS.flickFactor = 17;
  } else {
    ADV_DEFAULTS.flickDuration = 60; ADV_DEFAULTS.flickFactor = 20;
    if (S.diff === 5) S.diff = 3;
  }
  updateDiffLabels();
  resetAdvanced();
}
function setBackend(b) {
  S.backend = b;
  ['hid', 'adb'].forEach(function (x) { document.getElementById('backend-' + x).classList.toggle('active', x === b); });
  var warnBox = document.getElementById('hid-warn-box');
    if (warnBox) {
      if (b === 'hid') {
        warnBox.classList.remove('hidden'); 
      } else {
        warnBox.classList.add('hidden');    
      }
    }

  document.getElementById('orient-wrap').style.opacity = b === 'adb' ? '0.4' : '1';
}
function setOrient(o) {
  S.orient = o;
  document.getElementById('ol').classList.toggle('active', o === 'left');
  document.getElementById('or').classList.toggle('active', o === 'right');
}
function setDiff(i) {
  var btns = document.querySelectorAll('.db');

  // Guard: if this difficulty is disabled, ignore the click.
  if (btns[i] && btns[i].classList.contains('dis')) {
    return;
  }

  S.diff = i;
  btns.forEach(function (b, j) {
    b.classList.toggle('active', j === i);
  });

  // Keep play-panel glow synced with current difficulty even before playback starts.
  applyJacketColor(getDiffThemeColor(diffName(i)));
}
function setDiffAvail(avail) {
  document.querySelectorAll('.db').forEach(function (b, i) {
    var ok = !avail || avail.indexOf(i) >= 0;
    b.classList.toggle('dis', !ok);
    if (!ok && S.diff === i) setDiff(avail ? avail[avail.length - 1] : 3);
  });
}

// ══ search ═════════════════════════════════════════════════
var qTimer = null;
function onQInput() {
  var v = document.getElementById('q').value;
  document.getElementById('sc').style.display = v ? 'block' : 'none';
  clearTimeout(qTimer); if (!v.trim()) { closeDrop(); return; }
  qTimer = setTimeout(function () { doSearch(v.trim()); }, 160);
}
function onQFocus() { var v = document.getElementById('q').value.trim(); if (v) doSearch(v); }
function clearQ() { document.getElementById('q').value = ''; document.getElementById('sc').style.display = 'none'; closeDrop(); }

function loadDB(cb) {
  if (S.db) { cb(S.db); return; }
  fetch('/api/songdb?mode=' + S.mode)
    .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function (d) {
      S.db = normalizeSongDB(d);
      cb(S.db);
    })
    .catch(function (e) { log('song-log', t('log.conn.fail') + e, 'err'); });
}

function normalizeSong(rawSong) {
  if (!rawSong) return null;

  // Bestdori payload is already close to the UI schema.
  if (rawSong.musicTitle) {
    return rawSong;
  }

  var id = rawSong.id || rawSong.ID;
  if (!id) return null;

  var title = rawSong.title || rawSong.Title || '';
  var pronunciation = rawSong.pronunciation || rawSong.Pronunciation || '';
  var lyricist = rawSong.lyricist || rawSong.Lyricist || '';
  var composer = rawSong.composer || rawSong.Composer || '';
  var arranger = rawSong.arranger || rawSong.Arranger || '';

  return {
    id: id,
    musicTitle: [title, pronunciation],
    difficulty: rawSong.difficulty || {},
    jacketImage: rawSong.jacketImage || null,
    creatorArtistId: rawSong.creatorArtistId || rawSong.CreatorArtistID || 0,
    __artist: [lyricist, composer, arranger].filter(Boolean).join(' / '),
    __searchNames: [title, pronunciation].filter(Boolean),
    __raw: rawSong,
  };
}

function normalizeSongDB(payload) {
  if (!payload || !payload.songs) {
    return { songs: {}, bands: {}, artists: {} };
  }

  function normalizeSearchText(s) {
    // Normalize for search: lowercase and remove common punctuation (including CJK and full-width symbols)
    return String(s || '')
      .toLowerCase()
      .replace(/[\s\-_.,:;!?\[\]{}'"""~`・，。；！？「」『』（）]/g, '');
  }

  function addSearchName(song, name) {
    if (!song || !name) return;
    if (!song.__searchNames) song.__searchNames = [];
    if (song.__searchNames.indexOf(name) < 0) song.__searchNames.push(name);
    var compact = normalizeSearchText(name);
    if (compact && song.__searchNames.indexOf(compact) < 0) song.__searchNames.push(compact);
  }

  var songs = {};
  if (Array.isArray(payload.songs)) {
    payload.songs.forEach(function (s) {
      var n = normalizeSong(s);
      if (n && n.id) songs[n.id] = n;
    });
  } else {
    Object.keys(payload.songs).forEach(function (sid) {
      var n = normalizeSong(payload.songs[sid]);
      if (n) songs[parseInt(sid)] = n;
    });
  }

  // Handle songsJp for adding Japanese search names
  if (payload.songsJp) {
    var jpArray = Array.isArray(payload.songsJp) ? payload.songsJp : [];
    var jpObject = (typeof payload.songsJp === 'object' && !Array.isArray(payload.songsJp)) ? payload.songsJp : {};
    
    // Process array format
    jpArray.forEach(function (jp) {
      if (!jp || !jp.id) return;
      var songId = parseInt(jp.id);
      var song = songs[songId];
      if (!song) return;
      var jpTitle = jp.title || jp.musicTitle || '';
      var jpPronunciation = jp.pronunciation || '';
      if (jpTitle) addSearchName(song, jpTitle);
      if (jpPronunciation) addSearchName(song, jpPronunciation);
    });
    
    // Process object format (key = id)
    Object.keys(jpObject).forEach(function (id) {
      var jp = jpObject[id];
      if (!jp) return;
      var songId = parseInt(id);
      var song = songs[songId];
      if (!song) return;
      var jpTitle = jp.title || jp.musicTitle || '';
      var jpPronunciation = jp.pronunciation || '';
      if (jpTitle) addSearchName(song, jpTitle);
      if (jpPronunciation) addSearchName(song, jpPronunciation);
    });
  }

  function diffIndexByName(name) {
    switch (String(name || '').toLowerCase()) {
      case 'easy': return 0;
      case 'normal': return 1;
      case 'hard': return 2;
      case 'expert': return 3;
      case 'special': return 4;
      case 'master': return 4;
      case 'append': return 5;
      default: return -1;
    }
  }

  if (Array.isArray(payload.musicDifficulties)) {
    payload.musicDifficulties.forEach(function (md) {
      if (!md) return;
      var songId = md.musicId || md.musicID || md.songId || 0;
      var song = songs[songId];
      if (!song) return;
      var idx = diffIndexByName(md.musicDifficulty);
      if (idx < 0) return;
      if (!song.difficulty) song.difficulty = {};
      song.difficulty[idx] = {
        playLevel: md.playLevel || 0,
        totalNoteCount: md.totalNoteCount || 0,
      };
    });
  }

  var artists = {};
  if (Array.isArray(payload.artists)) {
    payload.artists.forEach(function (a) {
      if (!a || !a.id) return;
      artists[a.id] = a.name || a.pronunciation || '';
    });
  } else if (payload.artists) {
    Object.keys(payload.artists).forEach(function (aid) {
      var a = payload.artists[aid];
      if (!a) return;
      artists[parseInt(aid)] = a.name || a.pronunciation || '';
    });
  }

  return {
    songs: songs,
    bands: payload.bands || {},
    artists: artists,
  };
}

function pickName(arr, preferFirst) {
  if (!arr) return '';
  if (preferFirst) return arr[0] || arr[2] || arr[1] || arr[3] || arr[4] || '';
  return arr[2] || arr[1] || arr[0] || arr[3] || arr[4] || '';
}
function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }

function normalizeForSearch(s) {
  // Remove spaces and common punctuation (including full-width and half-width)
  return String(s || '')
    .toLowerCase()
    .replace(/[\s\-_.,:;!?\[\]{}'"""~`・，。；！？「」『』（）]/g, '');
}

function doSearch(q) {
  loadDB(function (db) {
    var ql = q.toLowerCase(), qc = normalizeForSearch(q), res = [];
    Object.keys(db.songs).forEach(function (sid) {
      var id = parseInt(sid), song = db.songs[sid];
      if (!song || !song.musicTitle) return;
      var names = (song.__searchNames && song.__searchNames.length) ? song.__searchNames : song.musicTitle;
      var hit = names.some(function (n) {
        if (!n) return false;
        var low = String(n).toLowerCase();
        var lowNorm = normalizeForSearch(n);
        return low.indexOf(ql) >= 0 || (qc && lowNorm.indexOf(qc) >= 0);
      });
      if (!hit) return;
      var band = db.bands[song.bandId];
      var artist = '';
      if (S.mode === 'pjsk' && db.artists && song.creatorArtistId) {
        artist = db.artists[song.creatorArtistId] || '';
      }
      if (!artist && band && band.bandName) {
        artist = pickName(band.bandName);
      }
      res.push({ id: id, song: song, band: artist });
    });
    res.sort(function (a, b) {
      var at = pickName(a.song.musicTitle, S.mode === 'pjsk').toLowerCase(), bt = pickName(b.song.musicTitle, S.mode === 'pjsk').toLowerCase();
      var ae = at === ql, be = bt === ql; if (ae && !be) return -1; if (!ae && be) return 1;
      var as = at.startsWith(ql), bs = bt.startsWith(ql); if (as && !bs) return -1; if (!as && bs) return 1;
      return a.id - b.id;
    });
    renderDrop(res.slice(0, 40));
  });
}

function renderDrop(res) {
  var drop = document.getElementById('drop');
  if (!res.length) { drop.innerHTML = '<div class="drop-hint">' + t('drop.none') + '</div>'; drop.classList.add('open'); return; }
  drop.innerHTML = res.map(function (r) {
    var title = pickName(r.song.musicTitle, S.mode === 'pjsk');
    var dh = Object.keys(r.song.difficulty || {}).map(Number).sort().map(function (d) { return '<span class="di-d d-' + diffName(d) + '">' + diffLabel(d) + '</span>'; }).join('');
    return '<div class="di" onclick="selSong(' + r.id + ')">'
      + '<span class="di-id">#' + r.id + '</span>'
      + '<div class="di-info"><div class="di-title">' + esc(title) + '</div>'
      + (r.band ? '<div class="di-band">' + esc(r.band) + '</div>' : '')
      + '</div><div class="di-diffs">' + dh + '</div></div>';
  }).join('');
  drop.classList.add('open'); S.dropIdx = -1;
}

function closeDrop() { document.getElementById('drop').classList.remove('open'); S.dropIdx = -1; }
function onQKey(e) {
  var items = document.getElementById('drop').querySelectorAll('.di');
  if (e.key === 'ArrowDown') { e.preventDefault(); S.dropIdx = Math.min(S.dropIdx + 1, items.length - 1); hiDrop(items); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); S.dropIdx = Math.max(S.dropIdx - 1, -1); hiDrop(items); }
  else if (e.key === 'Enter' && S.dropIdx >= 0 && items[S.dropIdx]) items[S.dropIdx].click();
  else if (e.key === 'Escape') closeDrop();
}
function hiDrop(items) { items.forEach(function (el, i) { el.classList.toggle('hi', i === S.dropIdx); if (i === S.dropIdx) el.scrollIntoView({ block: 'nearest' }); }); }
document.addEventListener('click', function (e) { if (!e.target.closest('.sw')) closeDrop(); });

function selSong(id) {
  loadDB(function (db) {
    var song = db.songs[id]; if (!song) return;
    S.songId = id; S.songData = song;
    var title = pickName(song.musicTitle, S.mode === 'pjsk');
    document.getElementById('sb-id').textContent = '#' + id;
    document.getElementById('sb-title').textContent = title;
    document.getElementById('sel-bar').classList.add('show');
    document.getElementById('q').value = ''; document.getElementById('sc').style.display = 'none';
    document.getElementById('song-id').value = id; closeDrop();
    var avail = Object.keys(song.difficulty || {}).map(Number).sort();
    setDiffAvail(avail.length ? avail : null);
    log('song-log', '#' + id + ' ' + title, 'ok');
  });
}
function clearSong() {
  S.songId = 0; S.songData = null;
  document.getElementById('sel-bar').classList.remove('show');
  document.getElementById('song-id').value = '';
  document.getElementById('q').value = ''; document.getElementById('sc').style.display = 'none';
  setDiffAvail(null); closeDrop();
}
function onManualId() {
  var v = parseInt(document.getElementById('song-id').value) || 0;
  if (v > 0) { S.songId = v; S.songData = null; document.getElementById('sb-id').textContent = '#' + v; document.getElementById('sb-title').textContent = t('manual.title'); document.getElementById('sel-bar').classList.add('show'); setDiffAvail(null); }
}

// ══ log ════════════════════════════════════════════════════
function log(boxId, msg, type) {
  var box = document.getElementById(boxId);
  var l = document.createElement('div'); l.className = 'll ' + (type || '');
  l.textContent = '[' + new Date().toLocaleTimeString() + '] ' + msg;
  box.appendChild(l); box.scrollTop = box.scrollHeight;
}

// ══ SSE ════════════════════════════════════════════════════
var es = new EventSource('/api/events');
es.onmessage = function (e) { var d = JSON.parse(e.data); S.state = d.state; S.offset = d.offset || 0; updateUI(d); };

function updateUI(d) {
  var st = d.state, dotCls = DOT_CLS[st] || '';
  var npDot = document.getElementById('np-dot');
  if (npDot) npDot.className = 'dot ' + dotCls;
  document.getElementById('pn-dot').className = 'dot ' + dotCls;

  var npCard = document.getElementById('np-card');
  if (npCard) {
    npCard.classList.remove('np-state-idle', 'np-state-ready', 'np-state-playing', 'np-state-done', 'np-state-error');
    if (st === 1) npCard.classList.add('np-state-ready');
    else if (st === 2) npCard.classList.add('np-state-playing');
    else if (st === 3) npCard.classList.add('np-state-done');
    else if (st === 4) npCard.classList.add('np-state-error');
    else npCard.classList.add('np-state-idle');
  }
  
  // Sync jacket-wrap playing class
  var jw = document.getElementById('pn-jacket-wrap');
  if (jw) {
    jw.classList.toggle('playing', st === 2);  // 2 = StatePlaying
  }
  
  // Add playing-glow class to player-deck when playing
  var deck = document.querySelector('.player-deck');
  if (deck) {
    deck.classList.toggle('playing-glow', st === 2);
  }
  
  var stateMap = { 0: 'state.idle', 1: 'state.ready.full', 2: 'state.playing.full', 3: 'state.done.full', 4: 'state.error.full' };
  var txt = t(stateMap[st] || 'state.idle');
  document.getElementById('np-state-txt').textContent = txt;
  document.getElementById('pn-state-label').textContent = txt;
  document.getElementById('ov').textContent = d.offset || 0;
  var btn = document.getElementById('btn-start');
  if (st === 1) { btn.disabled = false; btn.classList.add('rdy'); btn.innerHTML = t('play.start.btn'); }
  else { btn.disabled = true; btn.classList.remove('rdy'); btn.innerHTML = t('play.start.btn'); }
  if (d.nowPlaying && (d.nowPlaying.songId > 0 || d.nowPlaying.title)) { showNP(d.nowPlaying); updatePlayCard(d.nowPlaying); }
  if (st !== S._lastLogState) {
    S._lastLogState = st;
    if (st === 1) log('play-log', t('log.ready'), 'info');
    if (st === 2) log('play-log', t('log.playing'), 'info');
    if (st === 3) log('play-log', t('log.done'), 'ok');
    if (st === 4 && d.error) log('play-log', t('log.fail') + d.error, 'err');
  }

	if (st === 1 && typeof d.greatReq === 'number' && typeof d.greatApply === 'number') {
		var greatSig = String(d.greatReq) + '/' + String(d.greatApply);
		if (greatSig !== S._lastGreatSig) {
			S._lastGreatSig = greatSig;
			log('play-log', 'Great applied: ' + d.greatApply + ' / requested: ' + d.greatReq, d.greatApply > 0 ? 'ok' : 'info');
		}
	}
}

function showNP(np) {
  document.getElementById('np-card').style.display = 'block';
  var img = document.getElementById('np-img');
  if (np.jacketUrl) {
    setImageWithFallback(img, np.jacketUrls && np.jacketUrls.length ? np.jacketUrls : [np.jacketUrl]);
    img.style.display = 'block';
    document.getElementById('np-no').style.display = 'none';
  } else {
    img.onerror = null;
    img.removeAttribute('src');
    img.style.display = 'none';
    document.getElementById('np-no').style.display = 'flex';
  }
  document.getElementById('np-title').textContent = np.title || '—';
  document.getElementById('np-artist').textContent = np.artist || '';
  var npDiffRaw = np.diff || 'expert';
  var npDiffKey = normalizeDiffKey(npDiffRaw);
  var db = document.getElementById('np-diff'); db.className = 'np-diff d-' + npDiffKey; db.textContent = String(npDiffRaw || '').toUpperCase();
  document.getElementById('np-lv').textContent = np.diffLevel ? 'Lv.' + np.diffLevel : '';
}

function normalizeDiffKey(diff) {
  var key = String(diff || '').toLowerCase();
  if (key === 'master') return 'special';
  if (key === 'append') return 'append';
  return key;
}

function getDiffThemeColor(diff) {
  var diffColors = {
    easy: '#5ba3e0',
    normal: '#7ab84a',
    hard: '#d4921e',
    expert: '#e06060',
    special: '#9b95e0',
    append: '#4f8ff7'
  };
  var key = normalizeDiffKey(diff);
  return diffColors[key] || '#3b82f6';
}

function applyJacketColor(themeColor) {
  var wrap = document.getElementById('pn-jacket-wrap');
  if (wrap) {
    wrap.style.setProperty('--jacket-color', themeColor);
    wrap.classList.toggle('is-append', themeColor === '#4f8ff7' || themeColor === '#f26ec9');
  }

  var deck = document.querySelector('.player-deck');
  if (deck) {
    deck.style.setProperty('--jacket-color', themeColor);
    deck.classList.toggle('is-append', themeColor === '#4f8ff7' || themeColor === '#f26ec9');
  }

  // Mirror to root so all descendants and pseudo-elements resolve the same value.
  document.documentElement.style.setProperty('--jacket-color', themeColor);
  document.documentElement.classList.toggle('is-append-diff', themeColor === '#4f8ff7' || themeColor === '#f26ec9');
}

function updatePlayCard(np) {
  document.getElementById('pn-none').style.display = 'none'; document.getElementById('pn-loaded').style.display = 'block';
  var pimg = document.getElementById('pn-img');
  if (np.jacketUrl) {
    setImageWithFallback(pimg, np.jacketUrls && np.jacketUrls.length ? np.jacketUrls : [np.jacketUrl]);
    pimg.style.display = 'block';
    document.getElementById('pn-no').style.display = 'none';
  } else {
    pimg.onerror = null;
    pimg.removeAttribute('src');
    pimg.style.display = 'none';
    document.getElementById('pn-no').style.display = 'flex';
  }
  document.getElementById('pn-title-big').textContent = np.title || '—';
  document.getElementById('pn-artist-big').textContent = np.artist || '';
  var rawDiff = np.diff || 'expert';
  var diffKey = normalizeDiffKey(rawDiff);
  var badge = document.getElementById('pn-diff-badge'); badge.className = 'np-diff d-' + diffKey; badge.textContent = String(rawDiff || '').toUpperCase();
  document.getElementById('pn-lv-big').textContent = np.diffLevel ? 'Lv.' + np.diffLevel : '';
  var themeColor = getDiffThemeColor(diffKey);
  applyJacketColor(themeColor);
}

function setImageWithFallback(imgEl, urls) {
  var i = 0;
  function tryNext() {
    if (i >= urls.length) {
      imgEl.onerror = null;
      return;
    }
    var u = urls[i++];
    imgEl.onerror = tryNext;
    imgEl.src = u;
  }
  tryNext();
}

// ══ keyboard ═══════════════════════════════════════════════
document.addEventListener('keydown', function (e) {
  if (document.activeElement.tagName === 'INPUT' || document.activeElement.tagName === 'TEXTAREA') return;
  if (!document.getElementById('pane-play').classList.contains('active')) return;
  switch (e.key) {
    case 'Enter': case ' ': e.preventDefault(); apiStart(); break;
    case 'ArrowLeft': e.preventDefault(); adj(e.ctrlKey || e.metaKey ? -100 : e.shiftKey ? -50 : -10); break;
    case 'ArrowRight': e.preventDefault(); adj(e.ctrlKey || e.metaKey ? 100 : e.shiftKey ? 50 : 10); break;
  }
});

// ══ API ════════════════════════════════════════════════════
function buildNowPlaying() {
  var np = { songId: S.songId, diff: diffName(S.diff), mode: S.mode, title: '', artist: '', diffLevel: 0, jacketUrl: '', jacketUrls: [] };
  if (S.songData) {
    np.title = pickName(S.songData.musicTitle, S.mode === 'pjsk') || '';
    var di = S.songData.difficulty; if (di && di[S.diff]) np.diffLevel = di[S.diff].playLevel || 0;
    var ji = S.songData.jacketImage;
    if (S.mode === 'pjsk') {
      var raw = S.songData.__raw || {};
      var bundle = raw.assetbundleName || ('jacket_s_' + String(S.songId || 0).padStart(3, '0'));
      np.jacketUrls = [
        'https://storage.sekai.best/sekai-jp-assets/music/jacket/' + bundle + '/' + bundle + '.png',
        'https://assets.pjsek.ai/file/pjsekai-assets/startapp/music/jacket/' + bundle + '/' + bundle + '.png'
      ];
      np.jacketUrl = np.jacketUrls[0];
    } else if (ji && ji[0]) {
      var n = Math.ceil(S.songId / 10) * 10 || 10;
      np.jacketUrl = 'https://bestdori.com/assets/jp/musicjacket/musicjacket' + n + '_rip/assets-star-forassetbundle-startapp-musicjacket-musicjacket' + n + '-' + ji[0] + '-jacket.png';
      np.jacketUrls = [np.jacketUrl];
    }
    if (S.mode === 'pjsk' && S.db && S.db.artists && S.songData.creatorArtistId) {
      np.artist = S.db.artists[S.songData.creatorArtistId] || '';
    }
    if (S.db && S.db.bands && S.songData.bandId) { var band = S.db.bands[S.songData.bandId]; if (band && band.bandName) np.artist = pickName(band.bandName); }
    if (!np.artist && S.songData.__artist) np.artist = S.songData.__artist;
  }
  return np;
}

function submitRun() {
  var sid = parseInt(document.getElementById('song-id').value) || S.songId || 0;
  var cp = document.getElementById('chart-path').value.trim();
  var ds = document.getElementById('dev-serial').value.trim();
  if (!sid && !cp) { log('song-log', t('log.no.song'), 'err'); return; }

  var dsInput = document.getElementById('dev-serial');

  if (!ds) {
      var savedSerials = Object.keys(S.devices || {});
      if (savedSerials.length > 0) {
        ds = savedSerials[0]; 
        dsInput.value = ds;    
        log('song-log', 'No serial provided. Auto-selected: ' + ds, 'info');
      }
    }

  var isConfigured = S.devices && S.devices[ds];

  if (!ds || !isConfigured) {
    var errorMsg = !ds
      ? 'Device Serial is required!'
      : 'Device [' + ds + '] is not configured with resolution!';

    log('song-log', errorMsg + ' Redirecting...', 'err');

    if (ds) document.getElementById('dc-s').value = ds;

    nav('settings');

    setTimeout(function () {
      var devCard = document.getElementById('dc-s').closest('.card');
      if (devCard) {
        devCard.scrollIntoView({ behavior: 'smooth', block: 'center' });

        var focusTarget = !ds ? 'dc-s' : 'dc-w';
        document.getElementById(focusTarget).focus({ preventScroll: true });

        devCard.style.transition = 'box-shadow 0.3s ease, border-color 0.3s ease';
        devCard.style.boxShadow = '0 0 20px rgba(239, 68, 68, 0.4)';
        devCard.style.borderColor = '#ef4444';

        setTimeout(function () {
          devCard.style.boxShadow = '';
          devCard.style.borderColor = 'rgba(255, 255, 255, 0.06)';
        }, 2000);
      }
    }, 50);
    return;
  }

  var tRaw = parseInt(document.getElementById('sld-timing').value) || 0;
  var pRaw = parseInt(document.getElementById('sld-position').value) || 0;
  var dRaw = parseInt(document.getElementById('sld-tapDur').value) || 0;
  var grOffsetRaw = parseInt(document.getElementById('sld-grOffset').value) || 10;
  var grCountRaw = getGreatCountRaw();
  var adv = getAdvancedValues();
  var body = { mode: S.mode, backend: S.backend, diff: diffName(S.diff), orient: S.orient, songId: sid, chartPath: cp, deviceSerial: ds, nowPlaying: buildNowPlaying(), timingJitter: tRaw, positionJitter: jitterRealValue('position', pRaw), tapDurJitter: dRaw, greatOffsetMs: grOffsetRaw, greatCount: grCountRaw, tapDuration: adv.tapDuration, flickDuration: adv.flickDuration, flickReportInterval: adv.flickReportInterval, slideReportInterval: adv.slideReportInterval, flickFactor: adv.flickFactor, flickPow: adv.flickPow };
  log('song-log', t('log.loading'), 'info');
  fetch('/api/run', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    .then(function (r) { if (r.ok) { log('song-log', t('log.sent'), 'ok'); nav('play'); } else r.text().then(function (tx) { log('song-log', t('log.fail') + tx, 'err'); }); })
    .catch(function (e) { log('song-log', t('log.conn.fail') + e, 'err'); });
}

function apiStart() { if (S.state !== 1) return; fetch('/api/start', { method: 'POST' }).catch(function (e) { log('play-log', t('log.conn.fail') + e, 'err'); }); }
function apiStop() { fetch('/api/stop', { method: 'POST' }); }

var _adjTimer = null, _adjPending = 0;
function adj(d) { _adjPending += d; clearTimeout(_adjTimer); _adjTimer = setTimeout(function () { if (_adjPending === 0) return; var delta = _adjPending; _adjPending = 0; fetch('/api/offset', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ delta: delta }) }); }, 50); }
function resetOff() { _adjPending = 0; clearTimeout(_adjTimer); var delta = -S.offset; if (delta === 0) return; fetch('/api/offset', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ delta: delta }) }); }

// ══ devices ════════════════════════════════════════════════
function loadDevices() {
  fetch('/api/device').then(function (r) { return r.json(); }).then(function (d) {
    S.devices = d || {};
    var list = document.getElementById('dev-list');
    if (!d || !Object.keys(d).length) { list.innerHTML = '<div style="font-size:12px;color:var(--hint)">' + t('device.none') + '</div>'; return; }
    list.innerHTML = Object.entries(d).map(function (e) { return '<div class="dev-row"><span class="dev-s">' + e[0] + '</span><span>' + e[1].width + ' × ' + e[1].height + '</span><button class="btn-del" onclick="deleteDevice(\'' + e[0] + '\')">' + t('settings.device.delete') + '</button></div>'; }).join('');
  });
}
function saveDevice() {
  var s = document.getElementById('dc-s').value.trim(), w = parseInt(document.getElementById('dc-w').value) || 0, h = parseInt(document.getElementById('dc-h').value) || 0;
  if (!s || !w || !h) { document.getElementById('dc-hint').textContent = t('dc.hint.missing'); return; }
  fetch('/api/device', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ serial: s, width: w, height: h }) })
    .then(function (r) { if (r.ok) { document.getElementById('dc-hint').textContent = t('dc.hint.saved'); loadDevices(); } else document.getElementById('dc-hint').textContent = t('dc.hint.fail'); });
}
function deleteDevice(serial) {
  fetch('/api/device', { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ serial: serial }) })
    .then(function (r) { if (r.ok) loadDevices(); });
}

// ══ ADB & Device Utilities ════════════════════════════════
function killAdbServer() {
  log('song-log', 'Killing ADB server...', 'info');
  fetch('/api/kill-adb', { method: 'POST' })
    .then(function (r) {
      if (r.ok) log('song-log', 'ADB server killed successfully.', 'ok');
      else log('song-log', 'Failed to kill ADB.', 'err');
    })
    .catch(function (e) { log('song-log', 'Network error: ' + e, 'err'); });
}

function autoDetectDevice() {
  var dsInput = document.getElementById('dev-serial');
  dsInput.placeholder = "Detecting...";

  fetch('/api/detect-adb')
    .then(function (r) { return r.json(); })
    .then(function (d) {
      if (d.serial) {
        dsInput.value = d.serial;
        log('song-log', 'Device detected: ' + d.serial, 'ok');
      } else {
        log('song-log', 'No device found.', 'err');
        dsInput.placeholder = "";
      }
    })
    .catch(function (e) {
      log('song-log', 'Failed to auto-detect.', 'err');
      dsInput.placeholder = "";
    });
}


// ══ advanced VTE params ════════════════════════════════════
var ADV_DEFAULTS = { tapDuration: 10, flickDuration: 60, flickReportInterval: 5, slideReportInterval: 10, flickFactor: 20, flickPow: 10 };
function onAdvanced(key) {
  var raw = parseInt(document.getElementById('sld-' + key).value);
  var el = document.getElementById('val-' + key);

  var sld = document.getElementById('sld-' + key);
  var pct = ((raw - (sld.min || 0)) / ((sld.max || 100) - (sld.min || 0))) * 100;
  sld.style.setProperty('--val', pct + '%');

  if (key === 'flickFactor') {
    el.textContent = (raw / 100).toFixed(2);
  } else if (key === 'flickPow') {
    el.textContent = (raw / 10).toFixed(1);
  } else {
    el.textContent = raw;
  }
  el.style.color = 'var(--blue)';
}
function resetAdvanced() {
  Object.keys(ADV_DEFAULTS).forEach(function (key) {
    document.getElementById('sld-' + key).value = ADV_DEFAULTS[key];
    onAdvanced(key);
  });
}
function getAdvancedValues() {
  return {
    tapDuration: parseInt(document.getElementById('sld-tapDuration').value) || 10,
    flickDuration: parseInt(document.getElementById('sld-flickDuration').value) || 60,
    flickReportInterval: parseInt(document.getElementById('sld-flickReportInterval').value) || 5,
    slideReportInterval: parseInt(document.getElementById('sld-slideReportInterval').value) || 10,
    flickFactor: (parseInt(document.getElementById('sld-flickFactor').value) || 20) / 100,
    flickPow: (parseInt(document.getElementById('sld-flickPow').value) || 10) / 10,
  };
}
// ══ extraction ═════════════════════════════════════════════
function doExtract() {
  var p = document.getElementById('ex-path').value.trim();
  if (!p) { log('ex-log', t('log.no.song'), 'err'); return; }
  log('ex-log', t('log.extract.start') + p, 'info');
  fetch('/api/extract', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: p }) })
    .then(function (r) { if (r.ok) log('ex-log', t('log.extract.done'), 'ok'); else r.text().then(function (tx) { log('ex-log', t('log.extract.fail') + tx, 'err'); }); })
    .catch(function (e) { log('ex-log', t('log.conn.fail') + e, 'err'); });
}
// ══ initialization ═════════════════════════════════════════
I18n.init();
initTheme();
applyJacketColor(getDiffThemeColor(diffName(S.diff)));
setBackend(S.backend);
updateDiffLabels();
resetAdvanced();
loadDevices();

// ==========================================
// expose functions to global scope for HTML onclick handlers
// ==========================================
Object.assign(window, {
  I18n,
  toggleLangMenu,
  toggleTheme,
  nav,
  navToSearch,
  killAdbServer,
  autoDetectDevice,
  setMode,
  setBackend,
  setOrient,
  setDiff,
  clearSong,
  onQInput,
  onQKey,
  onQFocus,
  clearQ,
  onManualId,
  submitRun,
  apiStart,
  apiStop,
  adj,
  resetOff,
  saveDevice,
  deleteDevice,
  onJitter,
  onGreatCountInput,
  onAdvanced,
  resetAdvanced,
  doExtract,
  toggleDevDrop,
  loadDevices,
  selectDevSerial,
  selSong
});


// ══ Development mode ════════════════════════════════
if (import.meta.env.DEV) {
  document.addEventListener('keydown', function (e) {
    if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'd') {
      e.preventDefault();

      var mockNp = {
        songId: 999,
        title: 'DEBUG MOCK SONG ~Test Track~',
        artist: 'System Tester',
        diff: 'expert',
        diffLevel: 28,
        jacketUrl: 'https://bestdori.com/assets/jp/musicjacket/musicjacket10_rip/assets-star-forassetbundle-startapp-musicjacket-musicjacket10-10-jacket.png'
      };

      S.songId = 999;
      S.diff = 3;
      S.state = 1; 


      showNP(mockNp);
      updatePlayCard(mockNp);

      updateUI({
        state: 1,
        offset: 0,
        nowPlaying: mockNp
      });

      nav('play');
      log('play-log', 'Debug test song loaded.', 'info');
    }
  });
}