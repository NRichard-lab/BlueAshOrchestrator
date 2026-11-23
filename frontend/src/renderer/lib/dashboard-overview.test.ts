// @vitest-environment node
import { describe, expect, it } from "vitest";
import { dashboardOverview } from "./dashboard-overview";
import { boardKanbanColumnOrder } from "./session-presentation";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

function session(overrides: Partial<WorkspaceSession> & { id: string }): WorkspaceSession {
	return {
		status: "working",
		updatedAt: "2026-11-20T10:00:00Z",
		prs: [],
		...overrides,
	} as WorkspaceSession;
}

function project(id: string, name: string, sessions: WorkspaceSession[]): WorkspaceSummary {
	return { id, name, sessions } as WorkspaceSummary;
}

const name = (workspace: WorkspaceSummary) => workspace.name;

describe("dashboardOverview", () => {
	it("groups active workers from every project into the board's lanes", () => {
		const overview = dashboardOverview(
			[
				project("web", "Web app", [
					session({ id: "a", kanbanColumn: "building", title: "Add search" }),
					session({ id: "b", kanbanColumn: "needs_review", title: "Fix login", status: "changes_requested" }),
				]),
				project("api", "API", [session({ id: "c", kanbanColumn: "building", title: "Cache layer" })]),
			],
			boardKanbanColumnOrder,
			name,
			"Untitled task",
		);

		expect(overview.activeCount).toBe(3);
		expect(overview.projectCount).toBe(2);
		expect(overview.stages.map((stage) => stage.column)).toEqual(boardKanbanColumnOrder);
		const building = overview.stages.find((stage) => stage.column === "building");
		expect(building?.workers.map((worker) => worker.sessionId).sort()).toEqual(["a", "c"]);
		// The project travels with the worker, or a cross-project list says nothing.
		expect(building?.workers.find((worker) => worker.sessionId === "c")?.projectName).toBe("API");
	});

	it("counts finished work instead of listing it as running", () => {
		const overview = dashboardOverview(
			[
				project("web", "Web app", [
					session({ id: "merged", status: "merged", kanbanColumn: "archive" }),
					session({ id: "live", kanbanColumn: "validating" }),
				]),
			],
			boardKanbanColumnOrder,
			name,
			"Untitled task",
		);

		expect(overview.completedCount).toBe(1);
		expect(overview.activeCount).toBe(1);
		expect(overview.workers.map((worker) => worker.sessionId)).toEqual(["live"]);
	});

	it("leaves out orchestrators and terminated sessions", () => {
		const overview = dashboardOverview(
			[
				project("web", "Web app", [
					session({ id: "web-orchestrator", kind: "orchestrator", kanbanColumn: "building" }),
					session({ id: "gone", kanbanColumn: "building", isTerminated: true }),
					session({ id: "worker", kanbanColumn: "building" }),
				]),
			],
			boardKanbanColumnOrder,
			name,
			"Untitled task",
		);

		expect(overview.workers.map((worker) => worker.sessionId)).toEqual(["worker"]);
	});

	it("carries the daemon's phase and falls back to no phase without one", () => {
		const overview = dashboardOverview(
			[
				project("web", "Web app", [
					session({ id: "phased", kanbanColumn: "validating", displayStatus: "Fixing CI failures" }),
					session({ id: "bare", kanbanColumn: "validating", displayStatus: "   " }),
				]),
			],
			boardKanbanColumnOrder,
			name,
			"Untitled task",
		);

		const workers = new Map(overview.workers.map((worker) => [worker.sessionId, worker]));
		expect(workers.get("phased")?.phase).toBe("Fixing CI failures");
		// Blank is not a phase; the row falls back to the status label instead.
		expect(workers.get("bare")?.phase).toBeUndefined();
	});

	it("names an untitled task and counts the ones needing a person", () => {
		const overview = dashboardOverview(
			[
				project("web", "Web app", [
					session({ id: "nameless", kanbanColumn: "building" }),
					session({ id: "blocked", kanbanColumn: "needs_review", status: "needs_input" }),
				]),
			],
			boardKanbanColumnOrder,
			name,
			"Untitled task",
		);

		expect(overview.workers.find((worker) => worker.sessionId === "nameless")?.title).toBe("Untitled task");
		expect(overview.attentionCount).toBe(1);
	});

	it("orders every lane by most recent activity", () => {
		const overview = dashboardOverview(
			[
				project("web", "Web app", [
					session({ id: "older", kanbanColumn: "building", updatedAt: "2026-11-19T08:00:00Z" }),
					session({ id: "newer", kanbanColumn: "building", updatedAt: "2026-11-21T08:00:00Z" }),
				]),
			],
			boardKanbanColumnOrder,
			name,
			"Untitled task",
		);

		expect(overview.workers.map((worker) => worker.sessionId)).toEqual(["newer", "older"]);
	});
});
