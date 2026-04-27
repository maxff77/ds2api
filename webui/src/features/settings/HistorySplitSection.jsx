export default function HistorySplitSection({ t, form, setForm }) {
    return (
        <div className="bg-card border border-border rounded-xl p-5 space-y-4">
            <div className="space-y-1">
                <h3 className="font-semibold">{t('settings.historySplitTitle')}</h3>
                <p className="text-sm text-muted-foreground">{t('settings.historySplitDesc')}</p>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4">
                    <input
                        type="checkbox"
                        checked={Boolean(form.history_split?.enabled)}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            history_split: {
                                ...prev.history_split,
                                enabled: e.target.checked,
                            },
                            current_input_file: {
                                ...prev.current_input_file,
                                enabled: e.target.checked ? false : Boolean(prev.current_input_file?.enabled),
                            },
                        }))}
                        className="mt-1 h-4 w-4 rounded border-border"
                    />
                    <div className="space-y-1">
                        <span className="text-sm font-medium block">{t('settings.historySplitEnabled')}</span>
                        <span className="text-xs text-muted-foreground block">{t('settings.historySplitEnabledDesc')}</span>
                    </div>
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.historySplitTriggerAfterTurns')}</span>
                    <input
                        type="number"
                        min={1}
                        max={1000}
                        value={form.history_split?.trigger_after_turns || 1}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            history_split: {
                                ...prev.history_split,
                                trigger_after_turns: Number(e.target.value || 1),
                            },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                    <p className="text-xs text-muted-foreground">{t('settings.historySplitTriggerHelp')}</p>
                </label>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <label className="flex items-start gap-3 rounded-lg border border-border bg-background/60 p-4">
                    <input
                        type="checkbox"
                        checked={Boolean(form.current_input_file?.enabled)}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            history_split: {
                                ...prev.history_split,
                                enabled: e.target.checked ? false : Boolean(prev.history_split?.enabled),
                            },
                            current_input_file: {
                                ...prev.current_input_file,
                                enabled: e.target.checked,
                            },
                        }))}
                        className="mt-1 h-4 w-4 rounded border-border"
                    />
                    <div className="space-y-1">
                        <span className="text-sm font-medium block">{t('settings.currentInputFileEnabled')}</span>
                        <span className="text-xs text-muted-foreground block">{t('settings.currentInputFileDesc')}</span>
                    </div>
                </label>
                <label className="text-sm space-y-2">
                    <span className="text-muted-foreground">{t('settings.currentInputFileMinChars')}</span>
                    <input
                        type="number"
                        min={0}
                        max={100000000}
                        value={form.current_input_file?.min_chars ?? 0}
                        onChange={(e) => setForm((prev) => ({
                            ...prev,
                            current_input_file: {
                                ...prev.current_input_file,
                                min_chars: Number(e.target.value || 0),
                            },
                        }))}
                        className="w-full bg-background border border-border rounded-lg px-3 py-2"
                    />
                    <p className="text-xs text-muted-foreground">{t('settings.currentInputFileHelp')}</p>
                </label>
            </div>
            <p className="text-xs text-muted-foreground">{t('settings.splitPassThroughHelp')}</p>
        </div>
    )
}
