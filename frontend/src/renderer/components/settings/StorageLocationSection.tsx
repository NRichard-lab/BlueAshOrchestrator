import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type StorageLocations = Awaited<ReturnType<typeof aoBridge.app.getStorageLocations>>;

/**
 * Shows where worker workspaces and app data live, and lets a packaged install
 * move them under a folder the user picks. Restart is required after a move.
 */
export function StorageLocationSection() {
	const { t } = useTranslation();
	const [locations, setLocations] = useState<StorageLocations | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [restartRequired, setRestartRequired] = useState(false);

	useEffect(() => {
		let cancelled = false;
		void aoBridge.app.getStorageLocations().then((next) => {
			if (!cancelled) setLocations(next);
		}).catch((err: unknown) => {
			if (!cancelled) setError(err instanceof Error ? err.message : String(err));
		});
		return () => { cancelled = true; };
	}, []);

	const changeLocation = async () => {
		setError(null);
		const chosen = await aoBridge.app.chooseDirectory({
			title: t("settings.storage.chooseTitle"),
			defaultPath: locations?.installDir || undefined,
		});
		if (!chosen) return;
		setBusy(true);
		try {
			const next = await aoBridge.app.setStorageLocation(chosen);
			setLocations(next);
			setRestartRequired(next.restartRequired);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(false);
		}
	};

	return (
		<SettingsSection title={t("settings.storage")} grouped>
			<SettingsRow
				label={t("settings.storage.workspace")}
				description={
					<span className="break-all font-mono text-xs text-muted-foreground">
						{locations?.workspaceDir || t("settings.storage.loading")}
					</span>
				}
			>
				<span className="text-xs text-muted-foreground">{t("settings.storage.workspaceHint")}</span>
			</SettingsRow>
			<SettingsRow
				label={t("settings.storage.data")}
				description={
					<span className="break-all font-mono text-xs text-muted-foreground">
						{locations?.dataDir || t("settings.storage.loading")}
					</span>
				}
			>
				<span className="text-xs text-muted-foreground">{t("settings.storage.dataHint")}</span>
			</SettingsRow>
			<SettingsRow
				label={t("settings.storage.change")}
				description={
					error
						? <span role="alert" className="text-error">{error}</span>
						: restartRequired
							? <span className="text-muted-foreground">{t("settings.storage.restartRequired")}</span>
							: <span className="text-muted-foreground">{t("settings.storage.changeHint")}</span>
				}
			>
				<Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void changeLocation()}>
					{busy ? t("settings.storage.moving") : t("settings.storage.choose")}
				</Button>
			</SettingsRow>
		</SettingsSection>
	);
}
