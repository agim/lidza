/// <reference types="vite/client" />

// An image imported with ?responsive (vite.config.ts): a srcset per
// format and the fallback with its intrinsic size, for <Picture>.
declare module '*?responsive' {
  const image: import('./picture').ResponsiveImage
  export default image
}
