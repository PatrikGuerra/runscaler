import { getStatus } from '../api'
import { useI18n } from '../i18n'
import { usePolling } from '../usePolling'

export function StatusPage() {
  const { t, lang } = useI18n()
  const { data, error } = usePolling(getStatus, 5000)

  if (error) return <p className="error" role="alert">{error}</p>
  if (!data) return <p>{t('common.loading')}</p>
  const updated = new Date(data.fetchedAt).toLocaleTimeString(lang)

  if (!data.reachable || !data.health)
    return (
      <section className="card bad">
        <h1>{t('status.unreachable')}</h1>
        <p className="mono">{data.error}</p>
        <p className="muted">{t('status.updated')}: {updated}</p>
      </section>
    )

  const h = data.health
  return (
    <>
      <section className="card">
        <dl className="kv">
          <dt>{t('status.version')}</dt><dd className="mono">{h.version}</dd>
          <dt>{t('status.uptime')}</dt><dd className="mono">{h.uptime}</dd>
          {h.disk?.map((d) => (
            <div key={d.filesystem} className="contents">
              <dt>{t('status.disk')}</dt>
              <dd>{d.free_percent.toFixed(0)}% <span className="muted mono">{d.filesystem}</span></dd>
            </div>
          ))}
          <dt>{t('status.updated')}</dt><dd>{updated}</dd>
        </dl>
      </section>
      {h.scale_sets.map((s) => (
        <section key={s.name} className={`card ${s.ready ? 'good' : 'bad'}`}>
          <h2>
            <span className="mono">{s.name}</span>{' '}
            <span className="badge">{s.ready ? t('status.connected') : t('status.disconnected')}</span>
          </h2>
          <div className="counters">
            <Counter label={t('status.provisioning')} value={s.provisioning ?? 0} />
            <Counter label={t('status.idle')} value={s.idle + s.busy} />
            <Counter label={t('status.assigned')} value={s.metrics?.assigned_jobs ?? s.busy} highlight={(s.metrics?.assigned_jobs ?? s.busy) > 0} />
            <Counter label={t('status.removing')} value={s.removing ?? 0} />
          </div>
          {s.metrics && (
            <p>
              {t('status.jobs')}: {s.metrics.jobs_started} {t('status.started')}, {s.metrics.jobs_completed} {t('status.completed')}
            </p>
          )}
          {s.last_error && (
            <p className="error">{t('status.lastError')}: <span className="mono">{s.last_error}</span></p>
          )}
        </section>
      ))}
    </>
  )
}

function Counter({ label, value, highlight }: { label: string; value: number; highlight?: boolean }) {
  return (
    <div className={`counter ${highlight ? 'hot' : ''}`}>
      <span className="value">{value}</span>
      <span className="label">{label}</span>
    </div>
  )
}
