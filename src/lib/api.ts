import type { AppCatalog, PlatformRuntime } from '../types'

const tokenStorageKey = 'sql-seminar-auth-token'

const trimTrailingSlash = (value: string) => value.replace(/\/$/, '')

const resolveBaseUrl = () => {
  const configured = import.meta.env.VITE_API_BASE_URL
  if (configured) {
    return trimTrailingSlash(configured)
  }

  if (typeof window === 'undefined') {
    return 'http://localhost:3001'
  }

  if (window.location.hostname === 'localhost' && window.location.port === '5173') {
    return 'http://localhost:3001'
  }

  return window.location.origin
}

const baseUrl = resolveBaseUrl()

const normalizeApiError = (message?: string) => {
  switch (message) {
    case 'Invalid token':
    case 'invalid token':
      return 'Сессия истекла или была сброшена. Войдите снова.'
    case 'Missing token':
    case 'missing token':
      return 'Сессия не найдена. Войдите снова.'
    default:
      return message ?? 'Request failed'
  }
}

const createHeaders = (token?: string) => {
  const headers = new Headers({
    'Content-Type': 'application/json',
  })

  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  return headers
}

const request = async <T>(path: string, options: RequestInit = {}) => {
  const response = await fetch(`${baseUrl}${path}`, options)
  const data = await response.json() as T & { error?: string }

  if (!response.ok) {
    throw new Error(normalizeApiError(data.error))
  }

  return data
}

export const getStoredToken = () => localStorage.getItem(tokenStorageKey)

export const setStoredToken = (token: string) => {
  localStorage.setItem(tokenStorageKey, token)
}

export const clearStoredToken = () => {
  localStorage.removeItem(tokenStorageKey)
}

export const api = {
  baseUrl,
  async health() {
    return request<{ ok: boolean }>('/api/health')
  },
  async loginTeacher(login: string, password: string) {
    return request<{
      token: string
      catalog: AppCatalog
      runtime: PlatformRuntime
    }>('/api/auth/login', {
      method: 'POST',
      headers: createHeaders(),
      body: JSON.stringify({ mode: 'teacher', login, password }),
    })
  },
  async loginStudent(surname: string) {
    return request<{
      token: string
      catalog: AppCatalog
      runtime: PlatformRuntime
    }>('/api/auth/login', {
      method: 'POST',
      headers: createHeaders(),
      body: JSON.stringify({ mode: 'student', surname }),
    })
  },
  async bootstrap(token: string) {
    return request<{
      catalog: AppCatalog
      runtime: PlatformRuntime
    }>('/api/bootstrap', {
      headers: createHeaders(token),
    })
  },
  async reset(token: string) {
    return request<{
      catalog: AppCatalog
      runtime: PlatformRuntime
    }>('/api/reset', {
      method: 'POST',
      headers: createHeaders(token),
    })
  },
  async action(token: string, action: string, payload: Record<string, unknown> = {}) {
    return request<{
      catalog: AppCatalog
      runtime: PlatformRuntime
    }>(`/api/actions/${action}`, {
      method: 'POST',
      headers: createHeaders(token),
      body: JSON.stringify(payload),
    })
  },
  connect(token: string, onRuntime: (runtime: PlatformRuntime, catalog: AppCatalog) => void) {
    const wsBaseUrl = baseUrl.replace(/^http/i, 'ws')
    const socket = new WebSocket(`${wsBaseUrl}/ws?token=${encodeURIComponent(token)}`)

    socket.addEventListener('message', (event) => {
      const data = JSON.parse(event.data) as {
        type: string
        catalog: AppCatalog
        runtime: PlatformRuntime
      }

      if (data.type === 'state:update') {
        onRuntime(data.runtime, data.catalog)
      }
    })

    return socket
  },
}

export type RuntimeSocket = WebSocket
