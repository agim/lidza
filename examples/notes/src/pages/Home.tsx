import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, validators, type Credentials, type FieldError, type Session } from '@lidza/client'
import { usePreferences } from '../preferences'
import { useTitle } from '../navigation'
import { Button, Empty, Field, Notice, Page, Row, Select, Stack, TextInput } from '../ui'

// Every request goes through the generated client: api.currentSession, api.login,
// api.listNotes, ... are typed from the Go handlers, and lidza check fails
// here when a handler changes shape. currentSession answers 200 for a
// visitor too (user null), so the page never produces an error to ask.
// The page is built from src/ui.tsx: Page, Field, Button, Select, Notice
// and Empty; nothing here styles a control or a heading by hand.
export function Home() {
  useTitle()
  const session = useQuery({ queryKey: ['session'], queryFn: () => api.currentSession() })
  if (session.isPending) return <p className="text-muted">Loading…</p>
  if (session.isError) return <Notice tone="danger">{String(session.error)}</Notice>
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
    <Page title="Notes" lede="Sign in, or register with a new email.">
      <form className="max-w-sm" onSubmit={send('login')}>
        <Stack>
          <Field label="Email" error={problem('email')}>
            <TextInput type="email" name="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" />
          </Field>
          <Field label="Password" error={problem('password')}>
            <TextInput type="password" name="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
          </Field>
          {submit.isError && <Notice tone="danger">{submit.error.message}</Notice>}
          <Row>
            <Button type="submit" disabled={submit.isPending}>
              Sign in
            </Button>
            <Button tone="secondary" onClick={send('register')} disabled={submit.isPending}>
              Register
            </Button>
          </Row>
        </Stack>
      </form>
    </Page>
  )
}

// The order of the list is a display choice of the account, kept by the
// auth pack (src/preferences.ts): it follows the user to another browser.
type NoteOrder = 'newest' | 'oldest'
const noteOrders: { value: NoteOrder; label: string }[] = [
  { value: 'newest', label: 'Newest first' },
  { value: 'oldest', label: 'Oldest first' },
]

// Notes lists the signed-in user's notes and adds or deletes one. Every
// mutation invalidates the list, which refetches through the client.
function Notes({ session }: { session: Session }) {
  const queryClient = useQueryClient()
  const notes = useQuery({ queryKey: ['notes'], queryFn: () => api.listNotes() })
  const prefs = usePreferences(
    { read: api.preferences, write: api.patchPreferences },
    session.userId,
    { 'notes.order': 'newest' as NoteOrder },
    { accept: (_, value) => noteOrders.some((o) => o.value === value) },
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
    <Page
      title="Your notes"
      actions={
        <p className="text-sm text-muted">
          {session.email}{' '}
          <Button tone="quiet" className="px-1 py-0 underline" onClick={() => signOut.mutate()}>
            Sign out
          </Button>
        </p>
      }
    >
      <form onSubmit={add}>
        <Row>
          <TextInput name="title" aria-label="Title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="What to remember" className="flex-1" />
          <Button type="submit" disabled={title.trim() === '' || create.isPending}>
            Add note
          </Button>
        </Row>
      </form>
      {create.isError && <Notice tone="danger">{create.error.message}</Notice>}
      {notes.isPending && <p className="text-muted">Loading…</p>}
      {notes.isError && <Notice tone="danger">{String(notes.error)}</Notice>}
      {notes.data && notes.data.total === 0 && <Empty title="No notes yet." />}
      {notes.data && notes.data.total > 0 && (
        <Stack>
          <Row>
            <Select label="Order" prefix value={order} options={noteOrders} onChange={(value) => prefs.set({ 'notes.order': value })} />
          </Row>
          {prefs.error && <Notice tone="danger">{prefs.error.message}</Notice>}
          <ul className="divide-y divide-line rounded-lg border border-line bg-white">
            {shown.map((note) => (
              <li key={note.id} className="flex items-center justify-between px-4 py-3">
                <span>{note.title}</span>
                <Button tone="quiet" className="px-1 py-0 text-sm underline" onClick={() => remove.mutate(note.id)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        </Stack>
      )}
    </Page>
  )
}
