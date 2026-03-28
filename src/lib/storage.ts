import type { PlatformRuntime } from '../types'

const STORAGE_KEY = 'sql-seminar-platform-runtime-v1'
const CHANNEL_NAME = 'sql-seminar-platform-sync'

const emptyRuntime = (): PlatformRuntime => ({
  isAuthenticated: false,
  currentUserId: '',
  sessionId: '',
  createdUsers: [],
  createdGroups: [],
  userOverrides: {},
  groupOverrides: {},
  deletedUserIds: [],
  deletedGroupIds: [],
  seminarMeta: {},
  seminarStudentIds: {},
  seminarTaskIds: {},
  selectedSeminarByUser: {},
  selectedTaskByUser: {},
  drafts: {},
  selectedPlaygroundTemplateId: '',
  selectedPlaygroundChallengeId: '',
  selectedPlaygroundDatasetId: '',
  seminarRuntime: {},
  importedTemplates: [],
  createdTasks: [],
  createdSeminars: [],
  queryRuns: [],
  submissions: [],
  notifications: [],
  eventLogs: [],
})

export const loadRuntime = (): PlatformRuntime => {
  if (typeof window === 'undefined') {
    return emptyRuntime()
  }

  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    if (!stored) {
      return emptyRuntime()
    }

    return JSON.parse(stored) as PlatformRuntime
  } catch {
    return emptyRuntime()
  }
}

export const saveRuntime = (runtime: PlatformRuntime) => {
  if (typeof window === 'undefined') {
    return
  }

  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(runtime))

  if ('BroadcastChannel' in window) {
    const channel = new BroadcastChannel(CHANNEL_NAME)
    channel.postMessage(runtime)
    channel.close()
  }
}

export const resetRuntime = () => {
  if (typeof window === 'undefined') {
    return
  }

  window.localStorage.removeItem(STORAGE_KEY)
}

export const subscribeRuntimeSync = (callback: (runtime: PlatformRuntime) => void) => {
  if (typeof window === 'undefined') {
    return () => undefined
  }

  const handleStorage = (event: StorageEvent) => {
    if (event.key !== STORAGE_KEY || !event.newValue) {
      return
    }

    try {
      callback(JSON.parse(event.newValue) as PlatformRuntime)
    } catch {
      // ignore invalid payloads
    }
  }

  window.addEventListener('storage', handleStorage)

  let channel: BroadcastChannel | null = null

  if ('BroadcastChannel' in window) {
    channel = new BroadcastChannel(CHANNEL_NAME)
    channel.onmessage = (event) => {
      callback(event.data as PlatformRuntime)
    }
  }

  return () => {
    window.removeEventListener('storage', handleStorage)
    channel?.close()
  }
}
