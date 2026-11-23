import { createContext, useContext, useState, type ReactNode } from 'react'

const en = {
  'app.title': 'runscaler panel',
  'app.fork': 'Modified fork of Runscaler (Sustainable Use License). Not affiliated with the original author.',
  'nav.status': 'Status',
  'nav.history': 'History',
  'nav.logs': 'Logs',
  'nav.logout': 'Log out',
  'theme.toggle': 'Toggle theme',
  'lang.label': 'Language',
  'setup.title': 'Create the admin password',
  'setup.token': 'Setup token (printed in the panel container log)',
  'setup.password': 'New password (at least 12 characters)',
  'setup.submit': 'Create password',
  'login.title': 'Log in',
  'login.password': 'Password',
  'login.submit': 'Log in',
  'status.unreachable': 'runscaler is not reachable',
  'status.version': 'Version',
  'status.uptime': 'Uptime',
  'status.connected': 'Connected',
  'status.disconnected': 'Disconnected',
  'status.idle': 'Runners up',
  'status.assigned': 'Jobs running',
  'status.provisioning': 'Starting',
  'status.removing': 'Removing',
  'status.jobs': 'Jobs (since start)',
  'status.started': 'started',
  'status.completed': 'completed',
  'status.lastError': 'Last error',
  'status.disk': 'Free disk',
  'status.updated': 'Updated',
  'history.all': 'All events',
  'history.empty': 'No events yet.',
  'history.more': 'Load older',
  'logs.search': 'Search',
  'logs.truncated': 'Older lines are hidden.',
  'logs.empty': 'No log lines.',
  'common.loading': 'Loading…',
  'common.refresh': 'Refresh',
}
type Key = keyof typeof en

const ptBR: Record<Key, string> = {
  'app.title': 'painel do runscaler',
  'app.fork': 'Fork modificado do Runscaler (Sustainable Use License). Sem vínculo com o autor original.',
  'nav.status': 'Status',
  'nav.history': 'Histórico',
  'nav.logs': 'Logs',
  'nav.logout': 'Sair',
  'theme.toggle': 'Trocar tema',
  'lang.label': 'Idioma',
  'setup.title': 'Criar a senha de admin',
  'setup.token': 'Token de setup (impresso no log do container do painel)',
  'setup.password': 'Nova senha (pelo menos 12 caracteres)',
  'setup.submit': 'Criar senha',
  'login.title': 'Entrar',
  'login.password': 'Senha',
  'login.submit': 'Entrar',
  'status.unreachable': 'o runscaler não responde',
  'status.version': 'Versão',
  'status.uptime': 'No ar há',
  'status.connected': 'Conectado',
  'status.disconnected': 'Desconectado',
  'status.idle': 'Runners no ar',
  'status.assigned': 'Jobs rodando',
  'status.provisioning': 'Subindo',
  'status.removing': 'Removendo',
  'status.jobs': 'Jobs (desde o início)',
  'status.started': 'iniciados',
  'status.completed': 'concluídos',
  'status.lastError': 'Último erro',
  'status.disk': 'Disco livre',
  'status.updated': 'Atualizado',
  'history.all': 'Todos os eventos',
  'history.empty': 'Nenhum evento ainda.',
  'history.more': 'Carregar mais antigos',
  'logs.search': 'Buscar',
  'logs.truncated': 'Linhas mais antigas estão ocultas.',
  'logs.empty': 'Nenhuma linha de log.',
  'common.loading': 'Carregando…',
  'common.refresh': 'Atualizar',
}

export type Lang = 'en-US' | 'pt-BR'
const dicts: Record<Lang, Record<Key, string>> = { 'en-US': en, 'pt-BR': ptBR }

function initialLang(): Lang {
  const saved = localStorage.getItem('lang')
  if (saved === 'en-US' || saved === 'pt-BR') return saved
  return navigator.language.toLowerCase().startsWith('pt') ? 'pt-BR' : 'en-US'
}

const Ctx = createContext<{ lang: Lang; setLang: (l: Lang) => void; t: (k: Key) => string }>(null!)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(initialLang)
  const setLang = (l: Lang) => {
    localStorage.setItem('lang', l)
    document.documentElement.lang = l
    setLangState(l)
  }
  return <Ctx.Provider value={{ lang, setLang, t: (k) => dicts[lang][k] }}>{children}</Ctx.Provider>
}

export const useI18n = () => useContext(Ctx)
