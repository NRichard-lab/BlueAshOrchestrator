import { useNavigate } from "@tanstack/react-router";
import { LayoutDashboard, TriangleAlert } from "lucide-react";
import { ScrollArea } from "radix-ui";
import { useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { dashboardOverview, type DashboardWorker } from "../../lib/dashboard-overview";
import { boardKanbanColumnOrder, getKanbanColumnView, getSessionStatusView } from "../../lib/session-presentation";
import { cn } from "../../lib/utils";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../../types/workspace";
import { PageTopbar, PageTopbarTitle } from "../PageTopbar";

function Tile({ label, value, tone }: { label: string; value: number; tone?: string }) {
	return (
		<div className="flex flex-col gap-1">
			<span className="text-xs text-muted-foreground">{label}</span>
			<span className={cn("text-heading-lg font-semibold tabular-nums text-foreground", tone)}>{value}</span>
		</div>
	);
}

export function DashboardView() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	// Cloud projects included: a worker running there is as active as a local one.
	const workspaces = useWorkspaceQuery({ includeCloud: true }).data;

	// The same three routes the sidebar opens a worker through: a remote host's
	// session lives under its host, and the standalone workspace has no project
	// segment at all.
	const openWorker = useCallback(
		(worker: DashboardWorker) => {
			const { hostId, projectId, sessionId } = worker;
			if (hostId) {
				void navigate({
					to: "/host/$hostId/project/$projectId/session/$sessionId",
					params: { hostId, projectId, sessionId },
				});
				return;
			}
			if (projectId === STANDALONE_WORKSPACE_ID) {
				void navigate({ to: "/sessions/$sessionId", params: { sessionId } });
				return;
			}
			void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } });
		},
		[navigate],
	);

	const projectName = (workspace: WorkspaceSummary) =>
		workspace.id === STANDALONE_WORKSPACE_ID ? t("standalone.workspaceName") : workspace.name;

	const overview = useMemo(
		() => dashboardOverview(workspaces ?? [], boardKanbanColumnOrder, projectName, t("dashboard.untitledTask")),
		// projectName closes over t only, which changes with the language.
		[workspaces, t],
	);

	const columns = useMemo(
		() => new Map(boardKanbanColumnOrder.map((column) => [column, getKanbanColumnView(column, t)])),
		[t],
	);

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar
				lead={
					<PageTopbarTitle as="h1" icon={<LayoutDashboard className="size-icon-md" aria-hidden="true" />}>
						{t("dashboard.title")}
					</PageTopbarTitle>
				}
			/>
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none">
						<div className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-6 pt-5 pb-6">
							<section className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-4" aria-label={t("dashboard.totals")}>
								<Tile label={t("dashboard.activeWorkers")} value={overview.activeCount} />
								<Tile label={t("dashboard.projects")} value={overview.projectCount} />
								<Tile
									label={t("dashboard.needsAttention")}
									value={overview.attentionCount}
									tone={overview.attentionCount > 0 ? "text-warning" : undefined}
								/>
								<Tile label={t("dashboard.completed")} value={overview.completedCount} />
							</section>

							{overview.activeCount === 0 ? (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("dashboard.empty.title")}</p>
									<p className="max-w-sm text-xs text-muted-foreground">{t("dashboard.empty.body")}</p>
								</div>
							) : (
								<div className="flex flex-col gap-8">
									{overview.stages.map((stage) => {
										const view = columns.get(stage.column);
										return (
											<section
												key={stage.column}
												aria-labelledby={`dashboard-stage-${stage.column}`}
												className="flex flex-col gap-3"
											>
												<div className="flex items-center gap-2">
													<span aria-hidden="true" className={cn("size-2 rounded-full", view?.dotClassName)} />
													<h2
														id={`dashboard-stage-${stage.column}`}
														className={cn("text-control font-medium", view?.titleClassName)}
													>
														{view?.label ?? stage.column}
													</h2>
													<span className="text-xs tabular-nums text-muted-foreground">{stage.workers.length}</span>
												</div>
												{stage.workers.length === 0 ? (
													<p className="text-xs text-muted-foreground">{t("dashboard.stageEmpty")}</p>
												) : (
													<ul className="flex flex-col gap-1">
														{stage.workers.map((worker) => (
															<WorkerRow
																key={`${worker.projectId}:${worker.sessionId}`}
																worker={worker}
																onOpen={() => openWorker(worker)}
															/>
														))}
													</ul>
												)}
											</section>
										);
									})}
								</div>
							)}
						</div>
					</ScrollArea.Viewport>
					<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
						<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
					</ScrollArea.Scrollbar>
				</main>
			</ScrollArea.Root>
		</div>
	);
}

function WorkerRow({ worker, onOpen }: { worker: DashboardWorker; onOpen: () => void }) {
	const { t } = useTranslation();
	// The daemon's phrase when it sent one; the status label is the fallback a
	// daemon too old to derive a phase leaves us with.
	const phase = worker.phase ?? getSessionStatusView(worker.status, t).label;
	return (
		<li>
			<button
				type="button"
				onClick={onOpen}
				className="flex w-full min-w-0 items-center gap-3 rounded-md px-2 py-1.5 text-start hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
			>
				{worker.needsAttention ? (
					<TriangleAlert aria-label={t("dashboard.needsAttention")} className="size-icon-sm shrink-0 text-warning" />
				) : null}
				<span className="min-w-0 flex-1 truncate text-control text-foreground">{worker.title}</span>
				<span className="hidden min-w-0 max-w-48 shrink-0 truncate text-xs text-muted-foreground sm:inline">
					{worker.projectName}
				</span>
				<span className="shrink-0 text-xs text-muted-foreground">{phase}</span>
			</button>
		</li>
	);
}
