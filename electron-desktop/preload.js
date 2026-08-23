const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('poof', {
  platform: () => ipcRenderer.invoke('system:platform'),
  auth: {
    signIn: () => ipcRenderer.invoke('auth:signIn'),
    signOut: () => ipcRenderer.invoke('auth:signOut'),
    getSession: () => ipcRenderer.invoke('auth:getSession')
  },
  listProjects: () => ipcRenderer.invoke('projects:list'),
  createProject: (name) => ipcRenderer.invoke('projects:create', name),
  createMount: (opts) => ipcRenderer.invoke('mounts:create', opts),
  syncMounts: () => ipcRenderer.invoke('mounts:sync'),
  listMounts: () => ipcRenderer.invoke('mounts:list'),
  renameMount: (id, name) => ipcRenderer.invoke('mounts:rename', id, name),
  removeMount: (id) => ipcRenderer.invoke('mounts:remove', id),
  listDir: (dir) => ipcRenderer.invoke('fs:list', dir),
  openPath: (p) => ipcRenderer.invoke('fs:openPath', p),
  home: () => ipcRenderer.invoke('fs:home')
})
