export function productName(): string {
	const chrome = (globalThis as { chrome?: { runtime?: { getManifest?: () => { name?: string } } } }).chrome;
	const name = chrome?.runtime?.getManifest?.().name;
	return name === "BlueAshAI Browser Bridge" || name === "Blue Ash AI Browser Bridge" ? "Blue Ash AI" : "Orchestrator";
}
