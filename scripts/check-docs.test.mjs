import assert from 'node:assert/strict';
import test from 'node:test';
import { validateFoundation } from './check-docs.mjs';

function foundation(extra = {}) {
  return new Map(Object.entries({
    '.gitignore': '.evidence/\n',
    'AGENTS.md': '# Contributor notes\n',
    'CONTRIBUTING.md': '# Contributing\n\nRead [the notes](AGENTS.md).\n',
    'LICENSE': 'License fixture\n',
    'NOTICE': 'Notice fixture\n',
    'docs/README.md': '# Product guide\n\nRead [mining](mining.md#round-results) and [Fased](https://docs.fased.ai).\n',
    'docs/mining.md': '# Mining\n\n## Round results\n',
    ...extra,
  }));
}

test('accepts the documentation foundation and current relative links', () => {
  assert.deepEqual(validateFoundation(foundation()), []);
});

test('rejects broken local destinations and fragments', () => {
  const errors = validateFoundation(foundation({
    'docs/README.md': '# Guide\n\n[Missing](absent.md) [Wrong section](mining.md#absent)\n',
  }));
  assert.ok(errors.some(error => error.includes('missing target absent.md')));
  assert.ok(errors.some(error => error.includes('missing heading mining.md#absent')));
});

test('accepts decoded paths and duplicate-heading slugs', () => {
  assert.deepEqual(validateFoundation(foundation({
    'docs/README.md': '# Guide\n\n[Details](more%20details.md#same-1)\n',
    'docs/more details.md': '# Same\n\n## Same\n',
  })), []);
});

test('rejects links outside the repository and private workspace links', () => {
  const errors = validateFoundation(foundation({
    'docs/README.md': '# Guide\n\n[Outside](../../private.md) [Private](/home/fc/private.md) [File](file:///private.md)\n',
  }));
  assert.ok(errors.some(error => error.includes('escapes repository')));
  assert.equal(errors.filter(error => error.includes('private or unsupported link')).length, 2);
});

test('fails application source and unadmitted CI instead of reporting docs success', () => {
  const errors = validateFoundation(foundation({
    'src/app.tsx': 'export const app = true;\n',
    'package.json': '{}\n',
    '.github/workflows/other.yml': 'name: Other\n',
  }));
  assert.equal(errors.filter(error => error.includes('unsupported tracked path')).length, 3);
});

test('rejects missing licensing boundaries and malformed documentation', () => {
  const files = foundation({ 'docs/README.md': 'No title or final newline' });
  files.delete('LICENSE');
  const errors = validateFoundation(files);
  assert.ok(errors.some(error => error.includes('missing foundation file LICENSE')));
  assert.ok(errors.some(error => error.includes('missing top-level heading')));
  assert.ok(errors.some(error => error.includes('missing final newline')));
});

test('rejects trailing whitespace, conflict markers and malformed URLs', () => {
  const errors = validateFoundation(foundation({
    'docs/README.md': '# Guide\n\nText \n<<<<<<< HEAD\n[Broken](https://)\n',
  }));
  assert.ok(errors.some(error => error.includes('trailing whitespace')));
  assert.ok(errors.some(error => error.includes('merge conflict marker')));
  assert.ok(errors.some(error => error.includes('invalid URL')));
});
