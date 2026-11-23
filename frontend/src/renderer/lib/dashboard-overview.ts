import {
	sessionIsActive,
	sessionNeedsAttention,
	toKanbanColumn,
	workerSessions,
	type KanbanColumn,
	type SessionStatus,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

/** One active worker, with the project it belongs to carried alongside it. */
export interface DashboardWorker {
	sessionId: string;
	projectId: string;
	projectName: string;
	hostId?: string;
	title: string;
	/** The lane the daemon put it in: building, validating, needs_review, ready. */
	stage: KanbanColumn;
	/**
	 * What is happening inside the stage, in the daemon's words ("Reviewing",
	 * "Fixing CI failures"). Absent from a daemon too old to derive one, which is
	 * why the stage label has to stand on its own.
	 */
	phase?: string;
	status: SessionStatus;
	needsAttention: boolean;
	updatedAt: string;
}

export interface DashboardStage {
	column: KanbanColumn;
	workers: DashboardWorker[];
}

export interface DashboardOverview {
	/** Active workers grouped by stage, in the order given to the function. */
	stages: DashboardStage[];
	/** Every active worker, newest first, across all projects. */
	workers: DashboardWorker[];
	activeCount: number;
	attentionCount: number;
	/** Workers whose delivery is finished: the lane the board calls done. */
	completedCount: number;
	/** Projects contributing at least one active worker. */
	projectCount: number;
}

const DONE: KanbanColumn = "archive";

function workerTitle(session: WorkspaceSession, fallback: string): string {
	const title = session.title?.trim();
	return title && title.length > 0 ? title : fallback;
}

/**
 * Fold every project's worker sessions into one cross-project view: who is
 * working, which stage they are in, and what phase that stage is at.
 *
 * Kept apart from the component so the grouping can be tested without a router
 * or a query client. The stage order is the caller's (the board's lane order),
 * so the dashboard and the board never disagree about which lane comes first.
 *
 * `projectName` resolves a project's display name — the standalone workspace
 * has a translated one rather than a stored one.
 */
export function dashboardOverview(
	workspaces: readonly WorkspaceSummary[],
	stageOrder: readonly KanbanColumn[],
	projectName: (workspace: WorkspaceSummary) => string,
	untitled: string,
): DashboardOverview {
	const active: DashboardWorker[] = [];
	let completedCount = 0;

	for (const workspace of workspaces) {
		for (const session of workerSessions(workspace.sessions ?? [])) {
			const stage = toKanbanColumn(session.kanbanColumn, session.status);
			// A finished worker is counted and then left out of the lanes: the
			// lanes answer "who is working now", which a merged task is not.
			if (stage === DONE) {
				completedCount++;
				continue;
			}
			if (!sessionIsActive(session)) continue;
			active.push({
				sessionId: session.id,
				projectId: workspace.id,
				projectName: projectName(workspace),
				hostId: workspace.hostId,
				title: workerTitle(session, untitled),
				stage,
				phase: session.displayStatus?.trim() || undefined,
				status: session.status,
				needsAttention: sessionNeedsAttention(session),
				updatedAt: session.updatedAt,
			});
		}
	}

	// Newest activity first, so the busiest work is at the top of every lane.
	active.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));

	return {
		stages: stageOrder.map((column) => ({ column, workers: active.filter((worker) => worker.stage === column) })),
		workers: active,
		activeCount: active.length,
		attentionCount: active.filter((worker) => worker.needsAttention).length,
		completedCount,
		projectCount: new Set(active.map((worker) => worker.projectId)).size,
	};
}
