import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { RULES, scanBuildDir, scanText } from './scan-bundle.mjs';

// Planted values are assembled at runtime so this file itself never contains a secret-like literal.
const planted = {
  providerKey: ['s', 'k-', 'proj-', 'A1b2C3d4E5f6G7h8I9j0K1l2'].join(''),
  livekitSecretName: ['LIVEKIT', 'API', 'SECRET'].join('_'),
  apiSecret: ['api', 'secret'].join('_'),
  pem: ['-----BEGIN', 'PRIVATE', 'KEY-----'].join(' '),
  devSecret: ['ati', 'dev', 'livekit-secret'].join('-'),
};

const dirs: string[] = [];

async function buildDir(files: Record<string, string>): Promise<string> {
  const root = await mkdtemp(path.join(tmpdir(), 'scan-bundle-'));
  dirs.push(root);
  for (const [relative, content] of Object.entries(files)) {
    const full = path.join(root, relative);
    await mkdir(path.dirname(full), { recursive: true });
    await writeFile(full, content);
  }
  return root;
}

afterEach(async () => {
  await Promise.all(dirs.splice(0).map((d) => rm(d, { recursive: true, force: true })));
});

describe('scan-bundle', () => {
  it.each(Object.entries(planted))('detects a planted %s', async (_name, value) => {
    const root = await buildDir({ 'static/chunks/app.js': `var a=1;\nvar cfg="${value}";\n` });
    const { findings } = await scanBuildDir(root);
    expect(findings.length).toBeGreaterThan(0);
    expect(findings[0]).toMatchObject({ file: path.join('static', 'chunks', 'app.js'), line: 2 });
  });

  it('flags server-only configuration names in browser assets only', async () => {
    const name = ['ATI', 'CONTROL', 'PLANE', 'URL'].join('_');
    const root = await buildDir({ 'static/chunks/a.js': `x="${name}"`, 'server/app/page.js': `x="${name}"` });
    const { findings } = await scanBuildDir(root);
    expect(findings).toEqual([{ file: path.join('static', 'chunks', 'a.js'), line: 1, rule: 'server-env-in-client' }]);
  });

  it('does not fire on look-alikes in minified code', () => {
    const text = 'a{-webkit-mask-image:none}.desk-top{}var task-runner="x";t="sk-SK";n="risk-free"';
    expect(scanText(text, 'x.css', RULES)).toEqual([]);
  });

  it('passes a clean build and skips the compiler cache', async () => {
    const root = await buildDir({
      'static/chunks/app.js': 'console.log("ok")',
      'server/app/page.js': 'module.exports={}',
      [path.join('cache', 'turbopack', 'blob')]: `"${planted.providerKey}"`,
    });
    const { files, findings } = await scanBuildDir(root);
    expect(findings).toEqual([]);
    expect(files).toBe(2);
  });

  it('fails when there is no build output', async () => {
    await expect(scanBuildDir(path.join(tmpdir(), 'definitely-missing-build-dir'))).rejects.toThrow(/No build output/);
  });
});
