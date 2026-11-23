import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getApiBaseUrl } from "../../lib/api-client";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { SettingsOptionMenu } from "./SettingsOptionMenu";

type LocalEndpoint = {
	name: string;
	baseUrl: string;
	kind: string;
	builtin: boolean;
	reachable: boolean;
	models?: string[];
	error?: string;
	harnessEnv: string;
};

type LocalProvidersResponse = {
	endpoints: LocalEndpoint[];
	presets: Array<{ name: string; baseUrl: string; kind: string }>;
	notes: Record<string, string>;
};

const KIND_OPTIONS = [
	{ value: "openai-compatible", label: "OpenAI-compatible" },
	{ value: "ollama", label: "Ollama" },
] as const;

async function fetchLocalProviders(): Promise<LocalProvidersResponse> {
	const response = await fetch(`${getApiBaseUrl()}/api/v1/local-providers`);
	if (!response.ok) throw new Error(await response.text());
	return response.json() as Promise<LocalProvidersResponse>;
}

/**
 * Desktop Local options: probe Ollama / llama.cpp, add LAN or Bionic-style
 * OpenAI-compatible servers, and show the harness env var to point a worker at.
 */
export function LocalProvidersSection({ titleHidden = false }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const [data, setData] = useState<LocalProvidersResponse | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const [name, setName] = useState("bionic");
	const [baseUrl, setBaseUrl] = useState("http://127.0.0.1:1234");
	const [kind, setKind] = useState("openai-compatible");
	const [copied, setCopied] = useState<string | null>(null);

	const reload = useCallback(async () => {
		setError(null);
		setBusy(true);
		try {
			setData(await fetchLocalProviders());
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(false);
		}
	}, []);

	useEffect(() => {
		void reload();
	}, [reload]);

	const addEndpoint = async () => {
		setError(null);
		setBusy(true);
		try {
			const response = await fetch(`${getApiBaseUrl()}/api/v1/local-providers`, {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ name, baseUrl, kind }),
			});
			const body = await response.json().catch(() => ({}));
			if (!response.ok) throw new Error((body as { error?: string }).error || response.statusText);
			await reload();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
			setBusy(false);
		}
	};

	const removeEndpoint = async (endpointName: string) => {
		setError(null);
		setBusy(true);
		try {
			const response = await fetch(`${getApiBaseUrl()}/api/v1/local-providers/${encodeURIComponent(endpointName)}`, {
				method: "DELETE",
			});
			if (!response.ok && response.status !== 204) {
				const body = await response.json().catch(() => ({}));
				throw new Error((body as { error?: string }).error || response.statusText);
			}
			await reload();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
			setBusy(false);
		}
	};

	const applyPreset = (preset: { name: string; baseUrl: string; kind: string }) => {
		setName(preset.name);
		setBaseUrl(preset.baseUrl);
		setKind(preset.kind);
	};

	const copyConnection = async (endpoint: LocalEndpoint) => {
		const text = `${endpoint.harnessEnv}=${endpoint.baseUrl}`;
		await navigator.clipboard.writeText(text);
		setCopied(endpoint.name);
		window.setTimeout(() => setCopied((current) => (current === endpoint.name ? null : current)), 2000);
	};

	return (
		<SettingsSection title={t("settings.localProviders")} titleHidden={titleHidden} grouped>
			<p className="px-3 pb-2 text-xs text-muted-foreground">{t("settings.localProviders.description")}</p>
			{error ? <p role="alert" className="px-3 pb-2 text-xs text-error">{error}</p> : null}
			{data?.notes.bionic ? <p className="px-3 pb-2 text-xs text-muted-foreground">{data.notes.bionic}</p> : null}
			{data?.notes.lan ? <p className="px-3 pb-2 text-xs text-muted-foreground">{data.notes.lan}</p> : null}

			{(data?.endpoints ?? []).map((endpoint) => (
				<SettingsRow
					key={endpoint.name}
					label={endpoint.name}
					description={
						<span className="flex flex-col gap-0.5">
							<span className="break-all font-mono text-xs">{endpoint.baseUrl}</span>
							<span className="text-xs text-muted-foreground">
								{endpoint.reachable
									? t("settings.localProviders.reachable", { count: endpoint.models?.length ?? 0 })
									: endpoint.error || t("settings.localProviders.unreachable")}
							</span>
						</span>
					}
				>
					<div className="flex flex-wrap items-center gap-1">
						<Button type="button" size="sm" variant="ghost" onClick={() => void copyConnection(endpoint)}>
							{copied === endpoint.name ? t("settings.localProviders.copied") : t("settings.localProviders.copyEnv")}
						</Button>
						{!endpoint.builtin ? (
							<Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void removeEndpoint(endpoint.name)}>
								{t("settings.localProviders.remove")}
							</Button>
						) : null}
					</div>
				</SettingsRow>
			))}

			<SettingsRow label={t("settings.localProviders.presets")}>
				<div className="flex flex-wrap gap-1">
					{(data?.presets ?? []).map((preset) => (
						<Button key={preset.name} type="button" size="sm" variant="ghost" onClick={() => applyPreset(preset)}>
							{preset.name}
						</Button>
					))}
					<Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void reload()}>
						{t("settings.localProviders.refresh")}
					</Button>
				</div>
			</SettingsRow>

			<SettingsRow label={t("settings.localProviders.add")}>
				<div className="flex w-full max-w-xl flex-col gap-2">
					<Input aria-label={t("settings.localProviders.name")} value={name} onChange={(event) => setName(event.target.value)} placeholder="bionic" />
					<Input aria-label={t("settings.localProviders.url")} value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="http://127.0.0.1:1234" />
					<SettingsOptionMenu
						aria-label={t("settings.localProviders.kind")}
						value={kind}
						options={[...KIND_OPTIONS]}
						onChange={setKind}
					/>
					<Button type="button" size="sm" disabled={busy || !name.trim() || !baseUrl.trim()} onClick={() => void addEndpoint()}>
						{t("settings.localProviders.connect")}
					</Button>
				</div>
			</SettingsRow>
		</SettingsSection>
	);
}
