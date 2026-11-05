import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, validators, type Credentials, type FieldError, type Session } from '@lidza/client'
import { usePreferences } from '../preferences'
import { useTitle } from '../navigation'

// Every request goes through the generated client: api.currentSession, api.login,
// api.listNotes, ... are typed from the Go handlers, and lidza check fails
// here when a handler changes shape. currentSession answers 200 for a
// visitor too (user null), so the page never produces an error to ask.
export function Home() {
  useTitle()
  const session = useQuery({ queryKey: ['session'], queryFn: () => api.currentSession() })
  if (session.isPending) return <p className="text-muted">Loading…</p>
  if (session.isError) return <p className="text-danger">{String(session.error)}</p>
  return session.data.user ? <Notes session={session.data.user} /> : <SignIn />
}

// SignIn registers or signs in with one form. The client-side validators
// apply the same rules as the server (schema.lidza), before the request.
function SignIn() {
  const queryClient = useQueryClient()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<FieldError[]>([])
  const submit = useMutation({
    mutationFn: ({ mode, body }: { mode: 'login' | 'register'; body: Credentials }) =>
      mode === 'login' ? api.login(body) : api.register(body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['session'] }),
  })
  const send = (mode: 'login' | 'register') => (event: FormEvent) => {
    event.preventDefault()
    const body = { email, password }
    const problems = validators.Credentials(body)
    setErrors(problems)
    if (problems.length === 0) submit.mutate({ mode, body })
  }
  const problem = (field: string) => errors.find((e) => e.field === field)?.message

  return (
    <form className="max-w-sm space-y-4" onSubmit={send('login')}>
      <h1 className="text-3xl font-semibold tracking-tight">Notes</h1>
      <p className="text-muted">Sign in, or register with a new email.</p>
      <label className="block">
        <span className="mb-1 block text-sm font-medium">Email</span>
        <input
          type="email"
          name="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="w-full rounded border border-line px-3 py-2"
          autoComplete="email"
        />
        {problem('email') && <span className="text-sm text-danger">{problem('email')}</span>}
      </label>
      <label className="block">
        <span className="mb-1 block text-sm font-medium">Password</span>
        <input
          type="password"
          name="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="w-full rounded border border-line px-3 py-2"
          autoComplete="current-password"
        />
        {problem('password') && <span className="text-sm text-danger">{problem('password')}</span>}
      </label>
      {submit.isError && <p className="text-sm text-danger">{submit.error.message}</p>}
      <div className="flex gap-3">
        <button type="submit" className="rounded bg-brand px-4 py-2 text-white" disabled={submit.isPending}>
          Sign in
        </button>
        <button type="button" className="rounded border border-line px-4 py-2" onClick={send('register')} disabled={submit.isPending}>
          Register
        </button>
      </div>
    </form>
  )
}

// The order of the list is a display choice of the account, kept by the
// auth pack (src/preferences.ts): it follows the user to another browser.
type NoteOrder = 'newest' | 'oldest'
const noteOrders: NoteOrder[] = ['newest', 'oldest']

// Notes lists the signed-in user's notes and adds or deletes one. Every
// mutation invalidates the list, which refetches through the client.
function Notes({ session }: { session: Session }) {
  const queryClient = useQueryClient()
  const notes = useQuery({ queryKey: ['notes'], queryFn: () => api.listNotes() })
  const prefs = usePreferences(
    { read: api.preferences, write: api.patchPreferences },
    session.userId,
    { 'notes.order': 'newest' as NoteOrder },
    { accept: (_, value) => noteOrders.includes(value as NoteOrder) },
  )
  const order = prefs.values['notes.order']
  const items = notes.data?.items ?? []
  const shown = order === 'oldest' ? [...items].reverse() : items
  const [title, setTitle] = useState('')
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['notes'] })
  const create = useMutation({
    mutationFn: () => api.createNote({ title }),
    onSuccess: () => {
      setTitle('')
      refresh()
    },
  })
  const remove = useMutation({ mutationFn: (id: string) => api.deleteNote({ id }), onSuccess: refresh })
  const signOut = useMutation({
    // The last choice is stored before the session ends.
    mutationFn: async () => {
      await prefs.flush()
      return api.logout()
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['session'] }),
  })
  const add = (event: FormEvent) => {
    event.preventDefault()
    if (validators.CreateNote({ title }).length === 0) create.mutate()
  }

  return (
    <div className="space-y-6">
      <header className="flex items-baseline justify-between">
        <h1 className="text-3xl font-semibold tracking-tight">Your notes</h1>
        <p className="text-sm text-muted">
          {session.email}{' '}
          <button type="button" className="ml-2 underline" onClick={() => signOut.mutate()}>
            Sign out
          </button>
        </p>
      </header>
      <form className="flex gap-3" onSubmit={add}>
        <label className="flex-1">
          <span className="sr-only">Title</span>
          <input
            name="title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What to remember"
            className="w-full rounded border border-line px-3 py-2"
          />
        </label>
        <button type="submit" className="rounded bg-brand px-4 py-2 text-white" disabled={title.trim() === '' || create.isPending}>
          Add note
        </button>
      </form>
      {create.isError && <p className="text-sm text-danger">{create.error.message}</p>}
      {notes.isPending && <p className="text-muted">Loading…</p>}
      {notes.isError && <p className="text-danger">{String(notes.error)}</p>}
      {notes.data && notes.data.total === 0 && <p className="text-muted">No notes yet.</p>}
      {notes.data && notes.data.total > 0 && (
        <label className="flex items-center gap-2 text-sm text-muted">
          Order
          <select
            name="order"
            value={order}
            onChange={(e) => prefs.set({ 'notes.order': e.target.value as NoteOrder })}
            className="rounded border border-line bg-white px-2 py-1 text-ink"
          >
            <option value="newest">Newest first</option>
            <option value="oldest">Oldest first</option>
          </select>
        </label>
      )}
      {prefs.error && <p className="text-sm text-danger">{prefs.error.message}</p>}
      <ul className="divide-y divide-line rounded-lg border border-line bg-white">
        {shown.map((note) => (
          <li key={note.id} className="flex items-center justify-between px-4 py-3">
            <span>{note.title}</span>
            <button type="button" className="text-sm text-muted underline" onClick={() => remove.mutate(note.id)}>
              Delete
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
