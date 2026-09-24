import test from 'node:test';
import assert from 'node:assert/strict';
import {
  renderDossierSources,
  safeDossierRoute,
  sourceCapabilityDisclosures,
  sourceCardSummary
} from './personal-hq-sources.js';

test('saved observation displays only its actual saved date and never claims a scan', () => {
  const body = sourceCardSummary('saved_apps', {
    status: 'available',
    observed_at: '2026-09-22T12:00:00Z'
  });
  assert.match(body, /Sep 22, 2026/);
  assert.match(body, /not a new scan/);
  assert.match(
    sourceCardSummary('saved_apps', { status: 'not_configured' }),
    /No durable app observation/
  );
  assert.match(
    sourceCardSummary('saved_apps', { status: 'available', observed_at: 'bad' }),
    /evidence is unavailable/
  );
});

test('healthy-empty and revoked are not conflated with supported source patterns', () => {
  assert.match(
    sourceCardSummary('file_janitor', { status: 'healthy_empty' }),
    /no currently supported/
  );
  assert.match(
    sourceCardSummary('file_janitor', { status: 'not_configured' }),
    /No eligible built-in/
  );
  assert.match(sourceCardSummary('file_janitor', { status: 'revoked' }), /missing or revoked/);
  assert.match(
    sourceCardSummary('file_janitor', { status: 'unavailable' }),
    /could not be verified/
  );
  assert.match(sourceCardSummary('email', { status: 'available' }), /not enabled here/);
  assert.match(sourceCardSummary('calendar', { status: 'not_configured' }), /not enabled here/);
});

test('capability copy is inert, bounded, and does not promise email/calendar learning', () => {
  const card = {
    status: 'not_configured',
    can_read: '<img src=x onerror=alert(1)>',
    can_propose: 'Follow-ups and draft replies.',
    requires_confirmation: 'No external write is mapped.'
  };
  const disclosures = sourceCapabilityDisclosures('email', card);
  assert.equal(disclosures.length, 3);
  assert.equal(disclosures[0].label, 'After setup · Can read');
  assert.equal(disclosures[0].text, card.can_read);
  assert.match(disclosures[1].label, /existing workflows/);
  assert.equal(sourceCapabilityDisclosures('saved_apps', card).length, 0);
  assert.equal(
    sourceCapabilityDisclosures('calendar', { ...card, status: 'available' })[0].label,
    'Can read'
  );
  assert.equal(
    sourceCapabilityDisclosures('calendar', { ...card, can_read: 'a'.repeat(300) })[0].text.length,
    280
  );

  class FakeNode {
    constructor(tag) {
      this.tag = tag;
      this.children = [];
    }
    append(...children) {
      this.children.push(...children);
    }
    replaceChildren(...children) {
      this.children = [...children];
    }
  }
  const previous = globalThis.document;
  globalThis.document = {
    createElement: tag => new FakeNode(tag)
  };
  try {
    const container = new FakeNode('section');
    renderDossierSources(
      container,
      {},
      { cards: [{ key: 'email', ...card, status: 'available', action_route: '/settings' }] }
    );
    const email = container.children.find(child => child.children[0]?.textContent === 'Email');
    assert.ok(email);
    assert.equal(
      email.children.filter(child => child.tag === 'a').length,
      0,
      'connected sources must not show connect nudges'
    );
    assert.equal(
      email.children.find(child => child.tag === 'dl').children[1].textContent,
      card.can_read
    );
    renderDossierSources(
      container,
      {},
      { cards: [{ key: 'email', ...card, status: 'revoked', action_route: '/settings' }] }
    );
    const revoked = container.children.find(child => child.children[0]?.textContent === 'Email');
    assert.equal(revoked.children.find(child => child.tag === 'a').href, '/settings');
  } finally {
    globalThis.document = previous;
  }
});

test('setup routes cannot send dossier clicks to arbitrary origins or control characters', () => {
  for (const unsafe of [
    '//evil.test',
    'javascript:alert(1)',
    '/\\evil.test',
    '/\n/r',
    '/\u0000bad',
    '/%0Abad',
    '/%2Fevil.test'
  ]) {
    assert.equal(safeDossierRoute(unsafe), false, unsafe);
  }
  assert.equal(safeDossierRoute('/?create=1&blueprint=calendar-ops'), true);
  assert.equal(safeDossierRoute('/settings#google-account'), true);
});
