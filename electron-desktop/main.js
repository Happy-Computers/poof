const { app, BrowserWindow, ipcMain, shell } = require('electron')
const { spawn } = require('node:child_process')
const fs = require('node:fs/promises')
const http = require('node:http')
const path = require('node:path')

const REPOSITORY_ROOT = path.resolve(__dirname, '..')
const MOUNT_BIN =
  process.env.INFINITY_STORAGE_MOUNT_BIN ||
  path.join(REPOSITORY_ROOT, isWindows() ? 'infinity-storage-mount.exe' : 'infinity-storage-mount')
const PROXY_BIN =
  process.env.INFINITY_STORAGE_PROXY_BIN ||
  path.join(REPOSITORY_ROOT, 'stream_proxy', 'zig-out', 'bin', isWindows() ? 'stream_proxy.exe' : 'stream_proxy')
const API_URL = process.env.INFINITY_STORAGE_API_URL || 'http://127.0.0.1:3005'
const CALLBACK_PORT = Number(process.env.INFINITY_STORAGE_CALLBACK_PORT || 9778)
const S3_BUCKET = process.env.INFINITY_STORAGE_S3_BUCKET || 'amaan-space-test-1'
const LIVE_RELAY_URL = process.env.INFINITY_STORAGE_LIVE_RELAY_URL || ''

const mounts = new Map()
let nextId = 1

let sessionToken = null
let callbackServer = null
let pendingAuth = null

function isWindows() {
  return process.platform === 'win32'
}

function mountTarget(m) {
  return isWindows() ? `${m.letter}:` : m.mountDir
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

async function createMount({ name, letter, projectId }) {
  if (!name.trim() || !projectId) {
    throw new Error('mount name and project are required')
  }
  const project = (await listProjects()).find((p) => p.id === projectId)
  if (!project) throw new Error('project not found')
  const id = String(nextId++)
  let mountDir
  if (isWindows()) {
    if (!/^[A-Z]$/.test(letter)) throw new Error('drive letter required')
  } else {
    mountDir = path.join(app.getPath('temp'), 'infinity-storage', `${name}-${id}`)
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
    name: name.trim(),
    letter,
    projectId: project.id,
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

function publicMount(m) {
  return { id: m.id, name: m.name, letter: m.letter, target: m.target }
}

async function removeMount(id) {
  const m = mounts.get(String(id))
  if (!m) return false
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

function stopCallbackServer() {
  if (callbackServer) {
    callbackServer.close()
    callbackServer = null
  }
}

const CALLBACK_PAGE = `<!doctype html><meta charset="utf-8"><title>Signed in</title>
<body><p id="s">Completing sign-in…</p><script>
const m = location.hash.match(/token=([^&]+)/)
if (m) fetch('/token', { method: 'POST', body: decodeURIComponent(m[1]) })
  .then(() => { document.getElementById('s').textContent = 'Signed in. Return to Infinity Storage.' })
else document.getElementById('s').textContent = 'Sign-in failed: no token in redirect.'
</script></body>`

async function signIn() {
  if (pendingAuth) return pendingAuth.promise
  let resolvePromise, rejectPromise
  pendingAuth = {
    promise: new Promise((resolve, reject) => {
      resolvePromise = resolve
      rejectPromise = reject
      setTimeout(() => reject(new Error('sign-in timed out')), 5 * 60_000)
    })
  }
  const server = http.createServer((req, res) => {
    if (req.method === 'POST' && req.url === '/token') {
      let body = ''
      req.on('data', (c) => {
        body += c
        if (body.length > 4096) req.destroy()
      })
      req.on('end', () => {
        res.writeHead(200).end()
        sessionToken = body.trim()
        stopCallbackServer()
        getSession().then(resolvePromise, rejectPromise)
        pendingAuth = null
      })
      return
    }
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' }).end(CALLBACK_PAGE)
  })
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(CALLBACK_PORT, '127.0.0.1', resolve)
  }).catch((err) => {
    pendingAuth = null
    throw new Error(`cannot bind callback port ${CALLBACK_PORT}: ${err.message}`)
  })
  callbackServer = server
  const signInUrl =
    `${API_URL}/desktop/sign-in?redirect=` +
    encodeURIComponent(`http://127.0.0.1:${CALLBACK_PORT}/callback`)
  try {
    await shell.openExternal(signInUrl)
  } catch (err) {
    stopCallbackServer()
    pendingAuth = null
    throw err
  }
  return pendingAuth.promise
}

async function signOut() {
  if (sessionToken) {
    try {
      await apiFetch('/api/auth/sign-out', { method: 'POST' })
    } catch {}
  }
  sessionToken = null
}

function registerIpc() {
  ipcMain.handle('system:platform', () => process.platform)
  ipcMain.handle('auth:signIn', () => signIn())
  ipcMain.handle('auth:signOut', () => signOut())
  ipcMain.handle('auth:getSession', () => getSession())
  ipcMain.handle('projects:list', () => listProjects())
  ipcMain.handle('projects:create', (_e, name) => createProject(name))
  ipcMain.handle('mounts:create', (_e, opts) => createMount(opts))
  ipcMain.handle('mounts:list', () => [...mounts.values()].map(publicMount))
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

app.whenReady().then(() => {
  registerIpc()
  createWindow()

  app.on('activate', function () {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', function () {
  for (const id of [...mounts.keys()]) removeMount(id)
  if (process.platform !== 'darwin') app.quit()
})
