import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ChevronRight,
  File as FileIcon,
  Folder,
  FolderOpen,
  HardDrive,
  Pencil,
  Plus,
  Trash2
} from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger
} from "@/components/ui/context-menu"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from "@/components/ui/select"
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
  SidebarFooter
} from "@/components/ui/sidebar"

interface MountInfo {
  id: string
  name: string
  letter: string
  projectId: string
  profileId: string
  target: string
}

interface DirEntry {
  name: string
  kind: "dir" | "file"
  size: number | null
  mtime: number | null
}

interface SessionInfo {
  user: { id: string; name: string; email: string }
}

interface Project {
  id: string
  name: string
  prefix: string
}

declare global {
  interface Window {
    poof: {
      platform(): Promise<string>
      auth: {
        signIn(): Promise<SessionInfo | null>
        signOut(): Promise<void>
        getSession(): Promise<SessionInfo | null>
      }
      createMount(opts: {
        name: string
        letter: string
        projectId: string
      }): Promise<MountInfo>
      listProjects(): Promise<Project[]>
      createProject(name: string): Promise<Project>
      syncMounts(): Promise<MountInfo[] | undefined>
      listMounts(): Promise<MountInfo[]>
      renameMount(id: string, name: string): Promise<MountInfo>
      removeMount(id: string): Promise<boolean>
      listDir(dir?: string): Promise<{ path: string; entries: DirEntry[] }>
      openPath(p: string): Promise<string>
      home(): Promise<string>
    }
  }
}

function formatSize(n: number) {
  if (n < 1024) return `${n} B`
  const units = ["KB", "MB", "GB", "TB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

const ALL_LETTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("")
const DIRECTORY_REFRESH_MS = 1_000

export default function App() {
  const [session, setSession] = useState<SessionInfo | null | undefined>(
    undefined
  )
  const [platform, setPlatform] = useState("")
  const [signingIn, setSigningIn] = useState(false)
  const [signInError, setSignInError] = useState("")
  const [mounts, setMounts] = useState<MountInfo[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [selected, setSelected] = useState<MountInfo | null>(null)
  const [cwd, setCwd] = useState<string>("")
  const [entries, setEntries] = useState<DirEntry[]>([])

  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [letter, setLetter] = useState("")
  const [projectId, setProjectId] = useState("")
  const [createError, setCreateError] = useState("")
  const [creating, setCreating] = useState(false)

  const [deleteTarget, setDeleteTarget] = useState<MountInfo | null>(null)
  const [renameTarget, setRenameTarget] = useState<MountInfo | null>(null)
  const [renameName, setRenameName] = useState("")

  const refreshMounts = useCallback(async () => {
    setMounts(await window.poof.listMounts())
  }, [])

  const refreshProjects = useCallback(async () => {
    setProjects(await window.poof.listProjects())
  }, [])

  useEffect(() => {
    window.poof.platform().then(setPlatform).catch(() => setPlatform(""))
    window.poof.auth
      .getSession()
      .then(setSession)
      .catch(() => setSession(null))
  }, [])

  async function handleSignIn() {
    setSigningIn(true)
    setSignInError("")
    try {
      const s = await window.poof.auth.signIn()
      setSession(s)
    } catch (err) {
      setSignInError(String((err as Error).message ?? err))
    } finally {
      setSigningIn(false)
    }
  }

  async function handleSignOut() {
    await window.poof.auth.signOut()
    setSelected(null)
    setSession(null)
  }

  useEffect(() => {
    if (session === null || session === undefined) return
    const sync = () => {
      void window.poof.syncMounts().then(refreshMounts).catch(() => {})
    }
    sync()
    const interval = window.setInterval(sync, 2_000)
    void refreshProjects()
    return () => window.clearInterval(interval)
  }, [refreshMounts, refreshProjects, session])

  const browse = useCallback(async (dir: string) => {
    try {
      const res = await window.poof.listDir(dir)
      setCwd(res.path)
      setEntries(res.entries)
    } catch {
      setCwd("")
      setEntries([])
    }
  }, [])

  useEffect(() => {
    if (!selected) {
      setCwd("")
      setEntries([])
      return
    }
    const refresh = () => void browse(selected.target)
    refresh()
    const interval = window.setInterval(refresh, DIRECTORY_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [selected, browse])

  const freeLetters = useMemo(
    () => ALL_LETTERS.filter((l) => !mounts.some((m) => m.letter === l)),
    [mounts]
  )

  function openDialog() {
    setName(projects[0]?.name ?? "")
    setLetter(platform === "win32" ? (freeLetters[0] ?? "") : "")
    setProjectId(projects[0]?.id ?? "new")
    setCreateError("")
    setOpen(true)
  }

  async function createMount() {
    if (!name.trim()) return
    setCreating(true)
    setCreateError("")
    try {
      const project =
        projectId === "new"
          ? await window.poof.createProject(name.trim())
          : projects.find((p) => p.id === projectId)
      if (!project) throw new Error("select a project")
      const m = await window.poof.createMount({
        name: name.trim(),
        letter,
        projectId: project.id
      })
      await refreshProjects()
      await refreshMounts()
      setSelected(m)
      setOpen(false)
    } catch (err) {
      setCreateError(String((err as Error).message ?? err))
    } finally {
      setCreating(false)
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    await window.poof.removeMount(deleteTarget.id)
    await refreshMounts()
    if (selected?.id === deleteTarget.id) setSelected(null)
    setDeleteTarget(null)
  }

  async function saveRename() {
    if (!renameName.trim() || !renameTarget) return
    await window.poof.renameMount(renameTarget.profileId, renameName.trim())
    setMounts((prev) =>
      prev.map((m) =>
        m.id === renameTarget.id ? { ...m, name: renameName.trim() } : m
      )
    )
    setRenameTarget(null)
  }

  function openEntry(e: DirEntry) {
    if (e.kind === "dir") browse(joinPath(cwd, e.name))
    else void window.poof.openPath(joinPath(cwd, e.name))
  }

  const crumbs = cwd ? cwd.split("/").filter(Boolean) : []

  if (session === undefined) return null

  if (session === null) {
    return (
      <div className="flex h-screen flex-col items-center justify-center gap-4">
        <div className="flex items-center gap-2">
          <HardDrive className="size-6" />
          <span className="font-semibold text-xl">Infinity Storage</span>
        </div>
        <Button onClick={handleSignIn} disabled={signingIn}>
          {signingIn ? "Waiting for browser…" : "Sign in with Google"}
        </Button>
        {signInError && (
          <p className="text-destructive text-sm">{signInError}</p>
        )}
      </div>
    )
  }

  return (
    <SidebarProvider>
      <Sidebar>
        <SidebarHeader>
          <div className="flex items-center gap-2 px-2 py-1.5">
            <HardDrive />
            <span className="text-sm font-semibold">Mounts</span>
          </div>
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupLabel>Mounted drives</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {mounts.length === 0 ? (
                  <li className="px-2 py-4 text-muted-foreground text-sm">
                    No mounts yet.
                  </li>
                ) : (
                  mounts.map((m) => (
                    <SidebarMenuItem key={m.id}>
                      <ContextMenu>
                        <ContextMenuTrigger asChild>
                          <SidebarMenuButton
                            isActive={selected?.id === m.id}
                            onClick={() => setSelected(m)}
                          >
                            <HardDrive />
                            <span className="truncate">{m.name}</span>
                            <span className="ml-auto font-mono text-muted-foreground text-xs">
                              {m.target}
                            </span>
                          </SidebarMenuButton>
                        </ContextMenuTrigger>
                        <ContextMenuContent>
                          <ContextMenuItem onSelect={() => {
                            setRenameTarget(m)
                            setRenameName(m.name)
                          }}>
                            <Pencil />
                            Rename
                          </ContextMenuItem>
                          <ContextMenuSeparator />
                          <ContextMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(m)}
                          >
                            <Trash2 />
                            Unmount & Delete
                          </ContextMenuItem>
                        </ContextMenuContent>
                      </ContextMenu>
                    </SidebarMenuItem>
                  ))
                )}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter>
          <div className="flex items-center gap-2 px-2 py-1.5">
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">
                {session.user.name}
              </p>
              <p className="truncate text-muted-foreground text-xs">
                {session.user.email}
              </p>
            </div>
            <Button variant="ghost" size="sm" onClick={handleSignOut}>
              Sign out
            </Button>
          </div>
        </SidebarFooter>
      </Sidebar>
      <SidebarInset>
        <header className="flex h-14 items-center gap-3 border-b px-4">
          <SidebarTrigger />
          {cwd ? (
            <nav className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden text-sm">
              <button
                className="truncate text-muted-foreground hover:text-foreground"
                onClick={() => browse("/" + crumbs.slice(0, 1).join("/"))}
              >
                {crumbs[0]}
              </button>
              {crumbs.slice(1).map((c, i) => (
                <span key={i} className="flex min-w-0 items-center gap-1">
                  <ChevronRight className="size-3 text-muted-foreground" />
                  {i === crumbs.length - 2 ? (
                    <span className="truncate font-medium">{c}</span>
                  ) : (
                    <button
                      className="truncate text-muted-foreground hover:text-foreground"
                      onClick={() =>
                        browse("/" + crumbs.slice(0, i + 2).join("/"))
                      }
                    >
                      {c}
                    </button>
                  )}
                </span>
              ))}
            </nav>
          ) : (
            <h1 className="flex-1 font-semibold text-lg">Main</h1>
          )}
          <Button className="ml-auto" onClick={openDialog}>
            <Plus data-icon="inline-start" />
            Create Mount
          </Button>
        </header>
        <main className="flex-1 overflow-auto p-4">
          {!selected ? (
            <div className="flex h-full items-center justify-center">
              <p className="text-muted-foreground text-sm">
                Create a mount to get started.
              </p>
            </div>
          ) : entries.length === 0 ? (
            <p className="p-8 text-center text-muted-foreground text-sm">
              Empty folder.
            </p>
          ) : (
            <ul className="grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-2">
              {entries.map((e) => (
                <li key={e.name}>
                  <button
                    className="flex w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm hover:bg-accent"
                    onDoubleClick={() => openEntry(e)}
                  >
                    {e.kind === "dir" ? (
                      <FolderOpen className="size-4 shrink-0 text-blue-500" />
                    ) : (
                      <FileIcon className="size-4 shrink-0 text-muted-foreground" />
                    )}
                    <span className="truncate">{e.name}</span>
                    <span className="ml-auto shrink-0 text-muted-foreground text-xs">
                      {e.kind === "file" && e.size !== null
                        ? formatSize(e.size)
                        : ""}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </main>
      </SidebarInset>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Mount</DialogTitle>
            <DialogDescription>
              Select a project to mount from your private storage.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>Project</FieldLabel>
              <Select
                value={projectId}
                onValueChange={(value) => {
                  setProjectId(value)
                  if (value !== "new") {
                    setName(projects.find((project) => project.id === value)?.name ?? "")
                  }
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Select a project" />
                </SelectTrigger>
                <SelectContent>
                  {projects.map((project) => (
                    <SelectItem key={project.id} value={project.id}>
                      {project.name}
                    </SelectItem>
                  ))}
                  <SelectItem value="new">Create a project…</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="mount-name">Mount name</FieldLabel>
              <Input
                id="mount-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="summer-shoot"
              />
              <FieldDescription>
                {projectId === "new"
                  ? "Creates project with this name."
                  : "Linux uses this name for its directory."}
              </FieldDescription>
            </Field>
            {platform === "win32" && (
              <Field>
                <FieldLabel>Drive letter</FieldLabel>
                <Select value={letter} onValueChange={setLetter}>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Auto" />
                  </SelectTrigger>
                  <SelectContent>
                    {freeLetters.map((l) => (
                      <SelectItem key={l} value={l}>
                        {l}:
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FieldDescription>{freeLetters.length} letters free</FieldDescription>
              </Field>
            )}
          </FieldGroup>
          {createError && (
            <p className="text-destructive text-sm">{createError}</p>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              disabled={creating || !name.trim()}
              onClick={createMount}
            >
              {creating ? "Mounting…" : "Create Mount"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={renameTarget !== null}
        onOpenChange={(v) => !v && setRenameTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rename Mount</DialogTitle>
            <DialogDescription>Update the display name.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="rename-name">Mount name</FieldLabel>
              <Input
                id="rename-name"
                value={renameName}
                onChange={(e) => setRenameName(e.target.value)}
              />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameTarget(null)}>
              Cancel
            </Button>
            <Button disabled={!renameName.trim()} onClick={saveRename}>
              Save
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(v) => !v && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Unmount mount?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget &&
                `"${deleteTarget.name}" (${deleteTarget.target}) will be unmounted and removed from the sidebar.`}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={confirmDelete}>
              Unmount
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SidebarProvider>
  )
}

function joinPath(base: string, name: string) {
  return base.endsWith("/") ? base + name : base + "/" + name
}
