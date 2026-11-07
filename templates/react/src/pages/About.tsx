import { useTitle } from '../navigation'
import { Page } from '../ui'

export function About() {
  useTitle('About')
  return (
    <Page title="About">
      <p>
        This page is a client-side route, prerendered at build time. In production the Go binary serves its HTML, so a
        direct visit or a reload works too.
      </p>
    </Page>
  )
}
