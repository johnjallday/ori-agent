// Serve the CDN assets the page templates reference from node_modules instead
// of jsdelivr / Google Fonts.
//
// Why: layout/head.tmpl loads Bootstrap and Bootstrap Icons from a CDN, and
// base.tmpl loads the Bootstrap JS bundle. On a machine without outbound DNS
// those requests fail, Bootstrap's CSS never applies, and components that rely
// on it — most visibly the shared vault modal — render unstyled and full-size.
// The modal's backdrop then covers the page and silently intercepts every
// click, so unrelated specs fail with "element intercepts pointer events".
//
// Routing to the local copies makes specs hermetic and faster, and the pinned
// versions below are asserted against package.json so they cannot drift from
// what the templates actually request.
import type { Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const pkg = require('../../package.json');

const BOOTSTRAP = pkg.dependencies?.bootstrap ?? pkg.devDependencies?.bootstrap;
const ICONS = pkg.dependencies?.['bootstrap-icons'] ?? pkg.devDependencies?.['bootstrap-icons'];

function resolveDir(id: string): string {
  return require.resolve(`${id}/package.json`).replace(/\/package\.json$/, '');
}

/**
 * installLocalCdn routes the templates' CDN URLs to the matching node_modules
 * files. Call it before page.goto(). Webfonts from Google Fonts are aborted
 * rather than faked: they are purely cosmetic and the page falls back to a
 * system stack.
 */
export async function installLocalCdn(page: Page): Promise<void> {
  const bootstrapDir = resolveDir('bootstrap');
  const iconsDir = resolveDir('bootstrap-icons');

  await page.route(`**/bootstrap@${BOOTSTRAP}/dist/css/bootstrap.min.css`, route =>
    route.fulfill({
      status: 200,
      contentType: 'text/css',
      body: readFileSync(`${bootstrapDir}/dist/css/bootstrap.min.css`, 'utf8')
    })
  );

  await page.route(`**/bootstrap@${BOOTSTRAP}/dist/js/bootstrap.bundle.min.js`, route =>
    route.fulfill({
      status: 200,
      contentType: 'text/javascript',
      body: readFileSync(`${bootstrapDir}/dist/js/bootstrap.bundle.min.js`, 'utf8')
    })
  );

  await page.route(`**/bootstrap-icons@${ICONS}/font/bootstrap-icons.css`, route =>
    route.fulfill({
      status: 200,
      contentType: 'text/css',
      body: readFileSync(`${iconsDir}/font/bootstrap-icons.css`, 'utf8')
    })
  );

  // The icon stylesheet references its webfont relatively; without it every
  // role emblem renders as a tofu box.
  await page.route(`**/bootstrap-icons@${ICONS}/font/fonts/*`, route => {
    const name = new URL(route.request().url()).pathname.split('/').pop() as string;
    route.fulfill({
      status: 200,
      contentType: name.endsWith('woff2') ? 'font/woff2' : 'font/woff',
      body: readFileSync(`${iconsDir}/font/fonts/${name}`)
    });
  });

  await page.route('https://fonts.googleapis.com/**', route => route.abort());
  await page.route('https://fonts.gstatic.com/**', route => route.abort());
}
