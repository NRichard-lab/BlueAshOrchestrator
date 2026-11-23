// @vitest-environment node
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  defaultDownloadUrl,
  desktopFeedUrl,
  featureIndexUrl,
  floorUrl,
  isUrlOnOrigin,
  originFromAppUpdateYml,
  parseUpdateOrigin,
  readBakedUpdateOrigin,
  renderAppUpdateYml,
} from "./update-origin";

const ORIGIN = "https://updates.example.test";

describe("parseUpdateOrigin", () => {
  it("accepts a bare https origin and normalizes the trailing slash", () => {
    expect(parseUpdateOrigin("https://updates.example.test")).toEqual({ ok: true, origin: ORIGIN });
    expect(parseUpdateOrigin(" https://Updates.Example.test/ ")).toEqual({ ok: true, origin: ORIGIN });
    expect(parseUpdateOrigin("https://updates.example.test:8443")).toEqual({
      ok: true,
      origin: "https://updates.example.test:8443",
    });
  });

  it.each([
    [undefined, "not set"],
    ["", "not set"],
    ["not a url", "valid URL"],
    ["http://updates.example.test", "https"],
    ["ftp://updates.example.test", "https"],
    ["https://user:pw@updates.example.test", "credentials"],
    ["https://updates.example.test/path", "path"],
    ["https://updates.example.test/?a=1", "query"],
    ["https://updates.example.test/#x", "fragment"],
    ["https://localhost", "public DNS"],
    ["https://127.0.0.1", "public DNS"],
    ["https://[::1]", "public DNS"],
    ["https://intranet", "fully qualified"],
    ["https://github.com", "upstream"],
    ["https://raw.githubusercontent.com", "upstream"],
    ["https://api.github.com", "upstream"],
    ["https://orchestrator.inc", "upstream"],
    ["https://auth.orchestrator.inc", "upstream"],
    ["https://u.expo.dev", "upstream"],
    ["https://someone.github.io", "upstream"],
  ])("rejects %s", (raw, reason) => {
    const result = parseUpdateOrigin(raw);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.reason).toContain(reason);
  });

  it("does not treat a look-alike suffix as an upstream host", () => {
    expect(parseUpdateOrigin("https://notgithub.com").ok).toBe(true);
  });
});

describe("url builders", () => {
  it("lays out the channel tree", () => {
    expect(desktopFeedUrl(ORIGIN, { channel: "latest" })).toBe(`${ORIGIN}/desktop/stable/`);
    expect(desktopFeedUrl(ORIGIN, { channel: "nightly" })).toBe(`${ORIGIN}/desktop/nightly/`);
    expect(desktopFeedUrl(ORIGIN, { channel: "latest", feature: { pr: 42 } })).toBe(`${ORIGIN}/desktop/feature/pr42/`);
    expect(floorUrl(ORIGIN)).toBe(`${ORIGIN}/policy/desktop-version-floor.json`);
    expect(featureIndexUrl(ORIGIN)).toBe(`${ORIGIN}/desktop/feature/index.json`);
    expect(defaultDownloadUrl(ORIGIN)).toBe(`${ORIGIN}/download/`);
  });

  it("rejects an invalid feature pin", () => {
    expect(() => desktopFeedUrl(ORIGIN, { channel: "latest", feature: { pr: 0 } })).toThrow();
    expect(() => desktopFeedUrl(ORIGIN, { channel: "latest", feature: { pr: 1.5 } })).toThrow();
  });
});

describe("isUrlOnOrigin", () => {
  it("accepts only https URLs on exactly the origin", () => {
    expect(isUrlOnOrigin(`${ORIGIN}/download/`, ORIGIN)).toBe(true);
    expect(isUrlOnOrigin("http://updates.example.test/download/", ORIGIN)).toBe(false);
    expect(isUrlOnOrigin("https://updates.example.test.evil.test/", ORIGIN)).toBe(false);
    expect(isUrlOnOrigin("https://user@updates.example.test/", ORIGIN)).toBe(false);
    expect(isUrlOnOrigin("https://github.com/x", ORIGIN)).toBe(false);
    expect(isUrlOnOrigin(undefined, ORIGIN)).toBe(false);
    expect(isUrlOnOrigin("nope", ORIGIN)).toBe(false);
  });
});

describe("app-update.yml", () => {
  it("round-trips the origin", () => {
    const yml = renderAppUpdateYml(ORIGIN, "agent-orchestrator-updater");
    expect(yml).toContain("provider: generic");
    expect(yml).not.toMatch(/github|owner:|repo:/i);
    expect(originFromAppUpdateYml(yml)).toBe(ORIGIN);
  });

  it("rejects the legacy github provider and invalid urls", () => {
    expect(originFromAppUpdateYml("provider: github\nowner: x\nrepo: y\n")).toBeUndefined();
    expect(originFromAppUpdateYml("provider: generic\nurl: https://github.com/desktop/stable/\n")).toBeUndefined();
    expect(originFromAppUpdateYml("provider: generic\nurl: http://updates.example.test/desktop/stable/\n")).toBeUndefined();
    expect(originFromAppUpdateYml("provider: generic\nurl: https://updates.example.test/other/\n")).toBeUndefined();
    expect(originFromAppUpdateYml("")).toBeUndefined();
  });

  describe("readBakedUpdateOrigin", () => {
    let dir: string | undefined;
    afterEach(() => {
      if (dir) rmSync(dir, { recursive: true, force: true });
      dir = undefined;
    });

    it("reads the origin from resources and returns undefined when missing or legacy", () => {
      dir = mkdtempSync(join(tmpdir(), "ao-origin-"));
      expect(readBakedUpdateOrigin(dir)).toBeUndefined();
      writeFileSync(join(dir, "app-update.yml"), renderAppUpdateYml(ORIGIN, "x"));
      expect(readBakedUpdateOrigin(dir)).toBe(ORIGIN);
      writeFileSync(join(dir, "app-update.yml"), "provider: github\nowner: a\nrepo: b\n");
      expect(readBakedUpdateOrigin(dir)).toBeUndefined();
    });

    it("a BlueAshAI build has no update origin even when a valid feed file ships", async () => {
      dir = mkdtempSync(join(tmpdir(), "ao-origin-"));
      writeFileSync(join(dir, "app-update.yml"), renderAppUpdateYml(ORIGIN, "x"));
      vi.resetModules();
      vi.stubEnv("AO_BRAND", "blueashai");
      try {
        const blue = await import("./update-origin");
        expect(blue.readBakedUpdateOrigin(dir)).toBeUndefined();
      } finally {
        vi.unstubAllEnvs();
        vi.resetModules();
      }
    });
  });
});
