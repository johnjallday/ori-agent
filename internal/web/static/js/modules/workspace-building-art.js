// Curated, inert building silhouettes shared by the Build Catalog and Workspace Map.
// A silhouette communicates only the originating built-in blueprint. It never
// infers identity from a workspace name and never encodes live state.
(function () {
  'use strict';

  const BLUEPRINT_VARIANTS = Object.freeze({
    'personal-ops': 'hq',
    'email-ops': 'mail',
    'calendar-ops': 'calendar',
    'research-project': 'research',
    'content-production': 'studio',
    'writing-project': 'studio',
    'daily-briefings': 'briefing',
    'downloads-janitor': 'depot',
    'file-janitor': 'depot',
    'github-ops': 'github',
    travels: 'travel'
  });

  const VARIANTS = Object.freeze({
    hq: Object.freeze({
      color: '#e8b54b',
      markup: `
        <polygon points="60 50 112 70 60 87 8 70" fill="currentColor" opacity=".16" stroke="currentColor" stroke-opacity=".52"/>
        <polygon points="20 54 60 70 60 84 20 68" fill="currentColor" opacity=".45"/>
        <polygon points="100 54 60 70 60 84 100 68" fill="currentColor" opacity=".28"/>
        <polygon points="60 40 100 54 60 70 20 54" fill="currentColor" opacity=".7"/>
        <polygon points="38 31 60 40 60 67 38 58" fill="currentColor" opacity=".88"/>
        <polygon points="82 31 60 40 60 67 82 58" fill="currentColor" opacity=".56"/>
        <polygon points="60 20 84 30 60 42 36 30" fill="currentColor"/>
        <path d="M60 21V5" fill="none" stroke="currentColor" stroke-width="2"/>
        <path d="M60 7l15 5-15 5z" fill="currentColor"/>
        <circle cx="60" cy="4" r="2.4" fill="currentColor"/>
        <path d="M27 60v11M93 60v11M45 55v18M75 55v18" fill="none" stroke="currentColor" stroke-width="2" opacity=".7"/>
        <g data-building-emblem="hq">
          <circle cx="60" cy="50" r="9" fill="#101619" fill-opacity=".78" stroke="currentColor" stroke-width="1.2"/>
          <path d="M60 43l1.9 5.1L67 50l-5.1 1.9L60 57l-1.9-5.1L53 50l5.1-1.9z" fill="#f6f0df" fill-opacity=".88"/>
        </g>`
    }),
    briefing: Object.freeze({
      color: '#d08aa7',
      markup: `
        <polygon points="60 53 109 70 60 87 11 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="20 45 60 58 60 79 20 66" fill="currentColor" opacity=".4"/>
        <polygon points="100 45 60 58 60 79 100 66" fill="currentColor" opacity=".58"/>
        <polygon points="20 45 60 32 100 45 60 59" fill="currentColor" opacity=".84"/>
        <polygon points="43 22 60 15 77 22 77 53 60 59 43 53" fill="currentColor" opacity=".92"/>
        <path d="M60 15V4M60 7h15M72 4v6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
        <g data-building-emblem="briefing">
          <rect x="47" y="27" width="27" height="24" rx="1.5" fill="#101619" fill-opacity=".76" stroke="currentColor" stroke-width="1.2"/>
          <path d="M52 45V39M58 45V34M64 45V37M70 45V31" stroke="#f6f0df" stroke-opacity=".88" stroke-width="2"/>
          <path d="M51 32h8" stroke="#f6f0df" stroke-opacity=".88" stroke-width="1.6" stroke-linecap="round"/>
        </g>`
    }),
    mail: Object.freeze({
      color: '#68c8b6',
      markup: `
        <polygon points="60 51 110 69 60 87 10 69" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".42"/>
        <polygon points="20 42 60 55 60 78 20 65" fill="currentColor" opacity=".38"/>
        <polygon points="100 42 60 55 60 78 100 65" fill="currentColor" opacity=".62"/>
        <polygon points="60 27 104 41 60 57 16 41" fill="currentColor" opacity=".88"/>
        <path d="M30 38l30 11 30-11M30 38l30-3 30 3" fill="none" stroke="#0d1718" stroke-opacity=".72" stroke-width="2"/>
        <polygon points="74 57 90 52 90 65 74 70" fill="#0d1718" opacity=".58"/>
        <polygon points="30 52 45 57 45 67 30 62" fill="#0d1718" opacity=".42"/>
        <path d="M94 32V16h9M97 18h10v8H97" fill="none" stroke="currentColor" stroke-width="2"/>
        <g data-building-emblem="mail">
          <rect x="43" y="29" width="34" height="21" rx="1.5" fill="#101619" fill-opacity=".74" stroke="currentColor" stroke-width="1.2"/>
          <path d="M46 32l14 10 14-10M46 47l9-8M74 47l-9-8" fill="none" stroke="#f6f0df" stroke-opacity=".9" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
        </g>`
    }),
    calendar: Object.freeze({
      color: '#65bce5',
      markup: `
        <polygon points="60 52 109 70 60 87 11 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="17 48 60 61 60 79 17 66" fill="currentColor" opacity=".38"/>
        <polygon points="103 48 60 61 60 79 103 66" fill="currentColor" opacity=".58"/>
        <polygon points="17 48 60 35 103 48 60 62" fill="currentColor" opacity=".76"/>
        <polygon points="44 21 60 26 60 62 44 57" fill="currentColor" opacity=".9"/>
        <polygon points="76 21 60 26 60 62 76 57" fill="currentColor" opacity=".58"/>
        <polygon points="60 10 79 20 60 28 41 20" fill="currentColor"/>
        <path d="M60 10V3" stroke="currentColor" stroke-width="2"/>
        <g data-building-emblem="calendar">
          <rect x="48" y="29" width="24" height="24" rx="1.5" fill="#101619" fill-opacity=".76" stroke="currentColor" stroke-width="1.2"/>
          <path d="M48 36h24M54 27v6M66 27v6" fill="none" stroke="#f6f0df" stroke-opacity=".9" stroke-width="1.7" stroke-linecap="round"/>
          <path d="M53 40h4v3h-4zM63 40h4v3h-4zM53 46h4v3h-4zM63 46h4v3h-4z" fill="currentColor"/>
        </g>`
    }),
    research: Object.freeze({
      color: '#9b91e8',
      markup: `
        <polygon points="60 54 109 70 60 87 11 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="26 48 60 59 60 79 26 68" fill="currentColor" opacity=".38"/>
        <polygon points="94 48 60 59 60 79 94 68" fill="currentColor" opacity=".58"/>
        <polygon points="26 48 60 37 94 48 60 60" fill="currentColor" opacity=".72"/>
        <path d="M39 39c0-14 10-25 21-25s21 11 21 25z" fill="currentColor" opacity=".9"/>
        <path d="M42 38c3-11 10-18 18-18 9 0 16 7 18 18M60 17v21" fill="none" stroke="#101619" stroke-opacity=".7" stroke-width="1.5"/>
        <path d="M71 22l18-9 4 7-18 10" fill="currentColor"/>
        <circle cx="92" cy="16" r="3" fill="currentColor" opacity=".7"/>
        <g data-building-emblem="research">
          <path d="M57 25v8l-7 13c-2 4 0 7 4 7h12c4 0 6-3 4-7l-7-13v-8" fill="#101619" fill-opacity=".72" stroke="#f6f0df" stroke-opacity=".88" stroke-width="1.6" stroke-linejoin="round"/>
          <path d="M55 25h10M53 44h14" fill="none" stroke="#f6f0df" stroke-opacity=".88" stroke-width="1.7" stroke-linecap="round"/>
          <circle cx="58" cy="48" r="1.4" fill="currentColor"/><circle cx="63" cy="50" r="1.1" fill="currentColor"/>
        </g>`
    }),
    studio: Object.freeze({
      color: '#e5966b',
      markup: `
        <polygon points="60 53 109 70 60 87 11 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="20 41 60 55 60 78 20 65" fill="currentColor" opacity=".4"/>
        <polygon points="100 41 60 55 60 78 100 65" fill="currentColor" opacity=".6"/>
        <path d="M17 41l20-16v10l21-16v10l21-15v11l24 16-43 15z" fill="currentColor" opacity=".9"/>
        <path d="M37 25v10M58 19v10M79 14v11" stroke="#101619" stroke-opacity=".58" stroke-width="2"/>
        <polygon points="70 56 91 49 91 65 70 72" fill="#101619" opacity=".55"/>
        <path d="M74 59l13-4M74 64l13-4" stroke="currentColor" stroke-width="1.4" opacity=".7"/>
        <g data-building-emblem="publishing">
          <rect x="44" y="33" width="30" height="22" rx="1.5" fill="#101619" fill-opacity=".74" stroke="currentColor" stroke-width="1.2"/>
          <path d="M50 39h17M50 44h13M50 49h9" stroke="#f6f0df" stroke-opacity=".86" stroke-width="1.6" stroke-linecap="round"/>
          <path d="M63 49l10-10 3 3-10 10-4 1z" fill="currentColor" stroke="#101619" stroke-width="1.1" stroke-linejoin="round"/>
        </g>`
    }),
    depot: Object.freeze({
      color: '#c3b56d',
      markup: `
        <polygon points="60 54 110 70 60 87 10 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="18 41 60 56 60 79 18 64" fill="currentColor" opacity=".4"/>
        <polygon points="102 41 60 56 60 79 102 64" fill="currentColor" opacity=".58"/>
        <polygon points="18 41 60 26 102 41 60 57" fill="currentColor" opacity=".88"/>
        <path d="M29 46v20M39 50v20M49 53v20M71 53v20M81 50v20M91 46v20" stroke="#101619" stroke-opacity=".5" stroke-width="2"/>
        <rect x="48" y="17" width="24" height="10" fill="currentColor"/>
        <path d="M52 22h16" stroke="#101619" stroke-width="2"/>
        <g data-building-emblem="folder">
          <path d="M43 32h14l5 5h16v18H43z" fill="#101619" fill-opacity=".74" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/>
          <path d="M48 43h25M60 39v11M56 47l4 4 4-4" fill="none" stroke="#f6f0df" stroke-opacity=".88" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>
        </g>`
    }),
    github: Object.freeze({
      color: '#8fb3cc',
      markup: `
        <polygon points="60 52 108 70 60 87 12 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="25 38 60 51 60 78 25 65" fill="currentColor" opacity=".4"/>
        <polygon points="95 38 60 51 60 78 95 65" fill="currentColor" opacity=".58"/>
        <polygon points="25 38 43 24 77 24 95 38 60 52" fill="currentColor" opacity=".88"/>
        <polygon points="43 24 50 13 70 13 77 24" fill="currentColor"/>
        <path d="M34 47l17 6v12l-17-6zM69 53l17-6v12l-17 6z" fill="#101619" opacity=".54"/>
        <g data-building-emblem="github">
          <rect x="46" y="27" width="28" height="25" rx="2" fill="#101619" fill-opacity=".76" stroke="currentColor" stroke-width="1.3"/>
          <path d="M53 35v11M67 35v3c0 5.5-4.5 9-12 9" fill="none" stroke="#f6f0df" stroke-opacity=".9" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
          <circle cx="53" cy="33" r="2.2" fill="currentColor" stroke="#f6f0df" stroke-width="1.2"/>
          <circle cx="67" cy="33" r="2.2" fill="currentColor" stroke="#f6f0df" stroke-width="1.2"/>
          <circle cx="53" cy="48" r="2.2" fill="currentColor" stroke="#f6f0df" stroke-width="1.2"/>
        </g>`
    }),
    travel: Object.freeze({
      color: '#65c6cb',
      markup: `
        <polygon points="60 54 109 70 60 87 11 70" fill="currentColor" opacity=".13" stroke="currentColor" stroke-opacity=".45"/>
        <polygon points="22 45 60 57 60 79 22 67" fill="currentColor" opacity=".4"/>
        <polygon points="98 45 60 57 60 79 98 67" fill="currentColor" opacity=".58"/>
        <polygon points="22 45 60 32 98 45 60 58" fill="currentColor" opacity=".84"/>
        <polygon points="46 20 60 14 74 20 74 51 60 56 46 51" fill="currentColor" opacity=".9"/>
        <path d="M60 14V4M60 7l18 6-18 5" fill="none" stroke="currentColor" stroke-width="2"/>
        <g data-building-emblem="flight">
          <circle cx="60" cy="38" r="12" fill="#101619" fill-opacity=".74" stroke="currentColor" stroke-width="1.3"/>
          <path d="M60 27c1.2 0 1.8 1.1 1.8 2.8v5.4l7.8 4.8v2l-7.8-2.1v4.7l3 2.2v1.6L60 47.1l-4.8 1.3v-1.6l3-2.2v-4.7L50.4 42v-2l7.8-4.8v-5.4c0-1.7.6-2.8 1.8-2.8z" fill="#f6f0df" fill-opacity=".9" stroke="currentColor" stroke-width=".7" stroke-linejoin="round"/>
        </g>`
    })
  });

  function variantForBlueprint(blueprintID, builtin) {
    if (builtin !== true) return '';
    const normalized = String(blueprintID || '').trim();
    return Object.prototype.hasOwnProperty.call(BLUEPRINT_VARIANTS, normalized)
      ? BLUEPRINT_VARIANTS[normalized]
      : '';
  }

  function variantForWorkspace(workspace) {
    if (!workspace) return '';
    const variant = variantForBlueprint(
      workspace.blueprint_id,
      workspace.blueprint_builtin === true
    );
    // Personal HQ is designation-driven on the map. The Personal Ops blueprint
    // may preview HQ architecture in the catalog, but provenance alone cannot
    // promote an ordinary workspace into the reserved singleton landmark.
    if (variant === 'hq' && workspace.designation !== 'personal_hq') return '';
    return variant;
  }

  function svgForVariant(variant, options) {
    const normalized = String(variant || '').trim();
    if (!Object.prototype.hasOwnProperty.call(VARIANTS, normalized)) return '';
    const definition = VARIANTS[normalized];

    const context = options && options.context === 'catalog' ? 'catalog' : 'map';
    const className =
      context === 'catalog'
        ? 'workspace-template-building-art'
        : 'ws-map-struct ws-map-struct--blueprint';

    return `<svg class="${className}" data-building-variant="${normalized}" viewBox="0 0 120 88" width="112" height="92" style="color:${definition.color}" aria-hidden="true" focusable="false">${definition.markup}</svg>`;
  }

  window.OriWorkspaceBuildingArt = Object.freeze({
    variantForBlueprint,
    variantForWorkspace,
    svgForVariant
  });
})();
