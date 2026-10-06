// Picture renders an image imported with ?responsive (vite.config.ts):
// AVIF and WebP sources the browser picks from by width, the original
// format as the fallback, and the intrinsic size so the page does not
// shift while it loads.
//
//   import hero from '../hero.jpg?responsive'
//   <Picture image={hero} alt="The team at work" sizes="(min-width: 768px) 50vw, 100vw" priority />
//
// sizes says how wide the image is drawn, so the browser fetches one
// variant that fits: the default, 100vw, is right only for a full-width
// image. priority is for the largest image of the first screen (the LCP
// element): loaded eagerly and first. Every other image loads lazily.

export interface ResponsiveImage {
  /** A srcset per format (avif, webp, and the fallback's). */
  sources: Record<string, string>
  img: { src: string; w: number; h: number }
}

const types: Record<string, string> = { avif: 'image/avif', webp: 'image/webp' }

export function Picture(props: { image: ResponsiveImage; alt: string; sizes?: string; priority?: boolean; className?: string }) {
  const { image, alt, sizes = '100vw', priority = false, className } = props
  const fallback = Object.entries(image.sources).find(([format]) => !types[format])?.[1]
  return (
    <picture>
      {Object.entries(image.sources)
        .filter(([format]) => types[format])
        .map(([format, srcSet]) => (
          <source key={format} type={types[format]} srcSet={srcSet} sizes={sizes} />
        ))}
      <img
        src={image.img.src}
        srcSet={fallback}
        sizes={sizes}
        width={image.img.w}
        height={image.img.h}
        alt={alt}
        loading={priority ? 'eager' : 'lazy'}
        decoding="async"
        fetchPriority={priority ? 'high' : 'auto'}
        className={className ? `${className} h-auto max-w-full` : 'h-auto max-w-full'}
      />
    </picture>
  )
}
