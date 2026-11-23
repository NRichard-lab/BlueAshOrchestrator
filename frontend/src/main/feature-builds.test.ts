// @vitest-environment node
import { describe, it, expect, vi, afterEach } from "vitest";
import { listFeatureBuilds, parseFeatureBuild, reconcileFeaturePin } from "./feature-builds";
import type { UpdateSettings } from "./update-settings";

const ORIGIN = "https://updates.example.test";
let bakedOrigin: string | undefined = ORIGIN;

// Mock the electron module so `app` works outside Electron.
vi.mock("electron", () => ({
	app: { getVersion: () => "0.2.0", isPackaged: true },
}));
vi.mock("./update-origin", async (importOriginal) => {
	const actual = await importOriginal<typeof import("./update-origin")>();
	return { ...actual, readBakedUpdateOrigin: () => bakedOrigin };
});

const DAY_MS = 24 * 60 * 60 * 1000;

function makeBuild(overrides: Record<string, unknown> = {}) {
	return {
		pr: 2270,
		title: "Feature build pr2270",
		base: "main",
		sha: "abc1234",
		slug: "pr2270",
		buildId: "0.2.0-pr2270.202607061200",
		publishedAt: new Date(Date.now() - DAY_MS).toISOString(),
		...overrides,
	};
}

function stubIndex(builds: unknown, init: { ok?: boolean; status?: number } = {}) {
	const fetchMock = vi.fn(async (_url: string) => ({
		ok: init.ok ?? true,
		status: init.status ?? 200,
		json: async () => (Array.isArray(builds) ? { builds } : builds),
	}));
	vi.stubGlobal("fetch", fetchMock);
	return fetchMock;
}

describe("parseFeatureBuild", () => {
	it("returns { pr } for a feature-build version", () => {
		expect(parseFeatureBuild("0.2.0-pr2270.202607061200+abc1234")).toEqual({ pr: 2270 });
	});

	it("strips a leading v", () => {
		expect(parseFeatureBuild("v0.2.0-pr2270.202607061200")).toEqual({ pr: 2270 });
	});

	it("returns null for stable, nightly and empty versions", () => {
		expect(parseFeatureBuild("0.2.0")).toBeNull();
		expect(parseFeatureBuild("0.2.0-nightly.202607061200")).toBeNull();
		expect(parseFeatureBuild("")).toBeNull();
	});
});

describe("listFeatureBuilds", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
		bakedOrigin = ORIGIN;
	});

	it("reads only the feature index on the baked update origin", async () => {
		const fetchMock = stubIndex([makeBuild()]);
		await listFeatureBuilds();
		expect(fetchMock).toHaveBeenCalledTimes(1);
		expect(fetchMock.mock.calls[0][0]).toBe(`${ORIGIN}/desktop/feature/index.json`);
	});

	it("returns [] and makes no request when no update origin is baked", async () => {
		bakedOrigin = undefined;
		const fetchMock = stubIndex([makeBuild()]);
		expect(await listFeatureBuilds()).toEqual([]);
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("returns [] when the fetch throws or is not ok", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("network error"); }));
		expect(await listFeatureBuilds()).toEqual([]);
		stubIndex([], { ok: false, status: 500 });
		expect(await listFeatureBuilds()).toEqual([]);
	});

	it("returns [] for a malformed index", async () => {
		stubIndex({ nope: true });
		expect(await listFeatureBuilds()).toEqual([]);
	});

	it("drops malformed entries", async () => {
		stubIndex([makeBuild({ pr: "x" }), makeBuild({ pr: -1 }), makeBuild({ publishedAt: "nope" }), null, makeBuild({ pr: 7, buildId: "b7" })]);
		expect((await listFeatureBuilds()).map((b) => b.pr)).toEqual([7]);
	});

	it("excludes builds published more than 7 days ago and keeps ones inside the window", async () => {
		stubIndex([
			makeBuild({ pr: 1, publishedAt: new Date(Date.now() - 8 * DAY_MS).toISOString() }),
			makeBuild({ pr: 2, publishedAt: new Date(Date.now() - 6.9 * DAY_MS).toISOString() }),
		]);
		expect((await listFeatureBuilds()).map((b) => b.pr)).toEqual([2]);
	});

	it("returns a FeatureBuild with the expected shape", async () => {
		stubIndex([makeBuild()]);
		const [b] = await listFeatureBuilds();
		expect(b).toMatchObject({ pr: 2270, title: "Feature build pr2270", base: "main", sha: "abc1234", slug: "pr2270", buildId: "0.2.0-pr2270.202607061200" });
		expect(typeof b.publishedAt).toBe("string");
	});

	it("groups multiple builds of the same PR, keeping only the newest", async () => {
		stubIndex([
			makeBuild({ buildId: "old", publishedAt: new Date(Date.now() - 3 * DAY_MS).toISOString() }),
			makeBuild({ buildId: "new", publishedAt: new Date(Date.now() - 1 * DAY_MS).toISOString() }),
		]);
		const r = await listFeatureBuilds();
		expect(r).toHaveLength(1);
		expect(r[0].buildId).toBe("new");
	});

	it("sorts results newest-first across PRs", async () => {
		stubIndex([
			makeBuild({ pr: 1, publishedAt: new Date(Date.now() - 3 * DAY_MS).toISOString() }),
			makeBuild({ pr: 2, publishedAt: new Date(Date.now() - 1 * DAY_MS).toISOString() }),
			makeBuild({ pr: 3, publishedAt: new Date(Date.now() - 2 * DAY_MS).toISOString() }),
		]);
		expect((await listFeatureBuilds()).map((b) => b.pr)).toEqual([2, 3, 1]);
	});
});

describe("reconcileFeaturePin", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
		bakedOrigin = ORIGIN;
	});

	const pinned = (pr: number): UpdateSettings => ({
		enabled: true,
		channel: "latest",
		nightlyAck: false,
		feature: { pr },
	});

	it("returns unchanged when there is no pin", async () => {
		const settings: UpdateSettings = { enabled: true, channel: "nightly", nightlyAck: true, feature: null };
		expect(await reconcileFeaturePin(settings)).toEqual({ settings, cleared: false });
	});

	it("keeps the pin when the PR still has a live build", async () => {
		stubIndex([makeBuild()]);
		const r = await reconcileFeaturePin(pinned(2270));
		expect(r.cleared).toBe(false);
		expect(r.settings.feature).toEqual({ pr: 2270 });
	});

	it("clears the pin (preserving the home channel) when the PR has no live build", async () => {
		stubIndex([]);
		const r = await reconcileFeaturePin(pinned(2270));
		expect(r.cleared).toBe(true);
		expect(r.settings.feature).toBeNull();
		expect(r.settings.channel).toBe("latest");
	});

	it("clears the pin when a different PR is live but the pinned one is not", async () => {
		stubIndex([makeBuild({ pr: 999 })]);
		expect((await reconcileFeaturePin(pinned(2270))).cleared).toBe(true);
	});

	it("keeps the pin on a fetch error or when no origin is configured", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("network error"); }));
		expect((await reconcileFeaturePin(pinned(2270))).cleared).toBe(false);
		bakedOrigin = undefined;
		expect((await reconcileFeaturePin(pinned(2270))).cleared).toBe(false);
	});
});
