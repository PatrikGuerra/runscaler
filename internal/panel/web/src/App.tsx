import { useCallback, useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { getSession, logout, type Session } from './api'
import { useI18n, type Lang } from './i18n'
import { LoginPage, SetupPage } from './pages/Auth'
import { StatusPage } from './pages/Status'
import { HistoryPage } from './pages/History'
import { LogsPage } from './pages/Logs'

type Theme = 'light' | 'dark'

function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(() => {
    const saved = localStorage.getItem('theme')
    if (saved === 'light' || saved === 'dark') return saved
    return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  })
  useEffect(() => {
    document.documentElement.dataset.theme = theme
  }, [theme])
  const toggle = () =>
    setTheme((t) => {
      const next = t === 'dark' ? 'light' : 'dark'
      localStorage.setItem('theme', next)
      return next
    })
  return [theme, toggle]
}

export function App() {
  const { t, lang, setLang } = useI18n()
  const [theme, toggleTheme] = useTheme()
  const [session, setSession] = useState<Session | null>(null)

  const refresh = useCallback(() => {
    getSession().then(setSession, () => setSession({ authenticated: false, setupRequired: false }))
  }, [])
  useEffect(refresh, [refresh])

  const header = (
    <header className="topbar">
      <strong>{t('app.title')}</strong>
      {session?.authenticated && (
        <nav>
          <NavLink to="/" end>{t('nav.status')}</NavLink>
          <NavLink to="/history">{t('nav.history')}</NavLink>
          <NavLink to="/logs">{t('nav.logs')}</NavLink>
        </nav>
      )}
      <div className="spacer" />
      <label className="sr-only" htmlFor="lang">{t('lang.label')}</label>
      <select id="lang" value={lang} onChange={(e) => setLang(e.target.value as Lang)}>
        <option value="en-US">English</option>
        <option value="pt-BR">Português</option>
      </select>
      <button type="button" onClick={toggleTheme} aria-label={t('theme.toggle')} title={t('theme.toggle')}>
        {theme === 'dark' ? '☀' : '☾'}
      </button>
      {session?.authenticated && (
        <button type="button" onClick={() => logout().finally(refresh)}>{t('nav.logout')}</button>
      )}
    </header>
  )

  let body
  if (!session) body = <p>{t('common.loading')}</p>
  else if (session.setupRequired) body = <SetupPage onDone={refresh} />
  else if (!session.authenticated) body = <LoginPage onDone={refresh} />
  else
    body = (
      <Routes>
        <Route path="/" element={<StatusPage />} />
        <Route path="/history" element={<HistoryPage />} />
        <Route path="/logs" element={<LogsPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    )

  return (
    <>
      {header}
      <main>{body}</main>
      <footer>{t('app.fork')}</footer>
    </>
  )
}
