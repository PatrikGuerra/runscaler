// Thin client for the panel API. State-changing calls send the session's CSRF token.

let csrfToken = ''

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error ?? res.statusText)
  return data as T
}

export interface Session {
  authenticated: boolean
  setupRequired: boolean
  csrfToken?: string
}

export async function getSession(): Promise<Session> {
  const s = await request<Session>('GET', '/api/auth/session')
  csrfToken = s.csrfToken ?? ''
  return s
}

export const setup = (setupToken: string, password: string) =>
  request<void>('POST', '/api/auth/setup', { setupToken, password })
export const login = (password: string) => request<void>('POST', '/api/auth/login', { password })
export const logout = () => request<void>('POST', '/api/auth/logout')

export interface ScaleSet {
  name: string
  ready: boolean
  last_error?: string
  last_connected?: string
  idle: number
  busy: number
  provisioning?: number
  removing?: number
  metrics?: { jobs_started: number; jobs_completed: number; desired_runners: number; assigned_jobs?: number; running_jobs?: number }
}

export interface Status {
  reachable: boolean
  fetchedAt: string
  error?: string
  health?: {
    status: string
    version: string
    uptime: string
    scale_sets: ScaleSet[]
    disk?: { filesystem: string; free_percent: number; free_bytes: number; total_bytes: number }[]
    capacity?: { limit: number; in_use: number }
  }
}

export const getStatus = () => request<Status>('GET', '/api/status')

export interface HistoryEvent {
  id: number
  at: string
  kind: string
  detail: Record<string, unknown>
}

export const getHistory = (kind: string, before: number) =>
  request<{ events: HistoryEvent[]; nextBefore: number }>(
    'GET',
    `/api/history?limit=100&kind=${encodeURIComponent(kind)}${before ? `&before=${before}` : ''}`,
  )

export const getLogs = (lines: number, q: string) =>
  request<{ lines: string[]; truncated: boolean }>('GET', `/api/logs?lines=${lines}&q=${encodeURIComponent(q)}`)
