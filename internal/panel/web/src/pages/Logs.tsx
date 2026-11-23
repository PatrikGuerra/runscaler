import { useCallback, useState } from 'react'
import { getLogs } from '../api'
import { useI18n } from '../i18n'
import { usePolling } from '../usePolling'

export function LogsPage() {
  const { t } = useI18n()
  const [q, setQ] = useState('')
  const load = useCallback(() => getLogs(500, q), [q])
  const { data, error, reload } = usePolling(load, 10000)

  return (
    <section className="card">
      <div className="toolbar">
        <input type="search" placeholder={t('logs.search')} aria-label={t('logs.search')} value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="button" onClick={reload}>{t('common.refresh')}</button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      {data?.truncated && <p className="muted">{t('logs.truncated')}</p>}
      {data && data.lines.length === 0 && <p className="muted">{t('logs.empty')}</p>}
      {data && data.lines.length > 0 && <pre className="log">{data.lines.join('\n')}</pre>}
    </section>
  )
}
