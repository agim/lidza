/// <reference types="svelte" />
/// <reference types="vite/client" />

// An image imported with ?responsive (vite.config.ts): a srcset per
// format (avif, webp, and the fallback's) and the fallback with its
// intrinsic size, for Picture.svelte.
interface ResponsiveImage {
  sources: Record<string, string>
  img: { src: string; w: number; h: number }
}
declare module '*?responsive' {
  const image: ResponsiveImage
  export default image
}
