import { describe, expect, it } from "vitest";
import { createProjectConfig } from "../routes/_shell";

describe("createProjectConfig", () => {
	it("persists selected worker and orchestrator agents without tracker intake by default", () => {
		expect(
			createProjectConfig({
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
			}),
		).toEqual({
			worker: { agent: "codex" },
			orchestrator: { agent: "claude-code" },
		});
	});

	it("preserves tracker intake alongside selected agent defaults", () => {
		expect(
			createProjectConfig({
				workerAgent: "cursor",
				orchestratorAgent: "opencode",
				trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
			}),
		).toEqual({
			worker: { agent: "cursor" },
			orchestrator: { agent: "opencode" },
			trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
		});
	});

	it("sends no workers list when only one worker was chosen", () => {
		// A project on a single harness must serialize exactly as before, or every
		// existing project would gain a redundant field on its next edit.
		const config = createProjectConfig({
			workerAgent: "codex",
			orchestratorAgent: "claude-code",
			extraWorkerAgents: [],
		});
		expect(config).not.toHaveProperty("workers");
	});

	it("leads the workers list with the default worker", () => {
		expect(
			createProjectConfig({
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
				extraWorkerAgents: ["claude-code", "aider"],
			}),
		).toEqual({
			worker: { agent: "codex" },
			// The default first, so the daemon's ActiveWorkers order matches the picker.
			workers: [{ agent: "codex" }, { agent: "claude-code" }, { agent: "aider" }],
			orchestrator: { agent: "claude-code" },
		});
	});

	it("never repeats the default or carries a blank among the extras", () => {
		const config = createProjectConfig({
			workerAgent: "codex",
			orchestratorAgent: "claude-code",
			extraWorkerAgents: ["codex", "", "aider", "aider"],
		});
		expect(config.workers).toEqual([{ agent: "codex" }, { agent: "aider" }]);
	});
});
