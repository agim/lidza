import { Component, type ErrorInfo, type ReactNode } from 'react'
import type { ErrorComponentProps } from '@tanstack/react-router'
import { reportError } from './analytics'

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

// Catches render errors below it and shows a message instead of a blank
// page. The router uses it for every route; wrap smaller parts of a page
// with it to keep the rest interactive when one part fails.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info.componentStack)
    reportError(error, { componentStack: info.componentStack })
  }

  render() {
    if (this.state.error) {
      return (
        <div role="alert" className="rounded-lg border border-danger p-4">
          <h2 className="text-lg font-medium">Something went wrong</h2>
          <pre className="my-2 overflow-x-auto text-sm">{this.state.error.message}</pre>
          <button
            className="rounded bg-brand px-3 py-1 text-white hover:bg-brand-strong"
            onClick={() => this.setState({ error: null })}
          >
            Try again
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

// TanStack Router's error component signature.
export function RouteError({ error }: ErrorComponentProps) {
  return (
    <div role="alert" className="rounded-lg border border-danger p-4">
      <h2 className="text-lg font-medium">This page failed to render</h2>
      <pre className="my-2 overflow-x-auto text-sm">{error instanceof Error ? error.message : String(error)}</pre>
    </div>
  )
}
