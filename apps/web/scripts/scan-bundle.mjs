#!/usr/bin/env node
/**
 * Fails when the Next.js build output contains secret-like material.
 *
 * Usage: node scripts/scan-bundle.mjs [buildDir]   (default: .next next to this package)
 *
 * Scans every file under the build directory except the compiler cache (.next/cache) and the
 * dev server output (.next/dev), which are not build output. Prints only the file, line and
 * rule that matched, never the matched text. Exits 1 on any finding or if no build exists.
 */
import { readdir, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

/** Secret patterns. Kept specific enough not to fire on minified identifiers (e.g. "mask-image"). */
export const RULES = [
  { id: 'provider-secret-key', description: 'sk-… style API secret key', pattern: /\bsk-[A-Za-z0-9][A-Za-z0-9_-]{15,}/ },
  { id: 'livekit-api-secret', description: 'LIVEKIT_API_SECRET reference', pattern: /LIVEKIT_API_SECRET/ },
  { id: 'api-secret', description: 'api_secret reference', pattern: /api_secret/i },
  { id: 'private-key', description: 'PEM private key', pattern: /BEGIN [A-Z ]*PRIVATE KEY/ },
  { id: 'dev-secret', description: 'development secret prefix (ati-dev-)', pattern: /ati-dev-/ },
  { id: 'openai-env', description: 'OPENAI_API_KEY reference', pattern: /OPENAI_API_KEY/ },
  { id: 'aws-access-key', description: 'AWS access key ID', pattern: /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/ },
  { id: 'jwt', description: 'JSON Web Token', pattern: /\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/ },
];

/** Server-side configuration names that must never be inlined into browser assets. */
export const CLIENT_ONLY_RULES = [
  { id: 'server-env-in-client', description: 'server-only env var name in a browser asset', pattern: /ATI_CONTROL_PLANE_URL|ATI_LIVEKIT_PUBLIC_URL|ATI_LIVEKIT_API_KEY|ATI_DATABASE_URL/ },
];

const SKIP_DIRS = new Set(['cache', 'dev']);
const MAX_FILE_BYTES = 64 * 1024 * 1024;

async function* walk(dir, root) {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (dir === root && SKIP_DIRS.has(entry.name)) continue;
      yield* walk(full, root);
    } else if (entry.isFile()) {
      yield full;
    }
  }
}

/** Returns findings `{ file, line, rule }` for one file's text. */
export function scanText(text, file, rules) {
  const findings = [];
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    for (const rule of rules) {
      if (rule.pattern.test(line)) findings.push({ file, line: i + 1, rule: rule.id });
    }
  }
  return findings;
}

/** Scans a build directory. Browser assets (static/) are also checked against CLIENT_ONLY_RULES. */
export async function scanBuildDir(buildDir) {
  const root = path.resolve(buildDir);
  const info = await stat(root).catch(() => null);
  if (!info || !info.isDirectory()) {
    throw new Error(`No build output at ${root}. Run \`pnpm build\` first.`);
  }
  const findings = [];
  let files = 0;
  for await (const file of walk(root, root)) {
    const size = (await stat(file)).size;
    if (size > MAX_FILE_BYTES) {
      findings.push({ file: path.relative(root, file), line: 0, rule: 'file-too-large-to-scan' });
      continue;
    }
    files += 1;
    // latin1 keeps every byte, so binary files are scanned too without decoding errors.
    const text = await readFile(file, 'latin1');
    const relative = path.relative(root, file);
    const isBrowserAsset = relative.split(path.sep)[0] === 'static';
    findings.push(...scanText(text, relative, isBrowserAsset ? [...RULES, ...CLIENT_ONLY_RULES] : RULES));
  }
  return { files, findings };
}

async function main() {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const buildDir = process.argv[2] ?? path.join(here, '..', '.next');
  try {
    const { files, findings } = await scanBuildDir(buildDir);
    if (files === 0) {
      console.error('scan-bundle: the build directory contains no files.');
      process.exit(1);
    }
    if (findings.length > 0) {
      for (const f of findings) console.error(`scan-bundle: ${f.file}:${f.line}: ${f.rule}`);
      console.error(`scan-bundle: ${findings.length} finding(s) in ${files} files. Failing.`);
      process.exit(1);
    }
    console.log(`scan-bundle: ok (${files} files scanned, ${RULES.length + CLIENT_ONLY_RULES.length} rules)`);
  } catch (error) {
    console.error(`scan-bundle: ${error instanceof Error ? error.message : String(error)}`);
    process.exit(1);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
