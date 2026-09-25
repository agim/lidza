import { Component, type ErrorInfo, type ReactNode } from 'react'

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
  }

  render() {
    if (this.state.error) {
      return (
        <div role="alert" className="error-boundary">
          <h2>Something went wrong</h2>
          <pre>{this.state.error.message}</pre>
          <button onClick={() => this.setState({ error: null })}>Try again</button>
        </div>
      )
    }
    return this.props.children
  }
}

// TanStack Router's error component signature.
export function RouteError({ error }: { error: Error }) {
  return (
    <div role="alert" className="error-boundary">
      <h2>This page failed to render</h2>
      <pre>{error.message}</pre>
    </div>
  )
}
