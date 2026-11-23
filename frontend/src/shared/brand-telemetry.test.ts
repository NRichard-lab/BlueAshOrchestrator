// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";

// remoteTelemetryEnabled is a compile-time brand flag, separate from updatesEnabled: BlueAshAI sends no
// PostHog analytics and carries no Sentry crash-report DSN, whatever the environment says.
describe("remote telemetry by product profile", () => {
	afterEach(() => {
		vi.unstubAllEnvs();
		vi.resetModules();
	});

	it("legacy builds keep their packaged defaults", async () => {
		const telemetry = await import("./telemetry");
		expect(telemetry.rendererTelemetryEnabled({}, true)).toBe(true);
		expect(telemetry.rendererTelemetryEnabled({ AO_TELEMETRY_RENDERER: "off" }, true)).toBe(false);
	});

	it("BlueAshAI never enables renderer analytics, even when the environment asks for it", async () => {
		vi.resetModules();
		vi.stubEnv("AO_BRAND", "blueashai");
		const telemetry = await import("./telemetry");
		expect(telemetry.rendererTelemetryEnabled({}, true)).toBe(false);
		expect(telemetry.rendererTelemetryEnabled({ AO_TELEMETRY_RENDERER: "on" }, true)).toBe(false);
		expect(telemetry.rendererTelemetryEnabled({ AO_TELEMETRY_RENDERER: "on" }, false)).toBe(false);
	});

	it("BlueAshAI has no daemon crash-report DSN even for an explicit override or a published build", async () => {
		vi.resetModules();
		vi.stubEnv("AO_BRAND", "blueashai");
		vi.stubEnv("AO_PUBLISHED_BUILD", "1");
		const config = await import("./sentry-config");
		expect(config.daemonSentryDsn(undefined, "0.15.4", false)).toBe("");
		expect(config.daemonSentryDsn("https://example.invalid/1", "0.15.4", false)).toBe("");
	});

	it("legacy published builds still get the daemon DSN", async () => {
		vi.stubEnv("AO_PUBLISHED_BUILD", "1");
		const config = await import("./sentry-config");
		expect(config.daemonSentryDsn(undefined, "0.15.4", false)).toBe(config.DEFAULT_DAEMON_SENTRY_DSN);
	});
});
