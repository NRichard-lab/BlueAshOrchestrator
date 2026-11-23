// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";

const ORIGIN = "https://updates.example.test";

describe("checkDesktopVersionFloor", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  async function importModule(options: {
    isPackaged?: boolean;
    version?: string;
    floor?: { min?: string; latest?: string; downloadUrl?: string };
    fetchError?: boolean;
    fetchStatus?: number;
    origin?: string | undefined;
  } = {}) {
    const dialog = { showMessageBox: vi.fn(() => Promise.resolve({ response: 1 })) };
    const shellMock = { openExternal: vi.fn(() => Promise.resolve()) };
    const quit = vi.fn();
    vi.doMock("./update-origin", async (importOriginal) => {
      const actual = await importOriginal<typeof import("./update-origin")>();
      return { ...actual, readBakedUpdateOrigin: () => ("origin" in options ? options.origin : ORIGIN) };
    });
    vi.doMock("electron", () => ({
      app: {
        isPackaged: options.isPackaged ?? true,
        getVersion: () => options.version ?? "0.12.12",
        quit,
      },
      dialog,
      shell: shellMock,
    }));

    const fetchMock = vi.fn(async () => {
      if (options.fetchError) throw new Error("network error");
      return {
        ok: (options.fetchStatus ?? 200) === 200,
        status: options.fetchStatus ?? 200,
        json: async () => options.floor ?? { min: "", latest: "" },
      } as unknown as Response;
    });
    vi.stubGlobal("fetch", fetchMock);

    const mod = await import("./desktop-version-floor");
    return { mod, dialog, shellMock, quit, fetchMock };
  }

  it("does nothing when app is not packaged", async () => {
    const { mod, fetchMock } = await importModule({ isPackaged: false });
    await mod.checkDesktopVersionFloor();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("does nothing when floor is empty", async () => {
    const { mod, dialog } = await importModule({ floor: { min: "", latest: "" } });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });

  it("does nothing when fetch fails", async () => {
    const { mod, dialog } = await importModule({ fetchError: true });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });

  it("does nothing when fetch returns non-200", async () => {
    const { mod, dialog } = await importModule({ fetchStatus: 404 });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });

  it("does nothing when running version is at or above min", async () => {
    const { mod, dialog } = await importModule({
      version: "0.12.13",
      floor: { min: "0.12.13" },
    });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });

  it("shows a blocking dialog and quits when below min", async () => {
    const { mod, dialog, quit } = await importModule({
      version: "0.12.12",
      floor: { min: "0.12.13" },
    });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "warning",
        title: "Update Required",
      }),
    );
    expect(quit).toHaveBeenCalled();
  });

  it("attaches the dialog to the window and quits through the given quit", async () => {
    const { mod, dialog, quit } = await importModule({
      version: "0.12.12",
      floor: { min: "0.12.13" },
    });
    const parent = { isDestroyed: () => false };
    const quitCompletely = vi.fn();
    await mod.checkDesktopVersionFloor({ parent: () => parent as never, quit: quitCompletely });
    expect(dialog.showMessageBox).toHaveBeenCalledWith(parent, expect.objectContaining({ title: "Update Required" }));
    expect(quitCompletely).toHaveBeenCalled();
    expect(quit).not.toHaveBeenCalled();
  });

  it("asks again when the required-update quit was cancelled", async () => {
    vi.useFakeTimers();
    try {
      const { mod, dialog, quit } = await importModule({
        version: "0.12.12",
        floor: { min: "0.12.13" },
      });
      await mod.checkDesktopVersionFloor();
      expect(dialog.showMessageBox).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(60_000);
      expect(dialog.showMessageBox).toHaveBeenCalledTimes(2);
      expect(quit).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the update nudge without a parent once the window is gone", async () => {
    const { mod, dialog } = await importModule({
      version: "0.12.12",
      floor: { latest: "0.12.13" },
    });
    await mod.checkDesktopVersionFloor({ parent: () => ({ isDestroyed: () => true }) as never });
    expect(dialog.showMessageBox).toHaveBeenCalledWith(expect.objectContaining({ title: "Update Available" }));
  });

  it("opens an on-origin download URL when user clicks Download on required update", async () => {
    const { mod, dialog, shellMock, quit } = await importModule({
      version: "0.12.12",
      floor: { min: "0.12.13", downloadUrl: `${ORIGIN}/download/v0.12.13` },
    });
    dialog.showMessageBox.mockResolvedValue({ response: 0 });
    await mod.checkDesktopVersionFloor();
    expect(shellMock.openExternal).toHaveBeenCalledWith(`${ORIGIN}/download/v0.12.13`);
    expect(quit).toHaveBeenCalled();
  });

  it("reads the floor only from the baked origin", async () => {
    const { mod, fetchMock } = await importModule({ floor: { min: "", latest: "" } });
    await mod.checkDesktopVersionFloor();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe(`${ORIGIN}/policy/desktop-version-floor.json`);
  });

  it("makes no request and shows nothing when no update origin is baked", async () => {
    const { mod, fetchMock, dialog } = await importModule({ origin: undefined, version: "0.0.1", floor: { min: "9.9.9" } });
    await mod.checkDesktopVersionFloor();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });

  it.each([
    "https://github.com/Untrivial-ai/agent-orchestrator/releases/latest",
    "https://evil.example.test/download",
    "http://updates.example.test/download",
    "https://updates.example.test.evil.test/download",
  ])("replaces a foreign downloadUrl (%s) with the origin download page, never an upstream default", async (downloadUrl) => {
    const { mod, dialog, shellMock } = await importModule({
      version: "0.12.12",
      floor: { min: "0.12.13", downloadUrl },
    });
    dialog.showMessageBox.mockResolvedValue({ response: 0 });
    await mod.checkDesktopVersionFloor();
    expect(shellMock.openExternal).toHaveBeenCalledWith(`${ORIGIN}/download/`);
  });

  it("shows a dismissible dialog when below latest but at or above min", async () => {
    const { mod, dialog, quit } = await importModule({
      version: "0.12.12",
      floor: { min: "0.12.11", latest: "0.12.13" },
    });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "info",
        title: "Update Available",
      }),
    );
    expect(quit).not.toHaveBeenCalled();
  });

  it("does not show the latest nudge when at or above latest", async () => {
    const { mod, dialog } = await importModule({
      version: "0.12.14",
      floor: { latest: "0.12.13" },
    });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });

  it("ignores malformed version strings in the floor", async () => {
    const { mod, dialog } = await importModule({
      version: "0.12.12",
      floor: { min: "not-a-version", latest: "also bad" },
    });
    await mod.checkDesktopVersionFloor();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });
});

describe("checkDesktopVersionFloor in a BlueAshAI build", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("never fetches the floor and never prompts, even with a baked origin", async () => {
    vi.resetModules();
    vi.stubEnv("AO_BRAND", "blueashai");
    const dialog = { showMessageBox: vi.fn() };
    vi.doMock("./update-origin", async (importOriginal) => ({ ...(await importOriginal<typeof import("./update-origin")>()), readBakedUpdateOrigin: () => ORIGIN }));
    vi.doMock("electron", () => ({ app: { isPackaged: true, getVersion: () => "0.0.1", quit: vi.fn() }, dialog, shell: { openExternal: vi.fn() } }));
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const mod = await import("./desktop-version-floor");
    await mod.checkDesktopVersionFloor();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
  });
});
