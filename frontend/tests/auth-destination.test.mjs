import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { authDestination, authLink } from '../lib/auth-destination.ts';

test('only invitations survives login and signup through verification and Google', () => {
  for (const entry of ['/login', '/signup']) {
    const page = readFileSync(new URL(`../app/(auth)${entry}/page.tsx`, import.meta.url), 'utf8');
    assert.match(page, /authLink\(`\/verify\?email=\$\{encodeURIComponent\(email\)\}`, next\)/);
    assert.match(page, /authLink\("\/api\/v1\/auth\/google\/start", next\)/);
    const verify = authLink('/verify?email=guest%40example.test', '/invitations');
    assert.equal(authDestination(new URL(verify, 'https://example.test').searchParams.get('next')), '/invitations');
  }
  const page = readFileSync(new URL('../app/(auth)/verify/page.tsx', import.meta.url), 'utf8');
  assert.match(page, /router\.push\(authDestination\(next\)\)/);
  assert.match(page, /"\/v1\/auth\/signup\/verify", "\/v1\/auth\/login\/verify"/);
});

test('reject all other destinations', () => {
  for (const next of [null, '', 'https://evil.test', '//evil.test', '/invitations?x=1', '/invitations/../admin', '/overview', '/\\evil.test']) {
    assert.equal(authDestination(next), '/overview');
    assert.equal(authLink('/login', next), '/login');
    assert.equal(authLink('/api/v1/auth/google/start', next), '/api/v1/auth/google/start');
  }
});
