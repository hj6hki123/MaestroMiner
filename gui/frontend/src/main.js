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
        document.getElementById('lb-flag').textContent = d['lang.flag'] || '🌐';
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
          var d = langs[m.id] || {};
          return '<div class="lang-opt" data-lang="' + m.id + '" onclick="I18n.select(\'' + m.id + '\')">'
            + '<span class="lo-flag">' + (d['lang.flag'] || '🌐') + '</span>'
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

    function renderJitter(key) {
      var raw = parseInt(document.getElementById('sld-' + key).value);
      var el = document.getElementById('val-' + key);
      if (raw === 0) { el.textContent = 'OFF'; el.style.color = 'var(--hint)'; return; }
      el.style.color = 'var(--blue)';
      el.textContent = key === 'position' ? ('±' + Math.round((JITTER_POS_MAP[raw] || 0) * 100) + '%') : ('±' + raw + ' ms');
    }

    function renderAllJitters() { ['timing', 'position', 'tapDur'].forEach(renderJitter); }
    function onJitter(key) { renderJitter(key); }

    // ══ state ══════════════════════════════════════════════════
    var S = { backend: 'hid', diff: 3, orient: 'left', mode: 'bang', state: 0, offset: 0, songId: 0, songData: null, db: null, dropIdx: -1, _lastLogState: -1 };
    var DN = ['easy', 'normal', 'hard', 'expert', 'special'];
    var DL_BANG = ['EASY', 'NORMAL', 'HARD', 'EXPERT', 'SPECIAL'];
    var DOT_CLS = { 1: 'ready', 2: 'playing', 3: 'done', 4: 'error' };

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

    function setMode(m) {
      S.mode = m; S.db = null; if (S.songId) clearSong();

      // 新增：更新 UI 畫面的 active 狀態
      ['bang', 'pjsk'].forEach(function (x) {
        document.getElementById('mode-' + x).classList.toggle('active', x === m);
      });

      if (m === 'pjsk') {
        ADV_DEFAULTS.flickDuration = 20; ADV_DEFAULTS.flickFactor = 17;
      } else {
        ADV_DEFAULTS.flickDuration = 60; ADV_DEFAULTS.flickFactor = 20;
      }
      resetAdvanced();
    }
    function setBackend(b) {
      S.backend = b;
      ['hid', 'adb'].forEach(function (x) { document.getElementById('backend-' + x).classList.toggle('active', x === b); });
      document.getElementById('orient-wrap').style.opacity = b === 'adb' ? '0.4' : '1';
    }
    function setOrient(o) {
      S.orient = o;
      document.getElementById('ol').classList.toggle('active', o === 'left');
      document.getElementById('or').classList.toggle('active', o === 'right');
    }
    function setDiff(i) { S.diff = i; document.querySelectorAll('.db').forEach(function (b, j) { b.classList.toggle('active', j === i); }); }
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
        .then(function (d) { S.db = d; cb(d); })
        .catch(function (e) { log('song-log', t('log.conn.fail') + e, 'err'); });
    }

    function pickName(arr) { if (!arr) return ''; return arr[2] || arr[1] || arr[0] || arr[3] || arr[4] || ''; }
    function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }

    function doSearch(q) {
      loadDB(function (db) {
        var ql = q.toLowerCase(), res = [];
        Object.keys(db.songs).forEach(function (sid) {
          var id = parseInt(sid), song = db.songs[sid];
          if (!song || !song.musicTitle) return;
          if (!song.musicTitle.some(function (n) { return n && n.toLowerCase().indexOf(ql) >= 0; })) return;
          var band = db.bands[song.bandId];
          res.push({ id: id, song: song, band: band && band.bandName ? pickName(band.bandName) : '' });
        });
        res.sort(function (a, b) {
          var at = pickName(a.song.musicTitle).toLowerCase(), bt = pickName(b.song.musicTitle).toLowerCase();
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
        var title = pickName(r.song.musicTitle);
        var dh = Object.keys(r.song.difficulty || {}).map(Number).sort().map(function (d) { return '<span class="di-d d-' + DN[d] + '">' + DL_BANG[d] + '</span>'; }).join('');
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
        var title = pickName(song.musicTitle);
        document.getElementById('sb-id').textContent = '#' + id;
        document.getElementById('sb-title').textContent = title;
        document.getElementById('sel-bar').classList.add('show');
        document.getElementById('q').value = ''; document.getElementById('sc').style.display = 'none';
        document.getElementById('song-id').value = id; closeDrop();
        setDiffAvail(Object.keys(song.difficulty || {}).map(Number).sort());
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
      document.getElementById('np-dot').className = 'dot ' + dotCls;
      document.getElementById('pn-dot').className = 'dot ' + dotCls;
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
    }

    function showNP(np) {
      document.getElementById('np-card').style.display = 'block';
      if (np.jacketUrl) { var img = document.getElementById('np-img'); img.src = np.jacketUrl; img.style.display = 'block'; document.getElementById('np-no').style.display = 'none'; }
      document.getElementById('np-title').textContent = np.title || '—';
      document.getElementById('np-artist').textContent = np.artist || '';
      var db = document.getElementById('np-diff'); db.className = 'np-diff d-' + (np.diff || 'expert'); db.textContent = (np.diff || '').toUpperCase();
      document.getElementById('np-lv').textContent = np.diffLevel ? 'Lv.' + np.diffLevel : '';
    }
    function updatePlayCard(np) {
      document.getElementById('pn-none').style.display = 'none'; document.getElementById('pn-loaded').style.display = 'block';
      var pimg = document.getElementById('pn-img');
      if (np.jacketUrl) { pimg.src = np.jacketUrl; pimg.style.display = 'block'; document.getElementById('pn-no').style.display = 'none'; }
      document.getElementById('pn-title-big').textContent = np.title || '—';
      document.getElementById('pn-artist-big').textContent = np.artist || '';
      var badge = document.getElementById('pn-diff-badge'); badge.className = 'np-diff d-' + (np.diff || 'expert'); badge.textContent = (np.diff || '').toUpperCase();
      document.getElementById('pn-lv-big').textContent = np.diffLevel ? 'Lv.' + np.diffLevel : '';
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
      var np = { songId: S.songId, diff: DN[S.diff], mode: S.mode, title: '', artist: '', diffLevel: 0, jacketUrl: '' };
      if (S.songData) {
        np.title = pickName(S.songData.musicTitle) || '';
        var di = S.songData.difficulty; if (di && di[S.diff]) np.diffLevel = di[S.diff].playLevel || 0;
        var ji = S.songData.jacketImage;
        if (ji && ji[0]) { var n = Math.ceil(S.songId / 10) * 10 || 10; np.jacketUrl = 'https://bestdori.com/assets/jp/musicjacket/musicjacket' + n + '_rip/assets-star-forassetbundle-startapp-musicjacket-musicjacket' + n + '-' + ji[0] + '-jacket.png'; }
        if (S.db && S.db.bands && S.songData.bandId) { var band = S.db.bands[S.songData.bandId]; if (band && band.bandName) np.artist = pickName(band.bandName); }
      }
      return np;
    }

    function submitRun() {
      var sid = parseInt(document.getElementById('song-id').value) || S.songId || 0;
      var cp = document.getElementById('chart-path').value.trim();
      var ds = document.getElementById('dev-serial').value.trim();
      if (!sid && !cp) { log('song-log', t('log.no.song'), 'err'); return; }
      var tRaw = parseInt(document.getElementById('sld-timing').value) || 0;
      var pRaw = parseInt(document.getElementById('sld-position').value) || 0;
      var dRaw = parseInt(document.getElementById('sld-tapDur').value) || 0;
      var adv = getAdvancedValues();
      var body = { mode: S.mode, backend: S.backend, diff: DN[S.diff], orient: S.orient, songId: sid, chartPath: cp, deviceSerial: ds, nowPlaying: buildNowPlaying(), timingJitter: tRaw, positionJitter: jitterRealValue('position', pRaw), tapDurJitter: dRaw, tapDuration: adv.tapDuration, flickDuration: adv.flickDuration, flickReportInterval: adv.flickReportInterval, slideReportInterval: adv.slideReportInterval, flickFactor: adv.flickFactor, flickPow: adv.flickPow };
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
    // ══ advanced VTE params ════════════════════════════════════
    var ADV_DEFAULTS = { tapDuration: 10, flickDuration: 60, flickReportInterval: 5, slideReportInterval: 10, flickFactor: 20, flickPow: 10 };
    function onAdvanced(key) {
      var raw = parseInt(document.getElementById('sld-' + key).value);
      var el = document.getElementById('val-' + key);
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



// ==========================================
// expose functions to global scope for HTML onclick handlers
// ==========================================
Object.assign(window, {
  I18n,
  toggleLangMenu,
  nav,
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
  onAdvanced,
  resetAdvanced,
  doExtract,
  toggleDevDrop,
  selectDevSerial,
  selSong 
});