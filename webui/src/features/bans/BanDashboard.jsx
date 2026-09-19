import { useCallback, useEffect, useState } from 'react'
import { ShieldAlert, RotateCcw } from 'lucide-react'
import { useI18n } from '../../i18n'

export default function BanDashboard({ authFetch, onMessage }) {
    const { t } = useI18n()
    const [bans, setBans] = useState([])
    const [loading, setLoading] = useState(true)
    const [releasing, setReleasing] = useState({})

    const load = useCallback(async () => {
        setLoading(true)
        try {
            const res = await authFetch('/admin/bans')
            const data = await res.json()
            setBans(Array.isArray(data?.bans) ? data.bans : [])
        } catch (err) {
            onMessage('error', err?.message || t('messages.networkError'))
        } finally {
            setLoading(false)
        }
    }, [authFetch, onMessage, t])

    useEffect(() => { load() }, [load])

    const release = async (accountID) => {
        setReleasing(prev => ({ ...prev, [accountID]: true }))
        try {
            const res = await authFetch(`/admin/bans/${encodeURIComponent(accountID)}/release`, { method: 'POST' })
            const data = await res.json()
            if (!res.ok) {
                onMessage('error', data?.detail || t('messages.requestFailed'))
                return
            }
            onMessage('success', t('bans.releaseSuccess'))
            load()
        } catch (err) {
            onMessage('error', err?.message || t('messages.networkError'))
        } finally {
            setReleasing(prev => ({ ...prev, [accountID]: false }))
        }
    }

    if (loading) {
        return <div className="p-6 text-sm text-muted-foreground">{t('bans.loading')}</div>
    }

    if (bans.length === 0) {
        return (
            <div className="bg-card border border-border rounded-xl p-10 text-center shadow-sm">
                <ShieldAlert className="w-8 h-8 mx-auto mb-3 text-muted-foreground" />
                <h2 className="text-base font-semibold">{t('bans.emptyTitle')}</h2>
                <p className="text-sm text-muted-foreground mt-1">{t('bans.emptyDesc')}</p>
            </div>
        )
    }

    return (
        <div className="bg-card border border-border rounded-xl overflow-hidden shadow-sm">
            <div className="p-6 border-b border-border">
                <h2 className="text-lg font-semibold">{t('bans.title')}</h2>
                <p className="text-sm text-muted-foreground">{t('bans.desc')} ({bans.length})</p>
            </div>
            <div className="divide-y divide-border">
                {bans.map(ban => (
                    <div key={ban.account_id} className="p-4 flex flex-col md:flex-row md:items-center justify-between gap-3">
                        <div className="min-w-0">
                            <div className="font-medium truncate">{ban.account_id}</div>
                            <div className="text-sm text-muted-foreground">
                                {t('bans.reason')}: {ban.reason || '-'}
                                {' · '}{t('bans.strikes')}: {ban.strikes}
                                {' · '}{t('bans.until')}: {new Date(ban.until).toLocaleString()}
                            </div>
                        </div>
                        <button
                            onClick={() => release(ban.account_id)}
                            disabled={releasing[ban.account_id]}
                            className="flex items-center gap-2 px-4 py-2 bg-primary text-primary-foreground rounded-lg hover:bg-primary/90 transition-colors font-medium text-sm shadow-sm disabled:opacity-60 shrink-0"
                        >
                            <RotateCcw className="w-4 h-4" />
                            {t('bans.release')}
                        </button>
                    </div>
                ))}
            </div>
        </div>
    )
}
