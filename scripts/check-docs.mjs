import { execFileSync } from 'node:child_process';
import { lstatSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const foundationFiles = ['.gitignore', 'AGENTS.md', 'CONTRIBUTING.md', 'LICENSE', 'NOTICE'];
const toolingFiles = new Set([
  '.github/workflows/checks.yml',
  'scripts/check-docs.mjs',
  'scripts/check-docs.test.mjs',
]);

function supported(name) {
  return foundationFiles.includes(name) || toolingFiles.has(name)
    || /^[^/]+\.md$/.test(name) || /^docs\/.+\.md$/.test(name);
}

function headings(text) {
  const slugs = new Set();
  for (const match of text.matchAll(/^#{1,6}\s+(.+?)\s*#*$/gm)) {
    const base = match[1].toLowerCase().replace(/[^\p{L}\p{N}_\-\s]/gu, '').replace(/\s/g, '-');
    let slug = base;
    let suffix = 0;
    while (slugs.has(slug)) slug = `${base}-${++suffix}`;
    slugs.add(slug);
  }
  return slugs;
}

// Main currently contains the licensing/documentation foundation only. Do not
// let this focused check certify application code without its own runtime CI.
export function validateFoundation(files) {
  const errors = [];
  for (const name of [...foundationFiles, 'docs/README.md']) {
    if (!files.has(name)) errors.push(`missing foundation file ${name}`);
  }
  for (const [name, text] of files) {
    if (!supported(name)) {
      errors.push(`unsupported tracked path ${name}: add its owning checks before admitting application source`);
      continue;
    }
    if (!name.endsWith('.md')) continue;
    if (!/^#\s+\S/m.test(text)) errors.push(`${name}: missing top-level heading`);
    if (!text.endsWith('\n')) errors.push(`${name}: missing final newline`);
    if (/^(?:<{7}|={7}|>{7})(?:\s|$)/m.test(text)) errors.push(`${name}: merge conflict marker`);
    for (const [index, line] of text.split('\n').entries()) {
      if (/[\t ]+$/.test(line)) errors.push(`${name}:${index + 1}: trailing whitespace`);
    }
    for (const match of text.matchAll(/!?\[[^\]\n]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)/g)) {
      const target = match[1];
      if (/^(https?:|mailto:)/i.test(target)) {
        try { new URL(target); } catch { errors.push(`${name}: invalid URL ${target}`); }
        continue;
      }
      if (/^[a-z][a-z\d+.-]*:/i.test(target) || /^\/home\//.test(target) || target.startsWith('//')) {
        errors.push(`${name}: private or unsupported link ${target}`);
        continue;
      }
      let decoded;
      try { decoded = decodeURIComponent(target); } catch {
        errors.push(`${name}: invalid URL encoding ${target}`);
        continue;
      }
      const hash = decoded.indexOf('#');
      const fragment = hash < 0 ? '' : decoded.slice(hash + 1);
      const pathname = (hash < 0 ? decoded : decoded.slice(0, hash)).split('?')[0];
      const resolved = pathname
        ? path.posix.normalize(pathname.startsWith('/') ? pathname.slice(1) : path.posix.join(path.posix.dirname(name), pathname))
        : name;
      if (resolved === '..' || resolved.startsWith('../')) {
        errors.push(`${name}: link escapes repository ${target}`);
      } else if (!files.has(resolved)) {
        errors.push(`${name}: missing target ${target}`);
      } else if (fragment && resolved.endsWith('.md') && !headings(files.get(resolved)).has(fragment)) {
        errors.push(`${name}: missing heading ${target}`);
      }
    }
  }
  return errors;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const names = execFileSync('git', ['ls-files', '--cached', '-z'], { cwd: root, encoding: 'utf8' }).split('\0').filter(Boolean);
  const files = new Map();
  for (const name of names) {
    if (!supported(name)) {
      files.set(name, '');
      continue;
    }
    const absolute = path.join(root, name);
    if (!lstatSync(absolute).isFile()) throw new Error(`Expected an ordinary tracked file: ${name}`);
    files.set(name, readFileSync(absolute, 'utf8'));
  }
  const errors = validateFoundation(files);
  if (errors.length) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
  } else {
    console.log(`PASS: ${files.size} tracked foundation files; documentation links, headings, formatting and scope.`);
  }
}
