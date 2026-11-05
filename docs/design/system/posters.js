/* Gonema mockups: generative poster & backdrop art.
   Every film here is FICTIONAL. Posters are drawn in SVG (gradients, shapes, feTurbulence grain,
   display type) so the mockups look like a real library without using copyrighted artwork.
   In the product, these slots are filled by cached TMDB images; `dom` stands in for
   movies.dominant_color (see docs/data-model.md), the source of --movie-tint.

   Usage:
     <div class="poster-art" data-film="halcyon-drift"></div>      -> poster (2:3)
     <div class="backdrop-art" data-film="halcyon-drift"></div>    -> backdrop (16:9, no title)
   GN.films[id] exposes the metadata. */
(function () {
  'use strict';

  // pal: [sky top, sky bottom, ink (title / light), highlight]
  const films = {
    'halcyon-drift':        { title: 'Halcyon Drift', year: 2024, lines: ['HALCYON', 'DRIFT'], font: 'x', pos: 'top', motif: 'sun', pal: ['#1d1426', '#b8643c', '#f7e6d0', '#f2a65a'], dom: '#B8643C', credit: 'KESTREL PICTURES · A FILM BY INES CALLOWAY' },
    'last-projectionist':   { title: 'The Last Projectionist', year: 2022, lines: ['The Last', 'Projectionist'], font: 's', pos: 'bottom', motif: 'lighthouse', pal: ['#0e1420', '#26344a', '#f3e9d8', '#f6d98b'], dom: '#3A4C6B', credit: 'BOBINE FILMS · ODILE MARCHETTI' },
    'quiet-meridian':       { title: 'The Quiet Meridian', year: 2019, lines: ['THE QUIET', 'MERIDIAN'], font: 'c', pos: 'bottom', motif: 'orbit', pal: ['#0b1a1f', '#174a52', '#e8f1ee', '#8fd3c8'], dom: '#1F5A60', credit: 'NORTHLIGHT · TOMAS REINHOLT' },
    'saltwater-hymn':       { title: 'Saltwater Hymn', year: 2021, lines: ['Saltwater', 'Hymn'], font: 's', pos: 'top', motif: 'waves', pal: ['#e9dccb', '#6f8fa3', '#14202a', '#2d5870'], dom: '#5E7F94', credit: 'HARBOUR & SONS · MAREN OKAFOR' },
    'neon-orchard':         { title: 'Neon Orchard', year: 2023, lines: ['NEON', 'ORCHARD'], font: 'x', pos: 'bottom', motif: 'orchid', pal: ['#170b1e', '#4a1240', '#ffe7f3', '#ff6fb1'], dom: '#7A1F5C', credit: 'VELOUR · DIR. KAITO VANCE' },
    'ninefold':             { title: 'Ninefold', year: 2020, lines: ['NINEFOLD'], font: 'c', pos: 'top', motif: 'stripes', pal: ['#f0e6d6', '#e3d3bc', '#16120f', '#c4402f'], dom: '#C4402F', credit: 'A RUNE ADEYEMI FILM' },
    'room-without-weather': { title: 'A Room Without Weather', year: 2018, lines: ['A Room', 'Without', 'Weather'], font: 'g', pos: 'top', motif: 'arch', pal: ['#2a2622', '#0f0d0c', '#efe6d9', '#d9b98a'], dom: '#5A4A3A', credit: 'SALLE NOIRE · CLÉMENCE ARDOIN' },
    'coldharbour':          { title: 'Coldharbour', year: 2016, lines: ['COLDHARBOUR'], font: 'x', pos: 'bottom', motif: 'city', pal: ['#0d1117', '#24303f', '#e6edf3', '#e9b44c'], dom: '#2C3A4C', credit: 'GREYWATER · JONAH STRAND' },
    'velvet-static':        { title: 'Velvet Static', year: 2025, lines: ['Velvet', 'Static'], font: 's', pos: 'center', motif: 'signal', pal: ['#1a0f0f', '#5b1a1a', '#f6e4dc', '#ff8a6b'], dom: '#7B2A24', credit: 'ROSEWOOD · AMARA DELACROIX' },
    'glass-cartographer':   { title: 'The Glass Cartographer', year: 2021, lines: ['THE GLASS', 'CARTOGRAPHER'], font: 'x', pos: 'top', motif: 'grid', pal: ['#081018', '#13314a', '#e4f0ff', '#7cc4ff'], dom: '#1E4566', credit: 'MERIDIAN WORKS · YUSUF HALVORSEN' },
    'sundial':              { title: 'Sundial', year: 2015, lines: ['Sundial'], font: 's', pos: 'bottom', motif: 'split', pal: ['#f2c14e', '#e8833a', '#1b120b', '#1b120b'], dom: '#E8833A', credit: 'GOLDEN HOUR · PIA LINDQVIST' },
    'ember-ash':            { title: 'Ember & Ash Street', year: 2019, lines: ['EMBER &', 'ASH STREET'], font: 'c', pos: 'top', motif: 'lanterns', pal: ['#120a07', '#3d1a0e', '#ffe9d6', '#ff9a3c'], dom: '#5C2A14', credit: 'CINDER FILMS · MATEO QUILL' },
    'orbit-small-things':   { title: 'Orbit of Small Things', year: 2023, lines: ['orbit of', 'small things'], font: 'g', pos: 'bottom', motif: 'moon', pal: ['#0c0f1f', '#2b2f5a', '#eef0ff', '#c9ccff'], dom: '#2E3266', credit: 'PALE BLUE · NORA ESPINOZA' },
    'tidewater':            { title: 'Tidewater', year: 2014, lines: ['TIDEWATER'], font: 'x', pos: 'center', motif: 'figure', pal: ['#c7d3d4', '#4a6670', '#0f1a1e', '#f4efe6'], dom: '#4A6670', credit: 'LOWLAND · HENRIK SAAL' },
    'odeon-vale':           { title: 'Midnight at the Odeon Vale', year: 2012, lines: ['Midnight at the', 'Odeon Vale'], font: 's', pos: 'top', motif: 'marquee', pal: ['#14080c', '#3a0f1c', '#fbe8c8', '#f5c46a'], dom: '#5A1A2A', credit: 'PROJECTOR ROOM · ELSA MORROW' },
    'orchid-protocol':      { title: 'The Orchid Protocol', year: 2022, lines: ['THE ORCHID', 'PROTOCOL'], font: 'c', pos: 'bottom', motif: 'monolith', pal: ['#0a1110', '#1f3b2f', '#e6f4ea', '#7fe0a6'], dom: '#2A5040', credit: 'VANTAGE · DIR. SAMIR OKONJO' },
    'low-tide-high-noon':   { title: 'Low Tide, High Noon', year: 2020, lines: ['LOW TIDE,', 'HIGH NOON'], font: 'x', pos: 'top', motif: 'sun', pal: ['#f4dcb0', '#d9784a', '#2a140b', '#fff3d6'], dom: '#D9784A', credit: 'DUSTBOWL · CARMEN ROJAS-WELL' },
    'parallax':             { title: 'Parallax', year: 2024, lines: ['PARALLAX'], font: 'c', pos: 'center', motif: 'grid', pal: ['#10061c', '#2c0f4a', '#f5ecff', '#c08bff'], dom: '#3D1766', credit: 'HALFLIGHT · AUGUST BRENNER' },
    'wolves-lower-sky':     { title: 'Wolves of the Lower Sky', year: 2018, lines: ['Wolves of the', 'Lower Sky'], font: 's', pos: 'bottom', motif: 'moon', pal: ['#0e1214', '#3b4a4f', '#f0ece4', '#e9e2d0'], dom: '#3B4A4F', credit: 'IRONWOOD · TALIA FENWICK' },
    'marrow':               { title: 'Marrow', year: 2025, lines: ['MARROW'], font: 'x', pos: 'bottom', motif: 'figure', pal: ['#2b0d0d', '#0d0505', '#f2dcd2', '#c7352b'], dom: '#5A1A16', credit: 'BLACK SHEEP · OREN VASK' },
    'second-sunrise':       { title: 'Second Sunrise', year: 2026, lines: ['Second', 'Sunrise'], font: 'g', pos: 'top', motif: 'split', pal: ['#ffd9c2', '#ff9e7a', '#2a1410', '#ff5e3a'], dom: '#FF9E7A', credit: 'COASTLINE · JUNE ABERNATHY' },
    'lighthouse-daughter':  { title: "The Lighthouse Keeper's Daughter", year: 2013, lines: ["The Lighthouse", "Keeper's Daughter"], font: 's', pos: 'top', motif: 'lighthouse', pal: ['#1a2430', '#5d7a8c', '#f6efe3', '#ffe8a8'], dom: '#4C6577', credit: 'GULL ISLAND · ROSALIND PRY' },
    'fathom':               { title: 'Fathom', year: 2021, lines: ['FATHOM'], font: 'x', pos: 'top', motif: 'waves', pal: ['#04121a', '#0a3a4a', '#d9f6ff', '#3fd0e0'], dom: '#0A3A4A', credit: 'DEEPWATER · KOFI ANAND' },
    'concrete-psalms':      { title: 'Concrete Psalms', year: 2017, lines: ['CONCRETE', 'PSALMS'], font: 'c', pos: 'bottom', motif: 'halftone', pal: ['#d6d0c4', '#a39b8c', '#14110e', '#14110e'], dom: '#A39B8C', credit: 'BRUTAL GROUP · LEV OSTROVSKY' },
    'ghost-frequency':      { title: 'Ghost Frequency', year: 2023, lines: ['ghost', 'frequency'], font: 'g', pos: 'center', motif: 'signal', pal: ['#06100e', '#0f2b26', '#d8fff4', '#5cf2c8'], dom: '#0F2B26', credit: 'STATIC HOUSE · INGRID MAEL' },
    'atlas-undone':         { title: 'Atlas Undone', year: 2026, lines: ['ATLAS', 'UNDONE'], font: 'x', pos: 'bottom', motif: 'orbit', pal: ['#15100a', '#4a3518', '#fff1d6', '#f0b450'], dom: '#4A3518', credit: 'PRIME MERIDIAN · DARIUS KELLE' },
    'kingdom-lanterns':     { title: 'Kingdom of Lanterns', year: 2016, lines: ['Kingdom of', 'Lanterns'], font: 's', pos: 'bottom', motif: 'lanterns', pal: ['#0b0a1a', '#271a45', '#fff4dc', '#ffc35c'], dom: '#271A45', credit: 'PAPERMOON · HANA VOSS' },
    'drift-signal':         { title: 'Drift: Signal', year: 2019, lines: ['DRIFT', 'SIGNAL'], font: 'x', pos: 'top', motif: 'signal', pal: ['#1a1020', '#7a3a2a', '#f7e6d0', '#f2a65a'], dom: '#7A3A2A', credit: 'KESTREL PICTURES · INES CALLOWAY' },
    'drift-terminus':       { title: 'Drift: Terminus', year: 2027, lines: ['DRIFT', 'TERMINUS'], font: 'x', pos: 'bottom', motif: 'monolith', pal: ['#160f14', '#8a4a2c', '#f7e6d0', '#f2a65a'], dom: '#8A4A2C', credit: 'KESTREL PICTURES · INES CALLOWAY' },
    'paper-lanterns-kessel':{ title: 'Paper Lanterns Over Kessel', year: 2017, lines: ['PAPER LANTERNS', 'OVER KESSEL'], font: 'c', pos: 'top', motif: 'city', pal: ['#1d0f1a', '#5a2440', '#ffeede', '#ffb066'], dom: '#5A2440', credit: 'NIGHT MARKET · LIU WENSHAN' },
    'continental-drift':    { title: 'Continental Drift', year: 1998, lines: ['CONTINENTAL', 'DRIFT'], font: 'c', pos: 'top', motif: 'waves', pal: ['#e8dcc4', '#9b7b52', '#1c140c', '#6b4a2a'], dom: '#9B7B52', credit: 'TERRA FIRMA · WALTER BRISK' },
    'snowdrift':            { title: 'Snowdrift', year: 2011, lines: ['Snowdrift'], font: 's', pos: 'bottom', motif: 'moon', pal: ['#dfe7ee', '#8aa1b4', '#101820', '#ffffff'], dom: '#8AA1B4', credit: 'NORDLYS · ASTRID HOLM' },
    'drifters-calder':      { title: 'The Drifters of Calder Bay', year: 2005, lines: ['THE DRIFTERS', 'OF CALDER BAY'], font: 'x', pos: 'bottom', motif: 'lighthouse', pal: ['#14202a', '#3f5d6e', '#f1ece2', '#f2c46d'], dom: '#3F5D6E', credit: 'SALT ROAD · MORGAN FEIST' },
    'quiet-hours':          { title: 'The Quiet Hours', year: 2026, lines: ['The Quiet', 'Hours'], font: 's', pos: 'top', motif: 'arch', pal: ['#1d1a26', '#0d0c12', '#efe9f7', '#b9a6e8'], dom: '#3A3150', credit: 'NOCTURNE · ELODIE RANCE' },
    'iron-meadow':          { title: 'Iron Meadow', year: 2026, lines: ['IRON', 'MEADOW'], font: 'c', pos: 'bottom', motif: 'stripes', pal: ['#203024', '#121a14', '#e8f0e0', '#9cc27a'], dom: '#2E4A30', credit: 'FIELDWORK · BRANKO ILIC' },
    'small-hours-cairo':    { title: 'Small Hours in Cairo', year: 2026, lines: ['small hours', 'in cairo'], font: 'g', pos: 'top', motif: 'city', pal: ['#2a1608', '#6b3a14', '#fff0dc', '#ffc070'], dom: '#6B3A14', credit: 'NILE NIGHT · YASMIN FAROUK' },
  };

  function hash(s) { let h = 2166136261; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; }
  function rng(seed) { let a = seed; return function () { a |= 0; a = (a + 0x6D2B79F5) | 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; }; }
  let uidCounter = 0;

  // Motifs draw on a 200 x 300 canvas. p = palette, r = seeded random, u = unique id suffix.
  const motifs = {
    sun(p, r, u) {
      let cuts = `<clipPath id="sc${u}"><circle cx="100" cy="170" r="64"/></clipPath><g clip-path="url(#sc${u})">`; for (let i = 0; i < 7; i++) { const y = 176 + i * 9; cuts += `<rect x="30" y="${y}" width="140" height="${1 + i * 0.9}" fill="${p[1]}"/>`; } cuts += '</g>';
      return `<defs><linearGradient id="s${u}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${p[3]}"/><stop offset="1" stop-color="${p[2]}" stop-opacity=".9"/></linearGradient></defs>
        <circle cx="100" cy="170" r="64" fill="url(#s${u})" opacity=".95"/>${cuts}
        <path d="M0 232 L34 204 L62 220 L104 182 L140 214 L168 198 L200 216 V300 H0Z" fill="${p[0]}" opacity=".92"/>
        <path d="M0 252 L48 236 L96 250 L150 232 L200 246 V300 H0Z" fill="#000" opacity=".45"/>`;
    },
    orbit(p, r, u) {
      let rings = ''; for (let i = 0; i < 9; i++) rings += `<circle cx="100" cy="150" r="${28 + i * 12}" fill="none" stroke="${p[2]}" stroke-width="${i % 3 ? .5 : 1}" opacity="${.12 + (i % 4) * .08}"/>`;
      let stars = ''; for (let i = 0; i < 40; i++) stars += `<circle cx="${(r() * 200).toFixed(1)}" cy="${(r() * 300).toFixed(1)}" r="${(r() * .8 + .2).toFixed(2)}" fill="${p[2]}" opacity="${(r() * .6 + .2).toFixed(2)}"/>`;
      return `<defs><radialGradient id="o${u}" cx=".35" cy=".3" r=".8"><stop offset="0" stop-color="${p[3]}"/><stop offset="1" stop-color="${p[1]}"/></radialGradient></defs>
        ${stars}${rings}<circle cx="100" cy="150" r="22" fill="url(#o${u})"/>
        <circle cx="160" cy="96" r="6" fill="${p[2]}" opacity=".9"/><circle cx="52" cy="214" r="3" fill="${p[3]}"/>`;
    },
    lighthouse(p, r, u) {
      let sea = ''; for (let i = 0; i < 10; i++) sea += `<rect x="0" y="${232 + i * 7}" width="200" height="${.6 + i * .25}" fill="${p[2]}" opacity="${.08 + i * .02}"/>`;
      return `<defs><linearGradient id="b${u}" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="${p[3]}" stop-opacity=".75"/><stop offset="1" stop-color="${p[3]}" stop-opacity="0"/></linearGradient></defs>
        <polygon points="132,118 200,70 200,170" fill="url(#b${u})"/>
        <polygon points="128,118 0,60 0,150" fill="url(#b${u})" opacity=".35" transform="scale(1,1)"/>
        <path d="M120 230 L124 126 L136 126 L140 230Z" fill="${p[0]}"/><rect x="121" y="112" width="18" height="14" rx="2" fill="${p[3]}"/>
        <path d="M118 112 L130 100 L142 112Z" fill="${p[0]}"/>
        <path d="M70 232 C100 222 150 222 200 230 V300 H0 V236 C20 232 50 236 70 232Z" fill="${p[0]}" opacity=".85"/>${sea}`;
    },
    waves(p, r, u) {
      let w = ''; const n = 7;
      for (let i = 0; i < n; i++) {
        const y = 110 + i * 26, a = 8 + r() * 10, ph = r() * 60;
        let d = `M0 ${y}`; for (let x = 0; x <= 200; x += 20) d += ` Q${x + 10} ${y + (x / 20 % 2 ? a : -a) + ph * .05} ${x + 20} ${y}`;
        d += ' V300 H0Z';
        w += `<path d="${d}" fill="${i % 2 ? p[1] : p[3]}" opacity="${.18 + i * .1}"/>`;
      }
      return w + `<circle cx="158" cy="${r() > 2 ? 0 : 104}" r="9" fill="${p[2]}" opacity=".7"/>`;
    },
    orchid(p, r, u) {
      let petals = ''; for (let i = 0; i < 7; i++) petals += `<ellipse cx="100" cy="112" rx="15" ry="46" fill="url(#pe${u})" opacity=".85" transform="rotate(${i * 51.4} 100 150)"/>`;
      return `<defs><linearGradient id="pe${u}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${p[3]}"/><stop offset="1" stop-color="${p[1]}" stop-opacity=".2"/></linearGradient></defs>
        <g style="mix-blend-mode:screen">${petals}</g><circle cx="100" cy="150" r="9" fill="${p[2]}"/>
        <path d="M100 160 C96 210 108 240 100 300" stroke="${p[3]}" stroke-width="1.2" fill="none" opacity=".6"/>`;
    },
    stripes(p, r, u) {
      let s = ''; for (let i = 0; i < 10; i++) if (i !== 6) s += `<rect x="${i * 20 + 3}" y="70" width="12" height="190" fill="${i % 3 === 0 ? p[3] : p[2]}" opacity="${i % 3 === 0 ? 1 : .88}"/>`;
      return s + `<circle cx="129" cy="210" r="7" fill="${p[3]}"/>`;
    },
    arch(p, r, u) {
      return `<defs><linearGradient id="ar${u}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${p[3]}"/><stop offset="1" stop-color="${p[3]}" stop-opacity=".25"/></linearGradient>
        <linearGradient id="fl${u}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${p[3]}" stop-opacity=".45"/><stop offset="1" stop-color="${p[3]}" stop-opacity="0"/></linearGradient></defs>
        <path d="M62 250 V140 A38 38 0 0 1 138 140 V250Z" fill="url(#ar${u})"/>
        <polygon points="62,250 138,250 190,300 10,300" fill="url(#fl${u})"/>
        <path d="M97 250 v-24 a3 3 0 0 1 6 0 v24Z M96 222 a4 4 0 1 1 8 0 a4 4 0 1 1 -8 0" fill="${p[1]}"/>`;
    },
    city(p, r, u) {
      let b = '', win = '';
      for (let x = -4; x < 204;) {
        const w = 14 + Math.floor(r() * 18), h = 50 + Math.floor(r() * 110); b += `<rect x="${x}" y="${260 - h}" width="${w}" height="${h + 40}" fill="${p[0]}"/>`;
        for (let wy = 266 - h; wy < 252; wy += 7) for (let wx = x + 3; wx < x + w - 3; wx += 5) if (r() > .72) win += `<rect x="${wx}" y="${wy}" width="2" height="3" fill="${p[3]}" opacity="${(.5 + r() * .5).toFixed(2)}"/>`;
        x += w + 2;
      }
      return `<circle cx="140" cy="80" r="30" fill="${p[3]}" opacity=".18"/><circle cx="140" cy="80" r="16" fill="${p[3]}" opacity=".5"/>${b}${win}`;
    },
    signal(p, r, u) {
      let l = ''; for (let i = 0; i < 26; i++) {
        const y = 60 + i * 8; let d = `M10 ${y}`; for (let x = 10; x <= 190; x += 4) { const amp = Math.exp(-Math.pow((x - 100) / 34, 2)) * (6 + r() * 16); d += ` L${x} ${(y - amp * (r() > .5 ? 1 : .4)).toFixed(1)}`; }
        l += `<path d="${d}" fill="none" stroke="${i % 5 === 2 ? p[3] : p[2]}" stroke-width=".8" opacity="${i % 5 === 2 ? .95 : .5}"/>`;
      }
      return l;
    },
    grid(p, r, u) {
      let g = ''; for (let i = -10; i <= 10; i++) g += `<line x1="100" y1="168" x2="${100 + i * 26}" y2="300" stroke="${p[3]}" stroke-width=".6" opacity=".55"/>`;
      for (let i = 1; i < 12; i++) { const y = 168 + Math.pow(i / 11, 2.2) * 132; g += `<line x1="0" y1="${y.toFixed(1)}" x2="200" y2="${y.toFixed(1)}" stroke="${p[3]}" stroke-width=".6" opacity="${.15 + i * .04}"/>`; }
      return `<defs><radialGradient id="gv${u}"><stop offset="0" stop-color="${p[3]}" stop-opacity=".9"/><stop offset="1" stop-color="${p[3]}" stop-opacity="0"/></radialGradient></defs>
        <ellipse cx="100" cy="168" rx="90" ry="40" fill="url(#gv${u})"/><rect x="0" y="168" width="200" height="132" fill="${p[0]}" opacity=".55"/>${g}
        <polygon points="100,108 128,168 72,168" fill="none" stroke="${p[2]}" stroke-width="1"/>`;
    },
    split(p, r, u) {
      return `<polygon points="0,0 200,0 200,120 0,240" fill="${p[0]}"/><circle cx="100" cy="150" r="58" fill="${p[3]}" opacity=".92"/>
        <clipPath id="sp${u}"><polygon points="0,240 200,120 200,300 0,300"/></clipPath>
        <circle cx="100" cy="150" r="58" fill="${p[0]}" clip-path="url(#sp${u})" opacity=".9"/>
        <line x1="0" y1="240" x2="200" y2="120" stroke="${p[2]}" stroke-width=".8"/>`;
    },
    lanterns(p, r, u) {
      let l = `<defs><radialGradient id="ln${u}"><stop offset="0" stop-color="${p[3]}"/><stop offset=".45" stop-color="${p[3]}" stop-opacity=".55"/><stop offset="1" stop-color="${p[3]}" stop-opacity="0"/></radialGradient></defs>`;
      for (let i = 0; i < 26; i++) { const s = 3 + r() * 13; l += `<circle cx="${(r() * 200).toFixed(1)}" cy="${(40 + r() * 220).toFixed(1)}" r="${s.toFixed(1)}" fill="url(#ln${u})" opacity="${(.35 + r() * .65).toFixed(2)}"/>`; }
      return l + `<path d="M0 270 C60 250 140 262 200 248 V300 H0Z" fill="#000" opacity=".5"/>`;
    },
    moon(p, r, u) {
      let stars = ''; for (let i = 0; i < 50; i++) stars += `<circle cx="${(r() * 200).toFixed(1)}" cy="${(r() * 200).toFixed(1)}" r="${(r() * .7 + .15).toFixed(2)}" fill="${p[2]}" opacity="${(r() * .7 + .2).toFixed(2)}"/>`;
      return `${stars}<mask id="mm${u}"><rect width="200" height="300" fill="#fff"/><circle cx="118" cy="98" r="34" fill="#000"/></mask>
        <circle cx="104" cy="106" r="38" fill="${p[3]}" mask="url(#mm${u})"/>
        <path d="M0 236 C40 214 70 222 110 210 C150 198 176 214 200 206 V300 H0Z" fill="${p[1]}" opacity=".9"/>
        <path d="M0 256 C50 240 120 254 200 236 V300 H0Z" fill="${p[0]}"/>`;
    },
    figure(p, r, u) {
      return `<defs><radialGradient id="fg${u}" cx=".5" cy=".62" r=".6"><stop offset="0" stop-color="${p[3]}" stop-opacity=".55"/><stop offset="1" stop-color="${p[3]}" stop-opacity="0"/></radialGradient></defs>
        <rect width="200" height="300" fill="url(#fg${u})"/>
        <rect x="0" y="214" width="200" height="1" fill="${p[2]}" opacity=".5"/>
        <path d="M97.5 214 v-15 l-1.5 -9 a4 4 0 0 1 8 0 l-1.5 9 v15Z M96 182 a4 4 0 1 1 8 0 a4 4 0 1 1 -8 0" fill="${p[2]}"/>
        <path d="M97.5 215 L60 300 H140 L102.5 215Z" fill="${p[2]}" opacity=".08"/>`;
    },
    marquee(p, r, u) {
      let bulbs = ''; for (let i = 0; i < 17; i++) { bulbs += `<circle cx="${30 + i * 8.75}" cy="128" r="1.8" fill="${p[3]}"/><circle cx="${30 + i * 8.75}" cy="196" r="1.8" fill="${p[3]}"/>`; }
      for (let i = 1; i < 8; i++) bulbs += `<circle cx="30" cy="${128 + i * 8.5}" r="1.8" fill="${p[3]}"/><circle cx="170" cy="${128 + i * 8.5}" r="1.8" fill="${p[3]}"/>`;
      return `<rect x="24" y="122" width="152" height="80" rx="3" fill="${p[0]}" stroke="${p[3]}" stroke-opacity=".5"/>${bulbs}
        <text x="100" y="158" text-anchor="middle" font-family="Geist Mono, monospace" font-size="9" letter-spacing="3" fill="${p[2]}">TONIGHT</text>
        <text x="100" y="176" text-anchor="middle" font-family="Geist Mono, monospace" font-size="7" letter-spacing="2" fill="${p[3]}">ONE SHOW ONLY</text>
        <rect x="40" y="202" width="6" height="98" fill="${p[0]}"/><rect x="154" y="202" width="6" height="98" fill="${p[0]}"/>`;
    },
    monolith(p, r, u) {
      return `<defs><radialGradient id="mg${u}" cx=".5" cy=".55" r=".5"><stop offset="0" stop-color="${p[3]}" stop-opacity=".6"/><stop offset="1" stop-color="${p[3]}" stop-opacity="0"/></radialGradient></defs>
        <rect width="200" height="300" fill="url(#mg${u})"/><rect x="0" y="222" width="200" height="78" fill="${p[0]}"/>
        <rect x="84" y="74" width="32" height="148" fill="#060807"/><rect x="84" y="74" width="1.2" height="148" fill="${p[3]}" opacity=".9"/>
        <polygon points="84,222 116,222 150,300 50,300" fill="${p[3]}" opacity=".07"/>`;
    },
    halftone(p, r, u) {
      let d = ''; for (let y = 6; y < 300; y += 8) for (let x = 6; x < 200; x += 8) { const dist = Math.hypot(x - 120, y - 120) / 150; const rr = Math.max(0, 3.4 * (1 - dist)); if (rr > .25) d += `<circle cx="${x}" cy="${y}" r="${rr.toFixed(2)}" fill="${p[2]}"/>`; }
      return d + `<rect x="20" y="198" width="160" height="2" fill="${p[3]}"/>`;
    },
  };

  const fontSpec = {
    x: { family: 'Archivo, sans-serif', stretch: '125%', weight: 700, ls: .04, k: .82, upper: true },
    c: { family: 'Archivo, sans-serif', stretch: '62%', weight: 800, ls: .01, k: .43, upper: true },
    s: { family: "'Instrument Serif', Georgia, serif", stretch: '100%', weight: 400, ls: -.01, k: .44, upper: false },
    g: { family: 'Geist, sans-serif', stretch: '100%', weight: 600, ls: -.03, k: .55, upper: false },
  };

  function titleBlock(f, u) {
    const fs = fontSpec[f.font];
    const maxW = 172;
    const base = f.font === 'c' ? 46 : f.font === 's' ? 36 : f.font === 'g' ? 30 : 24;
    const sizes = f.lines.map(l => Math.min(base, maxW / (l.length * fs.k * (1 + fs.ls))));
    const size = Math.min(...sizes);
    const lh = size * (f.font === 's' ? 0.98 : 0.92);
    const total = lh * f.lines.length;
    let y0 = f.pos === 'top' ? 26 + size * .8 : f.pos === 'center' ? 150 - total / 2 + size * .75 : 262 - total + size * .75;
    const ink = f.pal[2];
    const t = f.lines.map((l, i) => `<text x="100" y="${(y0 + i * lh).toFixed(1)}" text-anchor="middle" font-family="${fs.family}" font-weight="${fs.weight}" font-size="${size.toFixed(1)}" letter-spacing="${(fs.ls * size).toFixed(2)}" style="font-stretch:${fs.stretch}" fill="${ink}"${f.font === 's' && i === 0 && f.lines.length > 1 ? ' font-style="italic"' : ''}>${esc(l)}</text>`).join('');
    const cy = f.pos === 'bottom' ? 18 : 286;
    const credit = `<text x="100" y="${cy}" text-anchor="middle" font-family="Archivo, sans-serif" font-size="4.6" letter-spacing="1.1" style="font-stretch:75%" fill="${ink}" opacity=".62">${esc(f.credit)}</text>`;
    return t + credit;
  }

  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;'); }

  function poster(id) {
    const f = films[id]; if (!f) return '';
    const u = id.replace(/[^a-z]/g, '').slice(0, 6) + (++uidCounter);
    const r = rng(hash(id)); const p = f.pal;
    const scrimTop = f.pos === 'top' ? `<rect width="200" height="110" fill="url(#st${u})"/>` : '';
    const scrimBottom = f.pos !== 'top' ? `<rect y="170" width="200" height="130" fill="url(#sb${u})"/>` : '';
    return `<svg viewBox="0 0 200 300" preserveAspectRatio="xMidYMid slice" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Poster for ${esc(f.title)} (${f.year})">
<defs>
<linearGradient id="bg${u}" x1="0" y1="0" x2=".35" y2="1"><stop offset="0" stop-color="${p[0]}"/><stop offset="1" stop-color="${p[1]}"/></linearGradient>
<radialGradient id="vg${u}" cx=".5" cy=".45" r=".75"><stop offset=".55" stop-color="#000" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity=".5"/></radialGradient>
<linearGradient id="st${u}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#000" stop-opacity=".35"/><stop offset="1" stop-color="#000" stop-opacity="0"/></linearGradient>
<linearGradient id="sb${u}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#000" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity=".45"/></linearGradient>
<filter id="gr${u}" x="0" y="0" width="100%" height="100%"><feTurbulence type="fractalNoise" baseFrequency=".9" numOctaves="2" seed="${hash(id) % 97}" stitchTiles="stitch"/><feColorMatrix type="saturate" values="0"/><feComponentTransfer><feFuncA type="table" tableValues="0 .55"/></feComponentTransfer></filter>
</defs>
<rect width="200" height="300" fill="url(#bg${u})"/>
${motifs[f.motif](p, r, u)}
<rect width="200" height="300" fill="url(#vg${u})"/>${scrimTop}${scrimBottom}
${titleBlock(f, u)}
<rect width="200" height="300" filter="url(#gr${u})" opacity=".55" style="mix-blend-mode:overlay"/>
</svg>`;
  }

  function backdrop(id) {
    const f = films[id]; if (!f) return '';
    const u = 'bd' + (++uidCounter); const r = rng(hash(id) + 7); const p = f.pal;
    return `<svg viewBox="0 0 480 270" preserveAspectRatio="xMidYMid slice" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<defs><linearGradient id="bg${u}" x1="0" y1="0" x2=".2" y2="1"><stop offset="0" stop-color="${p[0]}"/><stop offset="1" stop-color="${p[1]}"/></linearGradient></defs>
<rect width="480" height="270" fill="url(#bg${u})"/>
<g transform="translate(250 -40) scale(1.25)">${motifs[f.motif](p, r, u)}</g>
<g transform="translate(-40 10) scale(.9)" opacity=".5">${motifs[f.motif](p, rng(hash(id) + 3), u + 'b')}</g>
</svg>`;
  }

  function render(root) {
    (root || document).querySelectorAll('.poster-art[data-film]').forEach(el => { if (!el.dataset.done) { el.innerHTML = poster(el.dataset.film); el.dataset.done = '1'; } });
    (root || document).querySelectorAll('.backdrop-art[data-film]').forEach(el => { if (!el.dataset.done) { el.innerHTML = backdrop(el.dataset.film); el.dataset.done = '1'; } });
  }

  window.GN = window.GN || {};
  Object.assign(window.GN, { films, poster, backdrop, renderPosters: render });
  if (document.readyState !== 'loading') render(); else document.addEventListener('DOMContentLoaded', () => render());
})();
