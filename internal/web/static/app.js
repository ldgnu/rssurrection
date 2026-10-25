/* RSSurrection — UI y gestos. Cada bloque se inicializa aislado: si uno
   falla, el resto sigue funcionando. */
(function () {
  'use strict';

  function safe(name, fn) {
    try { fn(); } catch (err) {
      if (window.console && console.error) console.error('[rssurrection] ' + name + ':', err);
    }
  }

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  /* ---------- toast ---------- */
  var toastEl = null, toastTimer = 0;
  function toast(msg) {
    try {
      if (!toastEl) {
        toastEl = document.createElement('div');
        toastEl.className = 'toast';
        toastEl.setAttribute('role', 'status');
        document.body.appendChild(toastEl);
      }
      toastEl.textContent = msg;
      toastEl.classList.add('is-visible');
      window.clearTimeout(toastTimer);
      toastTimer = window.setTimeout(function () { toastEl.classList.remove('is-visible'); }, 1800);
    } catch (e) { /* nunca romper por un toast */ }
  }

  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { toast('Link copiado'); },
        function () { toast('No se pudo copiar'); });
      return;
    }
    window.prompt('Copiá el link', text);
  }

  function shareData(data) {
    if (navigator.share) { navigator.share(data).catch(function () {}); return; }
    copyText(data.url);
  }

  /* ---------- hoja de opciones (portada y lector) ---------- */
  safe('sheet', function () {
    var sheet = $('#sheet');
    var fab = $('#actions-btn') || $('#menu-fab');
    var backdrop = $('#sheet-backdrop');
    if (!sheet || !fab) return;

    function open() {
      sheet.classList.add('is-open');
      sheet.setAttribute('aria-hidden', 'false');
      if (backdrop) backdrop.classList.add('is-open');
      fab.setAttribute('aria-expanded', 'true');
    }
    function close() {
      sheet.classList.remove('is-open');
      sheet.setAttribute('aria-hidden', 'true');
      if (backdrop) backdrop.classList.remove('is-open');
      fab.setAttribute('aria-expanded', 'false');
    }

    fab.addEventListener('click', function (e) { e.preventDefault(); open(); });
    if (backdrop) backdrop.addEventListener('click', close);
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });

    // acciones de la hoja del lector
    var key = sheet.getAttribute('data-key');
    var meta = {
      title: sheet.getAttribute('data-title') || document.title,
      link: sheet.getAttribute('data-link') || location.href
    };
    function setFlag(name, on) {
      sheet.setAttribute('data-' + name, on ? '1' : '0');
      var label = sheet.querySelector('[data-label="' + name + '"]');
      if (label) {
        label.textContent = name === 'star'
          ? (on ? 'Quitar de guardados' : 'Guardar')
          : (on ? 'Marcar como no leído' : 'Marcar como leído');
      }
    }

    sheet.addEventListener('click', function (e) {
      var btn = e.target.closest('[data-action]');
      if (!btn) return;
      var action = btn.getAttribute('data-action');

      if (action === 'close') { close(); return; }
      if (action === 'share') { shareData({ title: meta.title, url: meta.link }); close(); return; }
      if (action === 'copy') { copyText(meta.link); close(); return; }

      if (action === 'star' && key) {
        var nextStar = sheet.getAttribute('data-starred') !== '1';
        setFlag('star', nextStar);
        fetch('/api/star', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ key: key, starred: nextStar })
        }).then(function (r) { if (!r.ok) throw new Error(r.status); toast(nextStar ? 'Guardado' : 'Quitado'); })
          .catch(function () { setFlag('star', !nextStar); toast('No se pudo guardar'); });
        return;
      }
      if (action === 'read' && key) {
        var nextRead = sheet.getAttribute('data-read') !== '1';
        setFlag('read', nextRead);
        fetch('/api/read', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ key: key, read: nextRead })
        }).then(function (r) { if (!r.ok) throw new Error(r.status); toast(nextRead ? 'Leído' : 'No leído'); })
          .catch(function () { setFlag('read', !nextRead); toast('No se pudo actualizar'); });
        return;
      }
    });

    // tamaño de letra del lector
    var fontBtns = $$('[data-font]', sheet);
    if (fontBtns.length) {
      var applyFont = function (n) {
        document.body.setAttribute('data-font', n);
        fontBtns.forEach(function (b) {
          b.setAttribute('aria-pressed', b.getAttribute('data-font') === n ? 'true' : 'false');
        });
        try { localStorage.setItem('rssr-font', n); } catch (e) {}
      };
      var saved = '';
      try { saved = localStorage.getItem('rssr-font') || ''; } catch (e) {}
      applyFont(saved || '1');
      fontBtns.forEach(function (b) {
        b.addEventListener('click', function () { applyFont(b.getAttribute('data-font')); });
      });
    }
  });

  /* ---------- tema ---------- */
  safe('theme', function () {
    var btns = Array.prototype.slice.call(document.querySelectorAll('[data-theme-set]'));
    function apply(name) {
      document.documentElement.setAttribute('data-theme', name);
      try { localStorage.setItem('rssr-theme', name); } catch (e) {}
      btns.forEach(function (b) {
        b.setAttribute('aria-pressed', b.getAttribute('data-theme-set') === name ? 'true' : 'false');
      });
    }
    var current = 'auto';
    try { current = localStorage.getItem('rssr-theme') || 'auto'; } catch (e) {}
    if (btns.length) apply(current);
    btns.forEach(function (b) {
      b.addEventListener('click', function () { apply(b.getAttribute('data-theme-set')); });
    });
  });

  /* ---------- actualizar ---------- */
  safe('refresh', function () {
    document.addEventListener('click', function (e) {
      var btn = e.target.closest('[data-action="refresh"]');
      if (!btn) return;
      var ptr = $('#ptr');
      if (ptr) { ptr.classList.add('is-refreshing'); ptr.style.opacity = 1; }
      window.setTimeout(function () { location.reload(); }, 240);
    });
  });

  /* ---------- menú lateral ---------- */
  safe('drawer', function () {
    var d = $('#drawer'), b = $('#sheet-backdrop');
    if (!d) return;
    function open() {
      document.body.classList.add('drawer-open');
      d.setAttribute('aria-hidden', 'false');
      if (b) b.classList.add('is-open');
    }
    function close() {
      document.body.classList.remove('drawer-open');
      d.setAttribute('aria-hidden', 'true');
      if (b) b.classList.remove('is-open');
    }
    if (b) b.addEventListener('click', close);
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });
    window.__rssrDrawer = { open: open, close: close };
  });

  /* ---------- toque largo: me gusta ---------- */
  safe('longpress-like', function () {
    var timer = 0, fired = false, ox = 0, oy = 0, programmatic = false;

    document.addEventListener('touchstart', function (e) {
      var card = e.target.closest('.story-card');
      if (!card || e.touches.length !== 1) return;
      fired = false;
      ox = e.touches[0].clientX;
      oy = e.touches[0].clientY;
      window.clearTimeout(timer);
      timer = window.setTimeout(function () {
        var btn = card.querySelector('.card-like');
        if (!btn) return;
        fired = true;
        programmatic = true;
        btn.click();
      }, 520);
    }, { passive: true });

    // Solo se cancela si el dedo se mueve de verdad (>14px).
    document.addEventListener('touchmove', function (e) {
      if (!timer) return;
      var t = e.touches[0];
      if (!t) return;
      if (Math.abs(t.clientX - ox) > 14 || Math.abs(t.clientY - oy) > 14) {
        window.clearTimeout(timer);
        timer = 0;
      }
    }, { passive: true });
    function cancel() { window.clearTimeout(timer); timer = 0; }
    document.addEventListener('touchend', cancel, { passive: true });
    document.addEventListener('touchcancel', cancel, { passive: true });

    // si el long press disparó, el tap posterior no navega
    document.addEventListener('click', function (e) {
      if (programmatic) { programmatic = false; return; }
      if (!fired) return;
      if (!e.target.closest('.story-link')) return;
      fired = false;
      e.preventDefault();
      e.stopPropagation();
    }, true);
  });

  /* ---------- corazón de tarjeta ---------- */
  safe('like', function () {
    document.addEventListener('click', function (e) {
      var btn = e.target.closest('.card-like');
      if (!btn) return;
      e.preventDefault();
      e.stopPropagation();
      var key = btn.getAttribute('data-key');
      var on = btn.getAttribute('data-starred') === '1';
      var next = !on;
      btn.setAttribute('data-starred', next ? '1' : '0');
      btn.classList.toggle('is-on', next);
      btn.setAttribute('aria-pressed', next ? 'true' : 'false');
      fetch('/api/star', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key: key, starred: next })
      }).then(function (r) { if (!r.ok) throw new Error(r.status); toast(next ? 'Guardado' : 'Quitado'); })
        .catch(function () {
          btn.setAttribute('data-starred', on ? '1' : '0');
          btn.classList.toggle('is-on', on);
          toast('No se pudo guardar');
        });
    });
  });

  /* ---------- marcar leído al abrir ---------- */
  safe('mark-read', function () {
    var reader = $('[data-read-key]');
    if (!reader) return;
    fetch('/api/read', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key: reader.getAttribute('data-read-key'), read: true })
    }).catch(function () {});
  });

  /* ---------- resumen con IA ---------- */
  safe('summary', function () {
    var box = $('#ai-box');
    if (!box) return;
    var text = $('#ai-text');
    fetch('/api/summary', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key: box.getAttribute('data-key') })
    }).then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (d) { if (text && d.summary) text.textContent = d.summary; })
      .catch(function () { box.remove(); });
  });

  /* ---------- imágenes: lazy real (el nativo no dispara en el snap) ---------- */
  safe('lazy-images', function () {
    var observer = null;

    function load(img) {
      var src = img.getAttribute('data-src');
      if (!src) return;
      img.setAttribute('src', src);
      img.removeAttribute('data-src');
    }

    if ('IntersectionObserver' in window) {
      observer = new IntersectionObserver(function (entries) {
        entries.forEach(function (en) {
          if (!en.isIntersecting) return;
          observer.unobserve(en.target);
          load(en.target);
        });
      }, { root: null, rootMargin: '150% 0px 150% 0px', threshold: 0.01 });
    }

    window.__rssrLazy = function (root) {
      var imgs = Array.prototype.slice.call((root || document).querySelectorAll('.lazy-img'));
      imgs.forEach(function (img) {
        if (observer) observer.observe(img);
        else load(img);
      });
    };

    window.__rssrLazy(document);

    // si el observer no existe, cargamos todo al primer scroll
    if (!observer) return;
    window.addEventListener('pagehide', function () {
      Array.prototype.slice.call(document.querySelectorAll('.lazy-img')).forEach(load);
    });
  });

  /* ---------- orden de categorías ---------- */
  safe('cat-order', function () {
    var CAT_KEY = 'rssr-cat-order';
    function order() {
      try {
        var raw = localStorage.getItem(CAT_KEY);
        var arr = raw ? JSON.parse(raw) : [];
        return Array.isArray(arr) ? arr : [];
      } catch (e) { return []; }
    }
    function apply(container, sel) {
      if (!container) return;
      var list = order();
      if (!list.length) return;
      var items = $$(sel, container);
      items.sort(function (a, b) {
        var ia = list.indexOf(a.getAttribute('data-cat') || a.getAttribute('data-slug'));
        var ib = list.indexOf(b.getAttribute('data-cat') || b.getAttribute('data-slug'));
        if (ia < 0) ia = 999;
        if (ib < 0) ib = 999;
        return ia - ib;
      });
      items.forEach(function (el) { container.appendChild(el); });
    }
    apply($('#sheet'), '.cat-order');
    apply($('#cat-list'), '.cat-row');

    var catList = $('#cat-list');
    if (catList) {
      // Arrastrar y soltar: se mantiene apretado y se mueve donde quieras.
      var holdTimer = 0, dragging = null, startY = 0;
      function persist() {
        var out = $$('.cat-row', catList).map(function (r) { return r.getAttribute('data-slug'); });
        try { localStorage.setItem(CAT_KEY, JSON.stringify(out)); } catch (e) {}
      }

      catList.addEventListener('pointerdown', function (e) {
        var row = e.target.closest('.cat-row');
        if (!row) return;
        startY = e.clientY;
        window.clearTimeout(holdTimer);
        holdTimer = window.setTimeout(function () {
          dragging = row;
          row.classList.add('is-dragging');
          if (row.setPointerCapture && e.pointerId !== undefined) {
            try { row.setPointerCapture(e.pointerId); } catch (err) {}
          }
          if (navigator.vibrate) navigator.vibrate(8);
        }, 160);
      });

      catList.addEventListener('pointermove', function (e) {
        if (!dragging) {
          if (Math.abs(e.clientY - startY) > 10) { window.clearTimeout(holdTimer); }
          return;
        }
        e.preventDefault();
        var rows = $$('.cat-row', catList).filter(function (r) { return r !== dragging; });
        var after = null;
        for (var i = 0; i < rows.length; i++) {
          var box = rows[i].getBoundingClientRect();
          if (e.clientY < box.top + box.height / 2) { after = rows[i]; break; }
        }
        if (after) catList.insertBefore(dragging, after);
        else catList.appendChild(dragging);
      }, { passive: false });

      function drop() {
        window.clearTimeout(holdTimer);
        if (dragging) {
          dragging.classList.remove('is-dragging');
          dragging = null;
          persist();
          toast('Orden guardado');
        }
      }
      // Sin esto, en iOS el scroll se queda con el gesto y el arrastre no mueve.
      catList.addEventListener('touchmove', function (e) {
        if (dragging && e.cancelable) e.preventDefault();
      }, { passive: false });

      catList.addEventListener('pointerup', drop);
      catList.addEventListener('pointercancel', drop);
      catList.addEventListener('pointerleave', drop);

      catList.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-move]');
        if (!btn) return;
        var row = btn.closest('.cat-row');
        if (!row) return;
        if (btn.getAttribute('data-move') === 'up' && row.previousElementSibling) {
          catList.insertBefore(row, row.previousElementSibling);
        } else if (btn.getAttribute('data-move') === 'down' && row.nextElementSibling) {
          catList.insertBefore(row.nextElementSibling, row);
        }
        var out = $$('.cat-row', catList).map(function (r) { return r.getAttribute('data-slug'); });
        try { localStorage.setItem(CAT_KEY, JSON.stringify(out)); } catch (e) {}
      });
    }
  });

  /* ---------- mosaico: arrastrar proporción ---------- */
  safe('fib', function () {
    var KEY = 'rssr-fib', MIN = 0.34, MAX = 0.76;
    function read() {
      try {
        var v = parseFloat(localStorage.getItem(KEY) || '');
        if (isFinite(v) && v >= MIN && v <= MAX) return v;
      } catch (e) {}
      return 0.618;
    }
    function write(v) {
      document.body.style.setProperty('--fib', String(v));
      try { localStorage.setItem(KEY, String(v)); } catch (e) {}
    }
    if (!$('.fib-page')) return;
    write(read());

    document.addEventListener('pointerdown', function (e) {
      var grip = e.target.closest('.fib-grip');
      if (!grip) return;
      var page = grip.closest('.fib-page');
      if (!page) return;
      e.preventDefault();
      var rect = page.getBoundingClientRect();
      grip.classList.add('is-dragging');
      function move(ev) {
        var ratio = Math.min(MAX, Math.max(MIN, (ev.clientX - rect.left) / rect.width));
        document.body.style.setProperty('--fib', ratio.toFixed(3));
      }
      function up() {
        grip.classList.remove('is-dragging');
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', up);
        window.removeEventListener('pointercancel', up);
        var v = parseFloat(getComputedStyle(document.body).getPropertyValue('--fib'));
        if (isFinite(v)) write(v);
      }
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', up);
      window.addEventListener('pointercancel', up);
    }, { passive: false });
  });

  /* ---------- compartir del lector ---------- */
  safe('share', function () {
    var btn = $('#share-btn');
    if (!btn) return;
    btn.addEventListener('click', function () {
      shareData({
        title: btn.getAttribute('data-title') || document.title,
        url: btn.getAttribute('data-url') || location.href
      });
    });
  });

  /* ---------- stream: swipe, infinito, tirón ---------- */
  safe('stream', function () {
    var stream = $('#stream');
    if (!stream) return;

    var loading = false;

    function cards() { return $$('.story-card:not(.story-end)', stream); }
    function current() {
      return stream.clientHeight ? Math.round(stream.scrollTop / stream.clientHeight) : 0;
    }
    function goTo(i) {
      stream.scrollTo({ top: i * stream.clientHeight, behavior: 'smooth' });
    }

    document.addEventListener('keydown', function (e) {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === 'ArrowDown' || e.key === 'PageDown' || e.key === ' ') { e.preventDefault(); goTo(current() + 1); }
      if (e.key === 'ArrowUp' || e.key === 'PageUp') { e.preventDefault(); goTo(current() - 1); }
    });

    function loadMore() {
      var cont = stream.getAttribute('data-next');
      if (!cont || loading) return;
      loading = true;
      var slug = stream.getAttribute('data-category-slug') || '';
      var mosaic = stream.getAttribute('data-view') === 'mosaic';
      var url = '/api/feed?c=' + encodeURIComponent(cont)
        + (slug ? '&cat=' + encodeURIComponent(slug) : '')
        + (mosaic ? '&v=mosaic' : '');
      fetch(url, { headers: { 'Accept': 'text/html' } }).then(function (r) {
        if (!r.ok) throw new Error(r.status);
        var next = r.headers.get('X-Next-Continuation') || '';
        return r.text().then(function (html) { return { html: html, next: next }; });
      }).then(function (res) {
        var holder = document.createElement('div');
        holder.innerHTML = res.html;
        var end = $('#stream-end');
        while (holder.firstElementChild) {
          var node = holder.firstElementChild;
          holder.removeChild(node);
          if (end) stream.insertBefore(node, end); else stream.appendChild(node);
        }
        stream.setAttribute('data-next', res.next);
        if (!res.next && end) end.remove();
        if (window.__rssrLazy) window.__rssrLazy(stream);
      }).catch(function () { toast('No se pudieron cargar más noticias'); })
        .then(function () { loading = false; });
    }

    stream.addEventListener('scroll', function () {
      if (stream.scrollTop + stream.clientHeight * 2.5 >= stream.scrollHeight) loadMore();
    }, { passive: true });

    // swipe horizontal → menú de secciones / abrir nota.
    // Pointer events + touch-action: pan-y: iOS entrega el gesto horizontal
    // sin cancelarlo, y el scroll vertical sigue siendo nativo.
    var sx = 0, sy = 0, lastX = 0, lastY = 0, tracking = false, suppressClickUntil = 0;

    function startSwipe(e) {
      if (e.pointerType === 'mouse') return;
      if (e.isPrimary === false) return;
      tracking = true;
      sx = lastX = e.clientX;
      sy = lastY = e.clientY;
    }

    function moveSwipe(e) {
      if (!tracking) return;
      lastX = e.clientX;
      lastY = e.clientY;
    }

    // finish(null) se usa cuando el navegador cancela el puntero: en iOS y en
    // Chrome muchas veces llega pointercancel en lugar de pointerup, así que
    // resolvemos con la última posición conocida.
    function finish(e) {
      if (!tracking) return;
      tracking = false;
      var x = e ? e.clientX : lastX;
      var y = e ? e.clientY : lastY;
      var dx = x - sx;
      var dy = y - sy;
      if (Math.abs(dx) < 55 || Math.abs(dx) < Math.abs(dy) * 1.15) return;
      if (dx > 0 && sx < 28) return; // borde izquierdo = "atrás" del sistema
      suppressClickUntil = Date.now() + 500;

      var drawer = window.__rssrDrawer;
      var open = document.body.classList.contains('drawer-open');
      if (dx < 0) {
        if (open) { if (drawer) drawer.close(); } else if (drawer) { drawer.open(); }
        return;
      }
      if (open) { if (drawer) drawer.close(); return; }

      var list = cards();
      var card = list[Math.max(0, Math.min(list.length - 1, current()))];
      var link = card && card.querySelector('.story-link');
      if (link && link.getAttribute('href')) window.location.assign(link.getAttribute('href'));
    }

    document.addEventListener('click', function (e) {
      if (Date.now() > suppressClickUntil) return;
      if (e.target.closest('.qa')) return;
      e.preventDefault();
      e.stopPropagation();
    }, true);

    document.addEventListener('pointerdown', startSwipe, { passive: true });
    document.addEventListener('pointermove', moveSwipe, { passive: true });
    document.addEventListener('pointerup', finish, { passive: true });
    document.addEventListener('pointercancel', function () { finish(null); }, { passive: true });
  });

  /* ---------- tirar del diario ---------- */
  safe('pull', function () {
    var stream = $('#stream');
    var zone = $('#ptr-zone');
    var ptr = $('#ptr');
    if (!stream || !zone || !ptr) return;

    var startY = 0, startX = 0, dist = 0, active = false, done = false;
    var THRESHOLD = 70, MAX = 110;

    function label(v) {
      var el = $('.ptr-label', ptr);
      if (el) el.textContent = v >= THRESHOLD ? 'Soltá para actualizar' : 'Tirá para actualizar';
    }
    function reset() {
      active = false;
      dist = 0;
      stream.classList.remove('is-pulling');
      stream.style.transform = '';
      ptr.style.opacity = 0;
    }

    function begin(e) {
      if (done || e.touches.length !== 1) return;
      if (stream.scrollTop > 0) return;
      active = true;
      startY = e.touches[0].clientY;
      startX = e.touches[0].clientX;
    }

    zone.addEventListener('touchstart', begin, { passive: true });
    stream.addEventListener('touchstart', begin, { passive: true });

    function moveHandler(e) {
      if (!active || done) return;
      var dy = e.touches[0].clientY - startY;
      var dx = e.touches[0].clientX - startX;
      if (dy <= 0) return;
      if (Math.abs(dx) > Math.abs(dy) * 1.2) { reset(); return; }
      if (e.cancelable) e.preventDefault();
      stream.classList.add('is-pulling');
      dist = Math.min(Math.pow(dy, 0.86) * 2.1, MAX);
      stream.style.transform = 'translateY(' + dist.toFixed(1) + 'px)';
      ptr.style.opacity = Math.min(dist / 52, 1);
      label(dist);
    }
    zone.addEventListener('touchmove', moveHandler, { passive: false });
    stream.addEventListener('touchmove', moveHandler, { passive: false });

    function endHandler() {
      if (!active || done) return;
      if (dist < THRESHOLD) { reset(); return; }
      done = true;
      ptr.classList.add('is-refreshing');
      ptr.style.opacity = 1;
      label(999);
      stream.classList.remove('is-pulling');
      stream.style.transform = 'translateY(60px)';
      window.setTimeout(function () { location.reload(); }, 420);
    }
    zone.addEventListener('touchend', endHandler, { passive: true });
    stream.addEventListener('touchend', endHandler, { passive: true });
    zone.addEventListener('touchcancel', reset, { passive: true });
    stream.addEventListener('touchcancel', reset, { passive: true });
  });
  safe('service-worker', function () {
    if (!('serviceWorker' in navigator)) return;
    navigator.serviceWorker.register('/sw.js').catch(function () {});
  });

  document.documentElement.setAttribute('data-js', 'ok');
})();
