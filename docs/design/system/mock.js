/* Gonema mockups: component templates shared by the screens and the system sheet.
   The markup here IS the reference structure for the real components (phase 7):
   a poster card is a link (whole poster) + sibling controls, never controls nested in a link. */
(function () {
  'use strict';
  window.GN = window.GN || {};
  const ic = (n, c) => '<svg class="i ' + (c || '') + '" aria-hidden="true"><use href="#i-' + n + '"/></svg>';

  const STATUS = {
    available: { icon: 'circle-check', label: 'Available' },
    missing: { icon: 'circle-dashed', label: 'Missing' },
    downloading: { icon: 'arrow-down-to-line', label: 'Downloading' },
    failed: { icon: 'triangle-alert', label: 'Failed' },
    upgraded: { icon: 'circle-arrow-up', label: 'Upgraded' },
    unmonitored: { icon: 'eye-off', label: 'Not monitored' },
  };
  GN.STATUS = STATUS;

  function badge(state, opts) {
    const s = STATUS[state]; if (!s) return '';
    return '<span class="badge' + (opts && opts.media ? ' on-media' : '') + (opts && opts.lg ? ' lg' : '') + '" data-status="' + state + '">' + ic(s.icon) + (opts && opts.label || s.label) + '</span>';
  }
  GN.badge = badge;

  function qbadge(q) { // "2160p WEB-DL"
    if (!q) return '';
    const [res, src] = q.split(' ');
    return '<span class="qbadge' + (res === '2160p' ? ' uhd' : '') + '"><b>' + res + '</b>' + (src ? '<span>' + src + '</span>' : '') + '</span>';
  }
  GN.qbadge = qbadge;

  /* card({ film, state, progress, eta, speed, selected, hover, quality, hdr, sub, note }) */
  function card(o) {
    const f = GN.films[o.film] || { title: '', year: '' };
    if (o.state === 'skeleton') {
      return '<div class="pc" data-state="skeleton" aria-hidden="true" style="--shimmer-delay:' + (o.delay || 0) + 'ms"><div class="pc-frame skeleton"></div>' +
        '<div class="pc-meta"><div class="skeleton sk-line" style="width:78%;--shimmer-delay:' + (o.delay || 0) + 'ms"></div><div class="skeleton sk-line" style="width:42%;height:10px;margin-top:6px"></div></div></div>';
    }
    const cls = ['pc'];
    if (o.selected) cls.push('is-selected');
    if (o.hover) cls.push('is-hover');
    if (o.focus) cls.push('is-focus');
    const st = o.state || 'available';
    const onPosterBadge = ['missing', 'failed', 'unmonitored'].includes(st) ? badge(st, { media: true }) : '';
    const label = f.title + ' (' + f.year + ')';

    let progress = '';
    if (st === 'downloading') {
      progress = '<div class="pc-progress"><div class="row"><span>' + Math.round(o.progress * 100) + '%</span><span>' + (o.eta || '') + '</span></div>' +
        '<div class="progress live" role="progressbar" aria-label="Download progress for ' + f.title + '" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' + Math.round(o.progress * 100) + '" style="--p:' + o.progress + '"><div class="fill"></div></div></div>';
    }

    const hoverinfo = '<div class="pc-hoverinfo">' + (o.quality ? qbadge(o.quality) : '') + (o.hdr ? '<span class="hdr-flag">' + o.hdr + '</span>' : '') + '</div>';
    const actions = '<div class="pc-actions">' +
      '<button class="icon-btn on-media" type="button" aria-label="Search for ' + f.title + '" title="Search  S">' + ic('search', 'i-sm') + '</button>' +
      '<button class="icon-btn on-media" type="button" aria-label="' + (st === 'unmonitored' ? 'Monitor ' : 'Stop monitoring ') + f.title + '" aria-pressed="' + (st !== 'unmonitored') + '">' + ic(st === 'unmonitored' ? 'bookmark' : 'bookmark-minus', 'i-sm') + '</button>' +
      '<button class="icon-btn on-media" type="button" aria-label="More actions for ' + f.title + '" aria-haspopup="menu">' + ic('ellipsis', 'i-sm') + '</button></div>';

    let sub = '<span>' + f.year + '</span>';
    const sMeta = STATUS[st];
    if (o.sub) sub += '<span class="sep" aria-hidden="true"></span>' + o.sub;
    else if (st === 'available') sub += '<span class="sep" aria-hidden="true"></span><span class="status" data-status="available">' + ic('circle-check') + '<span class="sr-only">Available, </span>' + (o.quality || '').split(' ')[0] + '</span>';
    else if (st === 'downloading') sub += '<span class="sep" aria-hidden="true"></span><span class="status" data-status="downloading">' + ic('arrow-down-to-line') + (o.speed || 'Downloading') + '</span>';
    else if (st === 'upgraded') sub += '<span class="sep" aria-hidden="true"></span><span class="status" data-status="upgraded">' + ic('circle-arrow-up') + (o.quality || '').split(' ')[0] + '</span>';
    else if (sMeta) sub += '<span class="sep" aria-hidden="true"></span><span class="status" data-status="' + st + '">' + (o.note || sMeta.label) + '</span>';

    return '<div class="' + cls.join(' ') + '" data-state="' + st + '">' +
      '<div class="pc-frame">' +
        '<a class="pc-link" href="movie-detail.html" aria-label="' + label + '"><div class="poster-art" data-film="' + o.film + '"></div></a>' +
        '<div class="pc-shade"></div>' +
        '<div class="pc-top">' + onPosterBadge + '<label class="check on-media pc-select"><input type="checkbox"' + (o.selected ? ' checked' : '') + ' aria-label="Select ' + label + '"></label></div>' +
        progress +
        '<div class="pc-bottom">' + hoverinfo + actions + '</div>' +
      '</div>' +
      '<div class="pc-meta"><div class="pc-title">' + f.title + '</div><div class="pc-sub">' + sub + '</div></div>' +
    '</div>';
  }
  GN.card = card;
  GN.ic = ic;
})();
