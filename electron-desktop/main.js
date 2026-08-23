const { app, BrowserWindow, ipcMain, safeStorage, shell } = require('electron')
const { spawn } = require('node:child_process')
const fs = require('node:fs/promises')
const path = require('node:path')

const REPOSITORY_ROOT = path.resolve(__dirname, '..')
const MOUNT_BIN =
  process.env.INFINITY_STORAGE_MOUNT_BIN ||
  path.join(REPOSITORY_ROOT, isWindows() ? 'infinity-storage-mount.exe' : 'infinity-storage-mount')
const PROXY_BIN =
  process.env.INFINITY_STORAGE_PROXY_BIN ||
  path.join(REPOSITORY_ROOT, 'stream_proxy', 'zig-out', 'bin', isWindows() ? 'stream_proxy.exe' : 'stream_proxy')
const API_URL = process.env.INFINITY_STORAGE_API_URL || 'http://127.0.0.1:3005'
const DESKTOP_AUTH_POLL_INTERVAL_MS = 1000
const DESKTOP_AUTH_POLL_MAX = 300
const S3_BUCKET = process.env.INFINITY_STORAGE_S3_BUCKET || 'amaan-space-test-1'
const LIVE_RELAY_URL = process.env.INFINITY_STORAGE_LIVE_RELAY_URL || ''

const mounts = new Map()
const mountPromises = new Map()
let nextId = 1
let legacyMountDirectoriesCleaned = false

let sessionToken = null
let pendingAuth = null

function sessionTokenPath() {
  return path.join(app.getPath('userData'), 'session-token.enc')
}

async function storeSessionToken(token) {
  if (!token) {
    await fs.rm(sessionTokenPath(), { force: true })
    return
  }
  if (!safeStorage.isEncryptionAvailable()) return
  const encrypted = safeStorage.encryptString(token)
  await fs.writeFile(sessionTokenPath(), encrypted, { mode: 0o600 })
}

async function loadSessionToken() {
  if (!safeStorage.isEncryptionAvailable()) return
  try {
    const encrypted = await fs.readFile(sessionTokenPath())
    sessionToken = safeStorage.decryptString(encrypted)
    if (!await getSession()) {
      sessionToken = null
      await storeSessionToken(null)
    }
  } catch (error) {
    if (error?.code !== 'ENOENT') console.error(`load session token: ${error.message}`)
    sessionToken = null
  }
}

function isWindows() {
  return process.platform === 'win32'
}

function mountTarget(m) {
  return isWindows() ? `${m.letter}:` : m.mountDir
}

function nextWindowsLetter() {
  const used = new Set([...mounts.values()].map((mount) => mount.letter))
  for (const letter of 'ABCDEFGHIJKLMNOPQRSTUVWXYZ') {
    if (!used.has(letter)) return letter
  }
  return null
}

async function createRelayTokenFile(id) {
  if (!LIVE_RELAY_URL) return null
  if (!sessionToken) throw new Error('sign in again before mounting shared storage')
  const directory = path.join(app.getPath('userData'), 'relay-tokens')
  const tokenFile = path.join(directory, `${id}.token`)
  await fs.mkdir(directory, { mode: 0o700, recursive: true })
  await fs.writeFile(tokenFile, sessionToken, { mode: 0o600 })
  return tokenFile
}

async function removeRelayTokenFile(tokenFile) {
  if (tokenFile) await fs.rm(tokenFile, { force: true })
}

async function listMountProfiles() {
  const res = await apiFetch('/v1/mounts')
  if (!res.ok) throw new Error(`could not load mounts (${res.status})`)
  return (await res.json()).mounts
}

async function saveMountProfile(name, projectId) {
  const res = await apiFetch('/v1/mounts', {
    method: 'POST',
    body: { name, projectId }
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error || `could not save mount (${res.status})`)
  }
  return (await res.json()).mount
}

async function renameMountProfile(id, name) {
  const res = await apiFetch(`/v1/mounts/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: { name }
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error || `could not rename mount (${res.status})`)
  }
  return (await res.json()).mount
}

async function deleteMountProfile(id) {
  const res = await apiFetch(`/v1/mounts/${encodeURIComponent(id)}`, {
    method: 'DELETE'
  })
  if (!res.ok && res.status !== 404) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error || `could not delete mount (${res.status})`)
  }
}

async function cleanupLegacyMountDirectories() {
  if (isWindows() || legacyMountDirectoriesCleaned) return
  legacyMountDirectoriesCleaned = true
  const directory = path.join(app.getPath('temp'), 'infinity-storage')
  await fs.mkdir(directory, { recursive: true })
  const entries = await fs.readdir(directory, { withFileTypes: true })
  for (const entry of entries) {
    if (!entry.isDirectory() || !/-[0-9]+$/.test(entry.name)) continue
    await fs.rmdir(path.join(directory, entry.name)).catch(() => {})
  }
}

async function createMountProcess({ name, letter, projectId }, saveProfile, existingProfile) {
  const project = (await listProjects()).find((p) => p.id === projectId)
  if (!project) throw new Error('project not found')
  if (isWindows() && !/^[A-Z]$/.test(letter)) throw new Error('drive letter required')
  const profile = existingProfile || (saveProfile ? await saveMountProfile(name.trim(), project.id) : null)
  const mountName = profile?.name || name.trim()
  const id = profile?.id || String(nextId++)
  let mountDir
  if (isWindows()) {
    mountDir = undefined
  } else {
    await cleanupLegacyMountDirectories()
    mountDir = path.join(app.getPath('temp'), 'infinity-storage', mountName)
    await fs.mkdir(mountDir, { recursive: true })
  }

  const tokenFile = await createRelayTokenFile(id)
  const args = [
    '-mount',
    mountTarget({ letter, mountDir }),
    '-bucket',
    S3_BUCKET,
    '-prefix',
    project.prefix,
    '-proxy-bin',
    PROXY_BIN
  ]
  if (tokenFile) {
    args.push(
      '-live-relay-url',
      LIVE_RELAY_URL,
      '-library-id',
      project.id,
      '-relay-token-file',
      tokenFile
    )
  }
  const child = spawn(MOUNT_BIN, args, { stdio: ['ignore', 'pipe', 'pipe'] })

  const stderr = []
  child.stderr.on('data', (d) => {
    stderr.push(d)
    if (stderr.length > 100) stderr.shift()
  })
  const ready = new Promise((resolve, reject) => {
    child.once('error', reject)
    child.stderr.on('data', (d) => {
      if (String(d).includes('mounted') || String(d).toLowerCase().includes('serving')) resolve()
    })
    setTimeout(resolve, 3000)
    child.once('exit', (code) => reject(new Error(`mount exited (${code}): ${stderr.join('')}`)))
  })
  try {
    await ready
  } catch (err) {
    child.kill()
    await removeRelayTokenFile(tokenFile)
    mounts.delete(id)
    throw err
  }

  const mount = {
    id,
    name: mountName,
    letter,
    projectId: project.id,
    profileId: profile?.id,
    mountDir,
    target: mountTarget({ letter, mountDir }),
    child,
    tokenFile
  }
  mounts.set(id, mount)
  child.once('exit', () => {
    mounts.delete(id)
    void removeRelayTokenFile(tokenFile)
  })
  return publicMount(mount)
}

async function createMount(options, saveProfile = true, existingProfile = null) {
  const name = typeof options.name === 'string' ? options.name.trim() : ''
  const projectId = options.projectId
  if (!name || !projectId) throw new Error('mount name and project are required')
  const active = [...mounts.values()].find((mount) => mount.projectId === projectId)
  if (active) return publicMount(active)
  const pending = mountPromises.get(projectId)
  if (pending) return pending
  const promise = createMountProcess({ ...options, name }, saveProfile, existingProfile)
  mountPromises.set(projectId, promise)
  try {
    return await promise
  } finally {
    if (mountPromises.get(projectId) === promise) mountPromises.delete(projectId)
  }
}

async function syncMounts() {
  await cleanupLegacyMountDirectories()
  const profiles = await listMountProfiles()
  for (const profile of profiles) {
    const active = [...mounts.values()].some((mount) => mount.projectId === profile.projectId)
    if (active) continue
    const letter = isWindows() ? nextWindowsLetter() : ''
    if (isWindows() && letter === null) {
      throw new Error('no free Windows drive letter')
    }
    await createMount({
      name: profile.name,
      letter: letter || '',
      projectId: profile.projectId
    }, false, profile)
  }
  return [...mounts.values()].map(publicMount)
}

function publicMount(m) {
  return {
    id: m.id,
    name: m.name,
    letter: m.letter,
    projectId: m.projectId,
    profileId: m.profileId,
    target: m.target
  }
}

async function removeMount(id, deleteProfile = true) {
  const m = mounts.get(String(id))
  if (!m) return false
  if (deleteProfile && m.profileId) await deleteMountProfile(m.profileId)
  mounts.delete(String(id))
  m.child.removeAllListeners('exit')
  m.child.kill()
  await removeRelayTokenFile(m.tokenFile)
  return true
}

async function listDir(dir) {
  if (!dir) dir = app.getPath('home')
  const entries = []
  for (const de of await fs.readdir(dir, { withFileTypes: true })) {
    if (de.name.startsWith('.')) continue
    let size = null
    let mtime = null
    try {
      const st = await fs.stat(path.join(dir, de.name))
      size = st.isFile() ? st.size : null
      mtime = st.mtimeMs
    } catch {}
    entries.push({
      name: de.name,
      kind: de.isDirectory() ? 'dir' : 'file',
      size,
      mtime
    })
  }
  entries.sort((a, b) =>
    a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === 'dir' ? -1 : 1
  )
  return { path: dir, entries }
}

function apiFetch(pathname, { method = 'GET', body } = {}) {
  const headers = {}
  if (sessionToken) headers.authorization = `Bearer ${sessionToken}`
  if (body !== undefined) headers['content-type'] = 'application/json'
  return fetch(`${API_URL}${pathname}`, {
    body: body === undefined ? undefined : JSON.stringify(body),
    headers,
    method
  })
}

async function getSession() {
  if (!sessionToken) return null
  try {
    const res = await apiFetch('/api/auth/get-session')
    if (!res.ok) return null
    return await res.json()
  } catch {
    return null
  }
}

async function listProjects() {
  const res = await apiFetch('/v1/projects')
  if (!res.ok) throw new Error(`could not load projects (${res.status})`)
  return (await res.json()).projects
}

async function createProject(name) {
  const res = await apiFetch('/v1/projects', { method: 'POST', body: { name } })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error || `could not create project (${res.status})`)
  }
  return (await res.json()).project
}

function wait(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

async function completeDesktopSignIn() {
  const start = await apiFetch('/desktop/device/start', { method: 'POST' })
  if (!start.ok) throw new Error(`could not start desktop sign-in (${start.status})`)
  const challenge = await start.json()
  if (typeof challenge.id !== 'string' || typeof challenge.verifier !== 'string') {
    throw new Error('desktop sign-in challenge invalid')
  }
  await shell.openExternal(
    `${API_URL}/desktop/sign-in?challenge=${encodeURIComponent(challenge.id)}`
  )
  for (let attempt = 0; attempt < DESKTOP_AUTH_POLL_MAX; attempt++) {
    await wait(DESKTOP_AUTH_POLL_INTERVAL_MS)
    const response = await apiFetch('/desktop/device/token', {
      method: 'POST',
      body: challenge
    })
    if (response.status === 202) continue
    if (!response.ok) throw new Error(`desktop sign-in failed (${response.status})`)
    const result = await response.json()
    if (typeof result.token !== 'string' || result.token.length === 0) {
      throw new Error('desktop sign-in token invalid')
    }
    sessionToken = result.token
    const session = await getSession()
    if (!session) {
      sessionToken = null
      await storeSessionToken(null)
      throw new Error('desktop session invalid')
    }
    await storeSessionToken(sessionToken)
    return session
  }
  throw new Error('sign-in timed out')
}

async function signIn() {
  if (pendingAuth) return pendingAuth.promise
  const promise = completeDesktopSignIn()
  pendingAuth = { promise }
  try {
    return await promise
  } finally {
    if (pendingAuth?.promise === promise) pendingAuth = null
  }
}

async function signOut() {
  if (sessionToken) {
    try {
      await apiFetch('/api/auth/sign-out', { method: 'POST' })
    } catch {}
  }
  sessionToken = null
  await storeSessionToken(null)
}

function registerIpc() {
  ipcMain.handle('system:platform', () => process.platform)
  ipcMain.handle('auth:signIn', () => signIn())
  ipcMain.handle('auth:signOut', () => signOut())
  ipcMain.handle('auth:getSession', () => getSession())
  ipcMain.handle('projects:list', () => listProjects())
  ipcMain.handle('projects:create', (_e, name) => createProject(name))
  ipcMain.handle('mounts:create', (_e, opts) => createMount(opts))
  ipcMain.handle('mounts:sync', () => syncMounts())
  ipcMain.handle('mounts:list', () => [...mounts.values()].map(publicMount))
  ipcMain.handle('mounts:rename', (_e, id, name) => renameMountProfile(id, name))
  ipcMain.handle('mounts:remove', (_e, id) => removeMount(id))
  ipcMain.handle('fs:list', (_e, dir) => listDir(dir))
  ipcMain.handle('fs:openPath', (_e, p) => shell.openPath(p))
  ipcMain.handle('fs:home', () => app.getPath('home'))
}

function createWindow() {
  const mainWindow = new BrowserWindow({
    width: 1100,
    height: 700,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js')
    }
  })
  mainWindow.loadFile(path.join(__dirname, 'dist', 'index.html'))
}

app.whenReady().then(async () => {
  await loadSessionToken()
  registerIpc()
  createWindow()

  app.on('activate', function () {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', function () {
  for (const id of [...mounts.keys()]) removeMount(id, false)
  if (process.platform !== 'darwin') app.quit()
})
