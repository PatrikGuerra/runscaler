import { useState, type FormEvent } from 'react'
import { login, setup } from '../api'
import { useI18n } from '../i18n'

function useSubmit(action: () => Promise<void>, onDone: () => void) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    action()
      .then(onDone, (err: Error) => setError(err.message))
      .finally(() => setBusy(false))
  }
  return { error, busy, submit }
}

export function SetupPage({ onDone }: { onDone: () => void }) {
  const { t } = useI18n()
  const [token, setToken] = useState('')
  const [password, setPassword] = useState('')
  const { error, busy, submit } = useSubmit(() => setup(token.trim(), password), onDone)
  return (
    <form className="card narrow" onSubmit={submit}>
      <h1>{t('setup.title')}</h1>
      <label>
        {t('setup.token')}
        <input value={token} onChange={(e) => setToken(e.target.value)} autoComplete="off" required />
      </label>
      <label>
        {t('setup.password')}
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} minLength={12} autoComplete="new-password" required />
      </label>
      {error && <p className="error" role="alert">{error}</p>}
      <button type="submit" disabled={busy}>{t('setup.submit')}</button>
    </form>
  )
}

export function LoginPage({ onDone }: { onDone: () => void }) {
  const { t } = useI18n()
  const [password, setPassword] = useState('')
  const { error, busy, submit } = useSubmit(() => login(password), onDone)
  return (
    <form className="card narrow" onSubmit={submit}>
      <h1>{t('login.title')}</h1>
      <label>
        {t('login.password')}
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" autoFocus required />
      </label>
      {error && <p className="error" role="alert">{error}</p>}
      <button type="submit" disabled={busy}>{t('login.submit')}</button>
    </form>
  )
}
