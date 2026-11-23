import { useEffect, useState } from 'react'
import { getHistory, type HistoryEvent } from '../api'
import { useI18n } from '../i18n'

const KINDS = [
  'runscaler_up', 'runscaler_down', 'listener_connected', 'listener_disconnected',
  'job_assigned', 'job_started', 'job_completed', 'startup_failed',
  'request_submitted', 'request_applied', 'request_rejected',
  'login_succeeded', 'login_failed',
]

export function HistoryPage() {
  const { t, lang } = useI18n()
  const [kind, setKind] = useState('')
  const [events, setEvents] = useState<HistoryEvent[]>([])
  const [next, setNext] = useState(0)
  const [error, setError] = useState('')

  const load = (before: number, append: boolean) =>
    getHistory(kind, before).then(
      (r) => {
        setEvents((prev) => (append ? [...prev, ...r.events] : r.events))
        setNext(r.events.length === 100 ? r.nextBefore : 0)
        setError('')
      },
      (e: Error) => setError(e.message),
    )

  useEffect(() => {
    load(0, false)
  }, [kind]) // load reads kind; it is recreated with it

  return (
    <section className="card">
      <div className="toolbar">
        <select value={kind} onChange={(e) => setKind(e.target.value)} aria-label={t('history.all')}>
          <option value="">{t('history.all')}</option>
          {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <button type="button" onClick={() => load(0, false)}>{t('common.refresh')}</button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      {events.length === 0 ? (
        <p className="muted">{t('history.empty')}</p>
      ) : (
        <table>
          <tbody>
            {events.map((e) => (
              <tr key={e.id}>
                <td className="nowrap">{new Date(e.at).toLocaleString(lang)}</td>
                <td className="mono">{e.kind}</td>
                <td className="mono muted">{JSON.stringify(e.detail)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {next > 0 && <button type="button" onClick={() => load(next, true)}>{t('history.more')}</button>}
    </section>
  )
}
