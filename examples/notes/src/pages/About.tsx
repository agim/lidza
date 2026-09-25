export function About() {
  return (
    <div className="space-y-4">
      <h1 className="text-3xl font-semibold tracking-tight">About</h1>
      <p>
        This page is a client-side route, prerendered at build time. In production the Go binary serves its HTML, so a
        direct visit or a reload works too.
      </p>
    </div>
  )
}
