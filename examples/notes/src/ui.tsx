// The app's components: every page is built from these, so pages read
// alike (the guide's "Pages and components"). The layout, the spacing,
// the type scale and the controls live here once; a page never styles a
// heading, a button or a field by hand, and never uses the browser's own
// <select>, <dialog>, confirm() or alert(), which no theme reaches
// (lidza check L026).
//
//   <Page title="Posts" lede="Everything published." actions={<Button to="/posts/new">New post</Button>}>
//     <Section title="Drafts">...</Section>
//   </Page>
//   <Field label="Title" error={problem('title')}><TextInput name="title" value={title} onChange={...} /></Field>
//   <Select label="Status" value={status} onChange={setStatus} options={[{ value: 'draft', label: 'Draft' }]} />
//   <Dialog open={open} onClose={() => setOpen(false)} title="Delete this post?" actions={<Button tone="danger" onClick={remove}>Delete</Button>}>
//   <Drawer open={open} onClose={close} title="Filters">...</Drawer>
//
// Change the look in src/index.css (the tokens) and here, not in pages.

import {
  Children,
  cloneElement,
  forwardRef,
  isValidElement,
  useEffect,
  useId,
  useRef,
  useState,
  type AnchorHTMLAttributes,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
  type TextareaHTMLAttributes,
} from 'react'
import { createLink } from '@tanstack/react-router'

/* ---------- layout: one page shape, one rhythm ---------- */

/** join drops the falsy parts of a class list. */
export function join(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(' ')
}

/**
 * Page is the one layout: a trail or back link above, the title and its
 * lede with the page's actions beside them, then the content with the
 * same space between every block.
 */
export function Page(props: { title: string; lede?: ReactNode; actions?: ReactNode; above?: ReactNode; children: ReactNode }) {
  return (
    <div className="space-y-8">
      <header className="space-y-3">
        {props.above}
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-1">
            <h1 className="text-3xl font-semibold tracking-tight text-ink">{props.title}</h1>
            {props.lede && <p className="text-muted">{props.lede}</p>}
          </div>
          {props.actions && <Row>{props.actions}</Row>}
        </div>
      </header>
      {props.children}
    </div>
  )
}

/** Section is a card of the page: an optional title, then its content, evenly spaced. */
export function Section(props: { title?: string; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={join('space-y-4 rounded-lg border border-line bg-white p-5', props.className)}>
      {(props.title || props.actions) && (
        <div className="flex flex-wrap items-center justify-between gap-3">
          {props.title && <h2 className="text-lg font-medium text-ink">{props.title}</h2>}
          {props.actions && <Row>{props.actions}</Row>}
        </div>
      )}
      {props.children}
    </section>
  )
}

/** Stack puts its children under each other with one gap. */
export function Stack(props: { gap?: 'sm' | 'md' | 'lg'; children: ReactNode; className?: string }) {
  return <div className={join({ sm: 'space-y-2', md: 'space-y-4', lg: 'space-y-8' }[props.gap ?? 'md'], props.className)}>{props.children}</div>
}

/** Row puts its children side by side, wrapping, with one gap. */
export function Row(props: { children: ReactNode; className?: string }) {
  return <div className={join('flex flex-wrap items-center gap-3', props.className)}>{props.children}</div>
}

/** Notice is a message with a tone: an error, a warning, or information. */
export function Notice(props: { tone?: 'danger' | 'info' | 'success'; children: ReactNode }) {
  const tone = { danger: 'border-danger/40 bg-danger/5 text-danger', info: 'border-line bg-surface text-ink', success: 'border-green-700/30 bg-green-50 text-green-800' }[props.tone ?? 'info']
  return (
    <p role={props.tone === 'danger' ? 'alert' : undefined} className={join('rounded border px-3 py-2 text-sm', tone)}>
      {props.children}
    </p>
  )
}

/** Empty is a list with nothing in it: what that means and what to do. */
export function Empty(props: { title: string; children?: ReactNode }) {
  return (
    <div className="space-y-2 rounded-lg border border-dashed border-line px-4 py-8 text-center">
      <p className="text-muted">{props.title}</p>
      {props.children && <Row className="justify-center">{props.children}</Row>}
    </div>
  )
}

/* ---------- controls ---------- */

const tones = {
  primary: 'bg-brand text-white hover:bg-brand-strong',
  secondary: 'border border-line bg-white text-ink hover:border-brand hover:text-brand',
  quiet: 'text-muted hover:text-brand',
  danger: 'border border-danger/40 bg-white text-danger hover:bg-danger hover:text-white',
}
const buttonBase = 'inline-flex items-center justify-center gap-2 rounded px-4 py-2 text-sm font-medium no-underline transition-colors disabled:pointer-events-none disabled:opacity-40'

/** Button is an action; a link that looks like one is `<Button to="/path">`. */
export function Button(
  props: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: keyof typeof tones; to?: string; params?: Record<string, string>; search?: Record<string, unknown> },
) {
  const { tone = 'primary', to, params, search, className, type, ...rest } = props
  const classes = join(buttonBase, tones[tone], className)
  if (to) {
    return (
      <ButtonLink to={to as never} params={params as never} search={search as never} className={classes}>
        {rest.children}
      </ButtonLink>
    )
  }
  return <button type={type ?? 'button'} className={classes} {...rest} />
}
const ButtonLink = createLink(
  forwardRef<HTMLAnchorElement, AnchorHTMLAttributes<HTMLAnchorElement>>(function ButtonAnchor({ children, ...props }, ref) {
    return (
      <a ref={ref} {...props}>
        {children}
      </a>
    )
  }),
)

/** inputClass is the look of every text-like control, so a bare input matches. */
export const inputClass = 'w-full rounded border border-line bg-white px-3 py-2 text-sm text-ink placeholder:text-muted focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/20 disabled:opacity-40 aria-[invalid=true]:border-danger'

/**
 * Field labels one control and shows its hint and error; the control gets
 * the ids, so a screen reader reads them together. The control is the
 * one child (TextInput, TextArea, Select, or a bare input).
 */
export function Field(props: { label: string; hint?: ReactNode; error?: ReactNode; children: ReactNode; className?: string }) {
  const id = useId()
  const described = join(props.hint ? id + '-hint' : null, props.error ? id + '-error' : null) || undefined
  const control = Children.only(props.children)
  const child = isValidElement<Record<string, unknown>>(control)
    ? cloneElement(control, { id, 'aria-describedby': described, 'aria-invalid': props.error ? true : undefined, ...(control.type === Select ? { labelledBy: id + '-label' } : {}) })
    : control
  return (
    <div className={join('space-y-1', props.className)}>
      <label id={id + '-label'} htmlFor={id} className="block text-sm font-medium text-ink">
        {props.label}
      </label>
      {child}
      {props.hint && (
        <p id={id + '-hint'} className="text-sm text-muted">
          {props.hint}
        </p>
      )}
      {props.error && (
        <p id={id + '-error'} className="text-sm text-danger">
          {props.error}
        </p>
      )}
    </div>
  )
}

export const TextInput = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function TextInput({ className, ...props }, ref) {
  return <input ref={ref} className={join(inputClass, className)} {...props} />
})

export const TextArea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function TextArea({ className, ...props }, ref) {
  return <textarea ref={ref} className={join(inputClass, 'min-h-24', className)} {...props} />
})

/** SearchInput is a search box with its own label, for a toolbar. */
export function SearchInput(props: { label: string; value: string; onChange: (value: string) => void; className?: string }) {
  return <input type="search" aria-label={props.label} placeholder={props.label} value={props.value} onChange={(e) => props.onChange(e.target.value)} className={join(inputClass, 'min-w-48', props.className)} />
}

export interface Option<T extends string = string> {
  value: T
  label: string
}

/**
 * Select is the app's dropdown: a button that opens a listbox, with the
 * keyboard (arrows, Home, End, Enter, Escape, type a letter) and a screen
 * reader (combobox and options) as the browser's own has, and the app's
 * look, which the browser's own never takes. `label` names it (shown as
 * "Label: value" when `prefix` is set, for a toolbar); inside a Field the
 * label is the Field's.
 */
export function Select<T extends string>(props: {
  value: T
  onChange: (value: T) => void
  options: readonly Option<T>[]
  label?: string
  /** Show the label before the value on the button ("Status: Draft"). */
  prefix?: boolean
  name?: string
  id?: string
  labelledBy?: string
  className?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const root = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLUListElement>(null)
  const typed = useRef({ text: '', at: 0 })
  const ownId = useId()
  const id = props.id ?? ownId
  const selected = props.options.findIndex((o) => o.value === props.value)
  const current = props.options[selected]

  useEffect(() => {
    if (!open) return
    setActive(Math.max(0, selected))
    const away = (e: MouseEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open, selected])
  useEffect(() => {
    if (open) list.current?.focus()
  }, [open])
  useEffect(() => {
    list.current?.children[active]?.scrollIntoView?.({ block: 'nearest' })
  }, [active, open])

  const choose = (i: number) => {
    const option = props.options[i]
    if (option && option.value !== props.value) props.onChange(option.value)
    setOpen(false)
    button.current?.focus()
  }
  const move = (to: number) => setActive(Math.min(props.options.length - 1, Math.max(0, to)))
  const onKey = (e: KeyboardEvent) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        if (!open) setOpen(true)
        else move(active + 1)
        return
      case 'ArrowUp':
        e.preventDefault()
        if (!open) setOpen(true)
        else move(active - 1)
        return
      case 'Home':
        if (open) {
          e.preventDefault()
          move(0)
        }
        return
      case 'End':
        if (open) {
          e.preventDefault()
          move(props.options.length - 1)
        }
        return
      case 'Enter':
      case ' ':
        e.preventDefault()
        if (open) choose(active)
        else setOpen(true)
        return
      case 'Escape':
        if (open) {
          e.preventDefault()
          setOpen(false)
          button.current?.focus()
        }
        return
      case 'Tab':
        if (open) setOpen(false)
        return
    }
    if (e.key.length === 1 && !e.altKey && !e.ctrlKey && !e.metaKey) {
      // Type to jump: letters typed within a second form one prefix.
      const now = Date.now()
      typed.current = { text: (now - typed.current.at < 1000 ? typed.current.text : '') + e.key.toLowerCase(), at: now }
      const from = open ? active + 1 : selected + 1
      const all = props.options
      const hit = [...all.slice(from), ...all.slice(0, from)].findIndex((o) => o.label.toLowerCase().startsWith(typed.current.text))
      if (hit >= 0) {
        const i = (from + hit) % all.length
        if (open) setActive(i)
        else choose(i)
      }
    }
  }

  const shown = current ? (props.prefix && props.label ? `${props.label}: ${current.label}` : current.label) : (props.label ?? '')
  return (
    <div ref={root} className={join('relative', props.className)}>
      <button
        ref={button}
        type="button"
        id={id}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={id + '-list'}
        aria-label={props.labelledBy ? undefined : props.label}
        aria-labelledby={props.labelledBy}
        aria-describedby={props['aria-describedby']}
        aria-invalid={props['aria-invalid']}
        data-value={props.value}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={onKey}
        className={join(inputClass, 'flex items-center justify-between gap-2 text-left', !props.className?.includes('w-') && 'w-auto')}
      >
        <span>{shown}</span>
        <span aria-hidden="true" className="text-muted">
          ▾
        </span>
      </button>
      {props.name && <input type="hidden" name={props.name} value={props.value} />}
      {open && (
        <ul
          ref={list}
          id={id + '-list'}
          role="listbox"
          tabIndex={-1}
          aria-activedescendant={id + '-option-' + active}
          aria-labelledby={props.labelledBy ?? id}
          onKeyDown={onKey}
          className="absolute z-20 mt-1 max-h-64 min-w-full overflow-auto rounded border border-line bg-white py-1 text-sm shadow-lg focus:outline-none"
        >
          {props.options.map((o, i) => (
            // The keys are handled by the listbox above (the focused element); an option is not focusable.
            // eslint-disable-next-line jsx-a11y/click-events-have-key-events
            <li
              key={o.value}
              id={id + '-option-' + i}
              role="option"
              aria-selected={i === selected}
              onMouseEnter={() => setActive(i)}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => choose(i)}
              className={join('cursor-pointer px-3 py-1.5', i === active && 'bg-surface', i === selected && 'font-medium text-brand')}
            >
              {o.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/* ---------- overlays ---------- */

function useModal(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (open && !el.open) el.showModal()
    if (!open && el.open) el.close()
  }, [open])
  useEffect(() => {
    const el = ref.current
    if (!el) return
    // Escape fires cancel; the browser closes the element, the app's
    // state follows. A click on the backdrop closes too.
    const cancel = (e: Event) => {
      e.preventDefault()
      onClose()
    }
    const click = (e: MouseEvent) => {
      if (e.target === el) onClose()
    }
    el.addEventListener('cancel', cancel)
    el.addEventListener('click', click)
    return () => {
      el.removeEventListener('cancel', cancel)
      el.removeEventListener('click', click)
    }
  }, [onClose])
  return ref
}

const modalBase = 'm-auto rounded-lg border border-line bg-white p-0 text-ink shadow-xl backdrop:bg-ink/40 open:flex open:flex-col'

/**
 * Dialog is a modal: a title, the content, the actions in a row. It is
 * the app's confirm(): `actions` holds the Buttons, and Escape or the
 * backdrop closes it. Focus is kept inside by the browser.
 */
export function Dialog(props: { open: boolean; onClose: () => void; title: string; children?: ReactNode; actions?: ReactNode; className?: string }) {
  const ref = useModal(props.open, props.onClose)
  const titleId = useId()
  return (
    <dialog ref={ref} aria-labelledby={titleId} className={join(modalBase, 'w-[min(32rem,calc(100vw-2rem))]', props.className)}>
      <div className="space-y-4 p-6">
        <h2 id={titleId} className="text-lg font-medium">
          {props.title}
        </h2>
        {props.children && <div className="space-y-3 text-sm">{props.children}</div>}
        <Row className="justify-end">
          <Button tone="secondary" onClick={props.onClose}>
            Cancel
          </Button>
          {props.actions}
        </Row>
      </div>
    </dialog>
  )
}

/** Drawer is a panel from the side, for filters, details or a form kept beside the page. */
export function Drawer(props: { open: boolean; onClose: () => void; title: string; children: ReactNode; side?: 'right' | 'left'; className?: string }) {
  const ref = useModal(props.open, props.onClose)
  const titleId = useId()
  const side = props.side ?? 'right'
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      className={join(modalBase, 'my-0 h-dvh max-h-dvh w-[min(26rem,100vw)] rounded-none', side === 'right' ? 'ml-auto mr-0' : 'ml-0 mr-auto', props.className)}
    >
      <div className="flex items-center justify-between gap-3 border-b border-line px-5 py-4">
        <h2 id={titleId} className="text-lg font-medium">
          {props.title}
        </h2>
        <Button tone="quiet" onClick={props.onClose} aria-label="Close">
          ✕
        </Button>
      </div>
      <div className="flex-1 space-y-4 overflow-auto p-5">{props.children}</div>
    </dialog>
  )
}
