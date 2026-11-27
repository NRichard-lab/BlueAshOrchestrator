// The product-identity values the bare-node build scripts stamp into macOS and Chrome artifacts.
//
//   node --test scripts/brand-packaging.node-test.mjs
//
// These scripts run under plain node (a "#!" line and all), which the vitest transform cannot
// parse, so their behavior is asserted here; makers/brand-identity.test.ts covers the rest.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { brandProfile, brandDefine, brands } from './brand-profile.mjs';
import { APP_BUNDLE_NAME, APP_ID, APP_NAME, VOLUME_NAME } from './build-mac-dmg.mjs';
import { dictationHelperPlist } from './build-dictation-helper.mjs';
import { SOURCE_BRAND_NAME } from './build-browser-extension.mjs';

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel) => readFileSync(path.join(frontend, rel), 'utf8');
const LEGACY = brands.legacy;
const BLUEASH = brands.blueashai;

test('the profile is chosen at build time, from the same JSON the app compiles in', () => {
  assert.deepEqual(brandProfile({ AO_BRAND: 'blueashai' }), BLUEASH);
  for (const env of [{}, { AO_BRAND: '' }, { AO_BRAND: 'evil' }]) assert.deepEqual(brandProfile(env), LEGACY);
  // Same substitution as vite.brand.ts, so a script that bundles brand.ts compiles the same profile.
  assert.deepEqual(brandDefine({ AO_BRAND: 'blueashai' }), { 'process.env.AO_BRAND': '"blueashai"' });
  assert.deepEqual(brandDefine({}), { 'process.env.AO_BRAND': '""' });
});

test('the macOS install window wraps this product\'s bundle under its own identifier', () => {
  // These tests run without AO_BRAND, so the values are the legacy product's.
  assert.equal(APP_NAME, LEGACY.packagerName);
  assert.equal(APP_BUNDLE_NAME, `${LEGACY.packagerName}.app`);
  assert.equal(APP_ID, LEGACY.appId);
  assert.equal(VOLUME_NAME, LEGACY.displayName);
  // Nothing in the builder may name the other product.
  assert.ok(!APP_BUNDLE_NAME.includes(BLUEASH.packagerName));
  assert.notEqual(APP_ID, BLUEASH.appId);
});

test('the dictation helper carries this product\'s identifier and name, keeping its executable', () => {
  const template = read('native/dictation-helper/Info.plist');
  const plist = dictationHelperPlist(template, BLUEASH);
  assert.ok(plist.includes(`<string>${BLUEASH.appId}.dictation</string>`), plist);
  assert.ok(!plist.includes(LEGACY.appId), 'the other product\'s identifier survived');
  assert.ok(plist.includes(`<string>${BLUEASH.displayName} uses the microphone to dictate messages.</string>`), plist);
  assert.ok(plist.includes(`<string>${BLUEASH.displayName} turns your speech into message text.</string>`), plist);
  // CFBundleExecutable must keep naming the compiled file inside the bundle, whatever the product.
  assert.match(plist, /<key>CFBundleExecutable<\/key>\s*<string>Orchestrator<\/string>/);
  // A legacy build leaves its own template alone.
  assert.ok(dictationHelperPlist(template, LEGACY).includes(`<string>${LEGACY.appId}.dictation</string>`));
});

test('the Chrome extension is built for the product that ships it', () => {
  const manifest = JSON.parse(read('src/browser-extension/manifest.json'));
  assert.equal(SOURCE_BRAND_NAME, 'Orchestrator');
  assert.ok(manifest.name.includes(SOURCE_BRAND_NAME), manifest.name);
  // The host name itself is compiled in from src/shared/brand.ts (brandDefine above).
  const script = read('scripts/build-browser-extension.mjs');
  assert.ok(script.includes('define: brandDefine()'), script);
  assert.ok(script.includes('SOURCE_BRAND_NAME'), script);
  assert.notEqual(BLUEASH.nativeMessagingHost, LEGACY.nativeMessagingHost);
});
