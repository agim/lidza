<!--
  Picture renders an image imported with ?responsive (vite.config.ts):
  AVIF and WebP sources the browser picks from by width, the original
  format as the fallback, and the intrinsic size so the page does not
  shift while it loads.

    import hero from './hero.jpg?responsive'
    <Picture image={hero} alt="The team at work" sizes="(min-width: 768px) 50vw, 100vw" priority />

  sizes says how wide the image is drawn, so the browser fetches one
  variant that fits: the default, 100vw, is right only for a full-width
  image. priority is for the largest image of the first screen (the LCP
  element): loaded eagerly and first. Every other image loads lazily.
-->
<script lang="ts">
  let {
    image,
    alt,
    sizes = '100vw',
    priority = false,
    class: className = '',
  }: { image: ResponsiveImage; alt: string; sizes?: string; priority?: boolean; class?: string } = $props()

  const types: Record<string, string> = { avif: 'image/avif', webp: 'image/webp' }
  const modern = $derived(Object.entries(image.sources).filter(([format]) => types[format]))
  const fallback = $derived(Object.entries(image.sources).find(([format]) => !types[format])?.[1])
</script>

<picture>
  {#each modern as [format, srcset] (format)}
    <source type={types[format]} {srcset} {sizes} />
  {/each}
  <img
    src={image.img.src}
    srcset={fallback}
    {sizes}
    width={image.img.w}
    height={image.img.h}
    {alt}
    loading={priority ? 'eager' : 'lazy'}
    decoding="async"
    fetchpriority={priority ? 'high' : 'auto'}
    class={className}
  />
</picture>
