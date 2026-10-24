// search.js is the documentation site's search box. docgen writes it with
// canopySearchIndex, every page's title, group, headings, and text, defined
// above this script. Without JavaScript the box stays hidden.
(() => {
  const box = document.getElementById('search');
  const input = document.getElementById('search-input');
  const list = document.getElementById('search-results');
  if (!box || !input || !list) return;
  box.hidden = false;

  const pages = canopySearchIndex.map((p) => ({
    ...p,
    lt: p.t.toLowerCase(),
    lx: p.x.toLowerCase(),
    lh: (p.h || []).map((h) => h.t.toLowerCase()),
  }));
  let results = [];
  let active = -1;

  // A page matches when every term is in its title, a heading, or its text;
  // title matches rank first, then headings, then text.
  function search(query) {
    const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length) return [];
    const out = [];
    for (const p of pages) {
      let score = 0;
      const matched = terms.every((t) => {
        const s = (p.lt.includes(t) ? 10 : 0) + (p.lh.some((h) => h.includes(t)) ? 4 : 0) + (p.lx.includes(t) ? 1 : 0);
        score += s;
        return s > 0;
      });
      if (!matched) continue;
      // Link straight to the first section whose heading has every term.
      const hi = p.lh.findIndex((h) => terms.every((t) => h.includes(t)));
      const section = hi >= 0 ? p.h[hi] : null;
      out.push({
        score,
        title: section ? `${p.t} › ${section.t}` : p.t,
        group: p.g,
        url: section ? `${p.u}#${section.i}` : p.u,
        snippet: snippet(p, terms[0]),
      });
    }
    return out.sort((a, b) => b.score - a.score).slice(0, 8);
  }

  function snippet(p, term) {
    const i = p.lx.indexOf(term);
    if (i < 0) return '';
    const start = Math.max(0, i - 40);
    return `${start ? '…' : ''}${p.x.slice(start, i + 90)}…`;
  }

  // show renders the results with textContent only: the index is our own,
  // but nothing in it is ever treated as HTML.
  function show() {
    list.replaceChildren();
    const query = input.value.trim();
    results.forEach((r, i) => {
      const li = document.createElement('li');
      li.id = `search-result-${i}`;
      li.setAttribute('role', 'option');
      li.setAttribute('aria-selected', String(i === active));
      const a = document.createElement('a');
      a.href = r.url;
      for (const [cls, text] of [['sr-group', r.group], ['sr-title', r.title], ['sr-snippet', r.snippet]]) {
        if (!text) continue;
        const span = document.createElement('span');
        span.className = cls;
        span.textContent = text;
        a.append(span);
      }
      li.append(a);
      list.append(li);
    });
    if (query && !results.length) {
      const li = document.createElement('li');
      li.className = 'sr-empty';
      li.textContent = `No pages match “${query}”.`;
      list.append(li);
    }
    list.hidden = !query;
    input.setAttribute('aria-expanded', String(!list.hidden));
    if (active >= 0) input.setAttribute('aria-activedescendant', `search-result-${active}`);
    else input.removeAttribute('aria-activedescendant');
  }

  input.addEventListener('input', () => {
    results = search(input.value);
    active = results.length ? 0 : -1;
    show();
  });
  input.addEventListener('keydown', (e) => {
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && results.length) {
      e.preventDefault();
      active = (active + (e.key === 'ArrowDown' ? 1 : -1) + results.length) % results.length;
      show();
    } else if (e.key === 'Enter' && active >= 0) {
      e.preventDefault();
      location.href = results[active].url;
    } else if (e.key === 'Escape') {
      input.value = '';
      results = [];
      active = -1;
      show();
      input.blur();
    }
  });
  input.addEventListener('focus', () => { if (input.value.trim()) show(); });
  document.addEventListener('click', (e) => {
    if (!box.contains(e.target)) {
      list.hidden = true;
      input.setAttribute('aria-expanded', 'false');
    }
  });
  // "/" focuses the search, as on GitHub, unless typing somewhere already.
  document.addEventListener('keydown', (e) => {
    const el = document.activeElement;
    if (e.key === '/' && el !== input && !['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName)) {
      e.preventDefault();
      input.focus();
    }
  });
})();
